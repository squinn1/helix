package schedulerv2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/helixml/helix/api/pkg/pubsub"
	"github.com/helixml/helix/api/pkg/scheduler"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
	openai "github.com/sashabaranov/go-openai"
)

type RunnerController struct {
	runners []string
	mu      *sync.RWMutex
	ps      pubsub.PubSub
	ctx     context.Context
}

type RunnerControllerConfig struct {
	Context context.Context
	PubSub  pubsub.PubSub
}

func NewRunnerController(ctx context.Context, cfg *RunnerControllerConfig) (*RunnerController, error) {
	controller := &RunnerController{
		ctx:     ctx,
		ps:      cfg.PubSub,
		runners: []string{},
		mu:      &sync.RWMutex{},
	}

	sub, err := cfg.PubSub.SubscribeWithCtx(controller.ctx, pubsub.GetRunnerConnectedQueue("*"), func(ctx context.Context, msg *nats.Msg) error {
		log.Info().Str("subject", msg.Subject).Str("data", string(msg.Data)).Msg("runner connected")
		runnerID, err := pubsub.ParseRunnerID(msg.Subject)
		if err != nil {
			log.Error().Err(err).Str("subject", msg.Subject).Msg("error parsing runner ID")
			return err
		}
		controller.OnConnectedHandler(runnerID)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error subscribing to runner.connected.*: %w", err)
	}
	go func() {
		<-ctx.Done()
		sub.Unsubscribe()
	}()

	return controller, nil
}

func (r *RunnerController) Send(ctx context.Context, runnerId string, req *types.Request) (*types.Response, error) {
	log.Trace().Str("runner_id", runnerId).Interface("request", req).Msg("sending request to runner")
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("error marshalling request: %w", err)
	}

	// Publish the task to the "tasks" subject
	response, err := r.ps.Request(ctx, pubsub.GetRunnerQueue(runnerId), data, 5*time.Minute) // TODO(phil): some requests are long running, so we need to make this configurable
	if err != nil {
		return nil, fmt.Errorf("error sending request to runner: %w", err)
	}

	var resp types.Response
	if err := json.Unmarshal(response, &resp); err != nil {
		return nil, fmt.Errorf("error unmarshalling response: %w", err)
	}
	log.Trace().Str("runner_id", runnerId).Interface("response", resp).Msg("received response from runner")

	return &resp, nil
}

func (c *RunnerController) OnConnectedHandler(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Add the runner to the cluster if it is not already in the cluster.
	if !slices.Contains(c.runners, id) {
		c.runners = append(c.runners, id)
	}
}

func (c *RunnerController) RunnerIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.runners
}

func (c *RunnerController) TotalMemory(runnerID string) uint64 {
	status, err := c.getStatus(runnerID)
	if err != nil {
		log.Error().Err(err).Msg("error getting runner status")
		return 0
	}
	return uint64(status.TotalMemory)
}

func (c *RunnerController) FreeMemory(runnerID string) uint64 {
	status, err := c.getStatus(runnerID)
	if err != nil {
		log.Error().Err(err).Msg("error getting runner status")
		return 0
	}
	return uint64(status.FreeMemory)
}

func (c *RunnerController) Version(runnerID string) string {
	status, err := c.getStatus(runnerID)
	if err != nil {
		log.Error().Err(err).Msg("error getting runner status")
		return ""
	}
	return status.Version
}

func (c *RunnerController) Slots(runnerID string) ([]types.RunnerSlot, error) {
	// Get the slots from the runner.
	slots, err := c.getSlots(runnerID)
	if err != nil {
		return nil, err
	}
	return slots.Slots, nil
}

func (c *RunnerController) SubmitChatCompletionRequest(slot *scheduler.Slot, req *types.RunnerLLMInferenceRequest) error {
	req.Request.Stream = false // TODO(phil): not sure how to handle streaming yet.
	body, err := json.Marshal(req.Request)
	if err != nil {
		return err
	}
	start := time.Now()
	resp, err := c.Send(c.ctx, slot.RunnerID, &types.Request{
		Method: "POST",
		URL:    fmt.Sprintf("/api/v1/slots/%s/v1/chat/completions", slot.ID),
		Body:   string(body),
	})
	if err != nil {
		return err
	}
	var res openai.ChatCompletionResponse
	if err := json.Unmarshal([]byte(resp.Body), &res); err != nil {
		return err
	}
	oldResp := &types.RunnerLLMInferenceResponse{
		RequestID:     req.RequestID,
		OwnerID:       req.OwnerID,
		SessionID:     req.SessionID,
		InteractionID: req.InteractionID,
		Response:      &res,
		DurationMs:    time.Since(start).Milliseconds(),
		Done:          true,
	}
	bts, err := json.Marshal(oldResp)
	if err != nil {
		return err
	}
	err = c.ps.Publish(c.ctx, pubsub.GetRunnerResponsesQueue(req.OwnerID, req.RequestID), bts)
	if err != nil {
		return fmt.Errorf("error publishing runner response: %w", err)
	}
	return nil
}

