package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/helixml/helix/api/pkg/pubsub"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
	openai "github.com/sashabaranov/go-openai"
)

type NatsControllerConfig struct {
	RunnerID  string
	PS        pubsub.PubSub
	ServerURL string
}

type NatsController struct {
	pubsub    pubsub.PubSub
	serverURL string
}

func NewNatsController(ctx context.Context, config *NatsControllerConfig) (*NatsController, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Parse the server URL to make sure it's valid
	parsedURL, err := url.Parse(config.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}

	controller := &NatsController{
		pubsub:    config.PS,
		serverURL: parsedURL.Scheme + "://" + parsedURL.Host,
	}

	// Monitor connection status
	config.PS.OnConnectionStatus(func(status pubsub.ConnectionStatus) {
		switch status {
		case pubsub.Connected:
			log.Info().Str("runner_id", config.RunnerID).Msg("nats connection established")
			// Resubscribe and announce connection
			if err := controller.setupSubscription(ctx, config.RunnerID); err != nil {
				log.Error().Err(err).Msg("failed to setup subscription after reconnect")
			}
		case pubsub.Disconnected:
			log.Warn().Str("runner_id", config.RunnerID).Msg("nats connection lost")
		case pubsub.Reconnecting:
			log.Info().Str("runner_id", config.RunnerID).Msg("nats connection reconnecting")
		}
	})

	// Initial subscription setup
	if err := controller.setupSubscription(ctx, config.RunnerID); err != nil {
		return nil, err
	}

	return controller, nil
}

// setupSubscription handles the subscription and connection announcement
func (c *NatsController) setupSubscription(ctx context.Context, runnerID string) error {
	log.Debug().Str("runner_id", runnerID).Str("queue", pubsub.GetRunnerQueue(runnerID)).Msg("subscribing to NATS queue")

	subscription, err := c.pubsub.SubscribeWithCtx(ctx, pubsub.GetRunnerQueue(runnerID), c.handler)
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	go func() {
		<-ctx.Done()
		subscription.Unsubscribe()
	}()

	log.Debug().Str("runner_id", runnerID).Str("queue", pubsub.GetRunnerConnectedQueue(runnerID)).Msg("publishing to runner.connected queue")
	if err := c.pubsub.Publish(ctx, pubsub.GetRunnerConnectedQueue(runnerID), []byte("connected")); err != nil {
		return fmt.Errorf("failed to publish connected message: %w", err)
	}

	return nil
}

func (c *NatsController) handler(ctx context.Context, msg *nats.Msg) error {
	var req types.Request
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		return err
	}

	log.Trace().Str("method", req.Method).Str("url", req.URL).Msg("received request")

	// Execute the task via an HTTP handler
	response := c.executeTaskViaHTTP(ctx, msg.Header, req)

	responseBytes, err := json.Marshal(response)
	if err != nil {
		return err
	}
	msg.Respond(responseBytes)
	return nil
}

// executeTaskViaHTTP handles both LLM and generic HTTP requests
func (c *NatsController) executeTaskViaHTTP(ctx context.Context, headers nats.Header, task types.Request) *types.Response {
	log.Debug().
		Str("method", task.Method).
		Str("url", task.URL).
		Str("body_type", headers.Get(pubsub.BodyTypeHeader)).
		Msg("executing task via HTTP")

	// Parse URL for path extraction
	parsedURL, err := url.Parse(task.URL)
	if err != nil {
		log.Error().Err(err).Str("url", task.URL).Msg("failed to parse URL")
		return &types.Response{StatusCode: 400, Body: "Unable to parse request URL"}
	}

	// Route based on request type
	if headers.Get(pubsub.BodyTypeHeader) == pubsub.BodyTypeLLMInferenceRequest {
		log.Debug().Str("path", parsedURL.Path).Msg("routing to LLM handler")
		return c.handleLLMRequest(ctx, parsedURL.Path, task)
	}
	log.Debug().Str("path", parsedURL.Path).Msg("routing to generic HTTP handler")
	return c.handleGenericHTTPRequest(ctx, parsedURL.Path, task)
}

// handleGenericHTTPRequest processes standard HTTP requests
func (c *NatsController) handleGenericHTTPRequest(ctx context.Context, path string, task types.Request) *types.Response {
	log.Trace().
		Str("path", path).
		Str("method", task.Method).
		Int("body_size", len(task.Body)).
		Msg("handling generic HTTP request")

	req, err := http.NewRequestWithContext(ctx, task.Method, c.serverURL+path, bytes.NewBuffer([]byte(task.Body)))
	if err != nil {
		log.Error().Err(err).Msg("failed to create HTTP request")
		return &types.Response{StatusCode: 500, Body: "Failed to create request"}
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("failed to execute HTTP request")
		return &types.Response{StatusCode: 500, Body: "Request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error().Err(err).Msg("failed to read response body")
		return &types.Response{StatusCode: 500, Body: "Failed to read response"}
	}

	log.Trace().
		Int("status_code", resp.StatusCode).
		Int("response_size", len(body)).
		Msg("received HTTP response")

	return &types.Response{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}

// handleLLMRequest processes LLM inference requests
func (c *NatsController) handleLLMRequest(ctx context.Context, path string, task types.Request) *types.Response {
	log.Debug().
		Str("path", path).
		Str("method", task.Method).
		Msg("handling LLM request")

	// Parse LLM request
	var llmReq types.RunnerLLMInferenceRequest
	if err := json.Unmarshal([]byte(task.Body), &llmReq); err != nil {
		log.Error().Err(err).Msg("failed to parse LLM request")
		return &types.Response{StatusCode: 400, Body: "Invalid LLM request format"}
	}

	log.Trace().
		Str("request_id", llmReq.RequestID).
		Str("owner_id", llmReq.OwnerID).
		Str("session_id", llmReq.SessionID).
		Msg("parsed LLM request")

	// Create HTTP request
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(llmReq.Request); err != nil {
		log.Error().Err(err).Msg("failed to encode LLM request body")
		return &types.Response{StatusCode: 500, Body: "Failed to encode request"}
	}

	req, err := http.NewRequestWithContext(ctx, task.Method, c.serverURL+path, &body)
	if err != nil {
		log.Error().Err(err).Msg("failed to create LLM HTTP request")
		return &types.Response{StatusCode: 500, Body: "Failed to create request"}
	}

	// Execute request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("failed to execute LLM HTTP request")
		return &types.Response{StatusCode: 500, Body: "Request failed"}
	}
	defer resp.Body.Close()

	// Get response queue for publishing results
	responseQueue := pubsub.GetRunnerResponsesQueue(llmReq.OwnerID, llmReq.RequestID)
	start := time.Now()

	log.Trace().
		Str("content_type", resp.Header.Get("Content-Type")).
		Str("response_queue", responseQueue).
		Msg("received LLM response")

	// Handle streaming vs non-streaming responses
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		log.Debug().Msg("handling streaming LLM response")
		return c.handleStreamingLLMResponse(ctx, resp, responseQueue, &llmReq, start)
	}
	log.Debug().Msg("handling regular LLM response")
	return c.handleRegularLLMResponse(ctx, resp, responseQueue, &llmReq, start)
}