func (c *RunnerController) CreateSlot(slot *scheduler.Slot) error {
	req := &types.CreateRunnerSlotRequest{
		ID: slot.ID,
		Attributes: types.CreateRunnerSlotAttributes{
			Runtime: "ollama", // TODO(phil): make this configurable
			Model:   slot.ModelName().String(),
		},
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	resp, err := c.Send(c.ctx, slot.RunnerID, &types.Request{
		Method: "POST",
		URL:    "/api/v1/slots",
		Body:   string(body),
	})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("error creating slot: %s", resp.Body)
	}
	return nil
}

func (c *RunnerController) getStatus(runnerID string) (*types.RunnerStatus, error) {
	resp, err := c.Send(c.ctx, runnerID, &types.Request{
		Method: "GET",
		URL:    "/api/v1/status",
	})
	if err != nil {
		return nil, err
	}
	var status types.RunnerStatus
	if err := json.Unmarshal([]byte(resp.Body), &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func (c *RunnerController) getSlots(runnerID string) (*types.ListRunnerSlotsResponse, error) {
	resp, err := c.Send(c.ctx, runnerID, &types.Request{
		Method: "GET",
		URL:    "/api/v1/slots",
	})
	if err != nil {
		return nil, err
	}
	var slots types.ListRunnerSlotsResponse
	if err := json.Unmarshal([]byte(resp.Body), &slots); err != nil {
		return nil, err
	}
	return &slots, nil
}

func (c *RunnerController) SubmitStreamingChatCompletionRequest(ctx context.Context, slot *scheduler.Slot, req *types.RunnerLLMInferenceRequest) error {
	req.Request.Stream = true // Ensure streaming is enabled
	body, err := json.Marshal(req.Request)
	if err != nil {
		return fmt.Errorf("error marshalling request: %w", err)
	}

	// Create a request to the runner's chat completion endpoint
	runnerRequest := &types.Request{
		Method: "POST",
		URL:    fmt.Sprintf("/api/v1/slots/%s/v1/chat/completions", slot.ID),
		Body:   string(body),
	}

	// Convert the runner request to bytes
	requestData, err := json.Marshal(runnerRequest)
	if err != nil {
		return fmt.Errorf("error marshalling runner request: %w", err)
	}

	// Use NATS streaming to send the request and get a response channel
	responseCh, err := c.ps.StreamChatRequest(ctx, pubsub.GetRunnerQueue(slot.RunnerID), runnerRequest.URL, requestData, map[string]string{
		"request_id":     req.RequestID,
		"owner_id":       req.OwnerID,
		"session_id":     req.SessionID,
		"interaction_id": req.InteractionID,
	})
	if err != nil {
		return fmt.Errorf("error initiating streaming request: %w", err)
	}

	// Process streaming responses in a goroutine
	go func() {
		start := time.Now()
		for chunk := range responseCh {
			// Try to unmarshal the chunk into a ChatCompletionStreamResponse
			var streamResp openai.ChatCompletionStreamResponse
			if err := json.Unmarshal(chunk, &streamResp); err != nil {
				log.Error().Err(err).Msg("error unmarshalling stream response")
				continue
			}

			// Create a response object for each chunk
			resp := &types.RunnerLLMInferenceResponse{
				RequestID:     req.RequestID,
				OwnerID:       req.OwnerID,
				SessionID:     req.SessionID,
				InteractionID: req.InteractionID,
				DurationMs:    time.Since(start).Milliseconds(),
				Done:          streamResp.Choices[0].FinishReason != "",
			}

			// Convert stream response to regular response format
			resp.Response = &openai.ChatCompletionResponse{
				ID:      streamResp.ID,
				Object:  streamResp.Object,
				Created: streamResp.Created,
				Model:   streamResp.Model,
				Choices: []openai.ChatCompletionChoice{
					{
						Index: streamResp.Choices[0].Index,
						Message: openai.ChatCompletionMessage{
							Role:    streamResp.Choices[0].Delta.Role,
							Content: streamResp.Choices[0].Delta.Content,
						},
						FinishReason: streamResp.Choices[0].FinishReason,
					},
				},
			}

			// Marshal and publish the response
			respData, err := json.Marshal(resp)
			if err != nil {
				log.Error().Err(err).Msg("error marshalling response")
				continue
			}

			// Publish to the responses queue
			if err := c.ps.Publish(ctx, pubsub.GetRunnerResponsesQueue(req.OwnerID, req.RequestID), respData); err != nil {
				log.Error().Err(err).Msg("error publishing response")
			}
		}
	}()

	return nil
}