// handleStreamingLLMResponse processes streaming LLM responses
func (c *NatsController) handleStreamingLLMResponse(ctx context.Context, resp *http.Response, responseQueue string, llmReq *types.RunnerLLMInferenceRequest, start time.Time) *types.Response {
	log.Debug().
		Str("request_id", llmReq.RequestID).
		Str("response_queue", responseQueue).
		Msg("starting stream processing")

	reader := bufio.NewReader(resp.Body)

	for {
		select {
		case <-ctx.Done():
			log.Debug().Msg("stream processing cancelled")
			return &types.Response{StatusCode: 500, Body: "Stream closed"}
		default:
			chunk, err := reader.ReadBytes('\n')
			if err == io.EOF {
				log.Debug().Msg("stream completed")
				return &types.Response{StatusCode: 200, Body: "Stream completed"}
			}
			if err != nil {
				log.Error().Err(err).Msg("error reading stream chunk")
				return &types.Response{StatusCode: 500, Body: "Stream error"}
			}

			// Skip empty chunks
			if len(bytes.TrimSpace(chunk)) == 0 {
				log.Trace().Msg("skipping empty chunk")
				continue
			}

			log.Trace().
				Int("chunk_size", len(chunk)).
				Str("chunk", string(chunk)).
				Msg("processing stream chunk")

			// Parse SSE format
			chunk = bytes.TrimSpace(bytes.TrimPrefix(chunk, []byte("data: ")))

			// Parse OpenAI response
			var streamResp openai.ChatCompletionStreamResponse
			if err := json.Unmarshal(chunk, &streamResp); err != nil {
				log.Error().Err(err).Msg("failed to parse stream chunk")
				continue
			}

			log.Trace().
				Str("finish_reason", string(streamResp.Choices[0].FinishReason)).
				Msg("parsed stream response")

			if err := c.publishLLMResponse(ctx, responseQueue, &streamResp, nil, llmReq, start); err != nil {
				log.Error().Err(err).Msg("failed to publish response")
			}
		}
	}
}

// handleRegularLLMResponse processes non-streaming LLM responses
func (c *NatsController) handleRegularLLMResponse(ctx context.Context, resp *http.Response, responseQueue string, llmReq *types.RunnerLLMInferenceRequest, start time.Time) *types.Response {
	log.Trace().
		Str("request_id", llmReq.RequestID).
		Str("response_queue", responseQueue).
		Msg("handling regular LLM response")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error().Err(err).Msg("failed to read response body")
		return &types.Response{StatusCode: 500, Body: "Failed to read response"}
	}

	var chatResp openai.ChatCompletionResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		log.Error().Err(err).Msg("failed to parse response")
		return &types.Response{StatusCode: 500, Body: "Invalid response format"}
	}

	log.Trace().
		Str("finish_reason", string(chatResp.Choices[0].FinishReason)).
		Msg("parsed chat response")

	if err := c.publishLLMResponse(ctx, responseQueue, nil, &chatResp, llmReq, start); err != nil {
		log.Error().Err(err).Msg("failed to publish response")
		return &types.Response{StatusCode: 500, Body: "Failed to publish response"}
	}

	return &types.Response{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}

// publishLLMResponse publishes LLM responses to NATS
func (c *NatsController) publishLLMResponse(ctx context.Context, queue string, streamResp *openai.ChatCompletionStreamResponse, resp *openai.ChatCompletionResponse, req *types.RunnerLLMInferenceRequest, start time.Time) error {
	var done bool
	if streamResp != nil {
		done = streamResp.Choices[0].FinishReason != ""
	} else if resp != nil {
		done = resp.Choices[0].FinishReason != ""
	}

	log.Trace().
		Str("request_id", req.RequestID).
		Str("queue", queue).
		Bool("done", done).
		Int64("duration_ms", time.Since(start).Milliseconds()).
		Msg("publishing LLM response")

	infResponse := &types.RunnerLLMInferenceResponse{
		RequestID:     req.RequestID,
		OwnerID:       req.OwnerID,
		SessionID:     req.SessionID,
		InteractionID: req.InteractionID,
		DurationMs:    time.Since(start).Milliseconds(),
		Done:          done,
	}

	if streamResp != nil {
		infResponse.StreamResponse = streamResp
	} else {
		infResponse.Response = resp
	}

	respData, err := json.Marshal(infResponse)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	if err := c.pubsub.Publish(ctx, queue, respData); err != nil {
		log.Error().
			Err(err).
			Str("queue", queue).
			Int("response_size", len(respData)).
			Msg("failed to publish to queue")
		return fmt.Errorf("failed to publish response: %w", err)
	}

	return nil
}
