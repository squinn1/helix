package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/helixml/helix/api/pkg/config"
	"github.com/helixml/helix/api/pkg/model"
	"github.com/helixml/helix/api/pkg/pubsub"
	"github.com/helixml/helix/api/pkg/types"
)

const schedulingDecisionHistorySize = 10

// InternalHelixClient utilizes Helix runners to complete chat requests. Primary
// purpose is to power internal tools
type InternalHelixServer struct {
	cfg *config.ServerConfig

	pubsub pubsub.PubSub // Used to get responses from the runners
	// controller Controller    // Used to create sessions

	queueMu sync.Mutex
	queue   []*types.RunnerLLMInferenceRequest

	schedulingDecisionsMu sync.Mutex
	schedulingDecisions   []*types.GlobalSchedulingDecision
}

func NewInternalHelixServer(cfg *config.ServerConfig, pubsub pubsub.PubSub) *InternalHelixServer {
	return &InternalHelixServer{
		cfg:    cfg,
		pubsub: pubsub,
	}
}

// TODO: move logic from controller and other places. This method would be called directly from the runner
// handler to get the next session. Pubsub is handled internally within this package
func (c *InternalHelixServer) GetNextLLMInferenceRequest(ctx context.Context, filter types.InferenceRequestFilter, runnerID string) (*types.RunnerLLMInferenceRequest, error) {
	c.queueMu.Lock()
	defer c.queueMu.Unlock()

	filteredReqs, err := filterLLMInferenceRequest(c.queue, filter)
	if err != nil {
		return nil, fmt.Errorf("error filtering requests: %w", err)
	}

	if len(filteredReqs) == 0 {
		return nil, nil
	}

	req, index := pickRequest(filteredReqs)

	c.queue = append(c.queue[:index], c.queue[index+1:]...)

	c.addSchedulingDecision(filter, runnerID, runnerID, req.SessionID, req.InteractionID)

	return req, nil
}

func (c *InternalHelixServer) enqueueRequest(req *types.RunnerLLMInferenceRequest) {
	c.queueMu.Lock()
	defer c.queueMu.Unlock()

	c.queue = append(c.queue, req)
}

func pickRequest(reqs []*types.RunnerLLMInferenceRequest) (*types.RunnerLLMInferenceRequest, int) {
	if len(reqs) == 0 {
		return nil, 0
	}

	// First look for any requests with priority
	for idx, req := range reqs {
		if req.Priority {
			return req, idx
		}
	}

	// If no requests have priority, return the first one (oldest)
	return reqs[0], 0
}

func filterLLMInferenceRequest(reqs []*types.RunnerLLMInferenceRequest, filter types.InferenceRequestFilter) ([]*types.RunnerLLMInferenceRequest, error) {
	var filteredReqs []*types.RunnerLLMInferenceRequest

	modelName := types.ModelName(filter.ModelName)

	var memoryRequirement uint64

	model, err := model.GetModel(modelName)
	if err == nil {
		memoryRequirement = model.GetMemoryRequirements(types.SessionModeInference)
	}

	for _, req := range reqs {
		if filter.ModelName != "" && types.ModelName(req.Request.Model) != filter.ModelName {
			continue
		}

		if filter.Memory != 0 && memoryRequirement > filter.Memory {
			continue
		}

		if filter.Older != 0 && req.CreatedAt.After(time.Now().Add(-filter.Older)) {
			continue
		}

		filteredReqs = append(filteredReqs, req)
	}

	return filteredReqs, nil

}

// ProcessRunnerResponse is called on both partial streaming and full responses coming from the runner
func (c *InternalHelixClient) ProcessRunnerResponse(ctx context.Context, resp *types.RunnerLLMInferenceResponse) error {
	bts, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("error marshalling runner response: %w", err)
	}

	err = c.pubsub.Publish(ctx, pubsub.GetRunnerResponsesQueue(resp.OwnerID, resp.RequestID), bts)
	if err != nil {
		return fmt.Errorf("error publishing runner response: %w", err)
	}

	return nil
}

func (c *InternalHelixServer) GetSchedulingDecision() []*types.GlobalSchedulingDecision {
	c.schedulingDecisionsMu.Lock()
	defer c.schedulingDecisionsMu.Unlock()

	// Copy scheduling decisions
	queue := make([]*types.GlobalSchedulingDecision, len(c.schedulingDecisions))
	copy(queue, c.schedulingDecisions)

	return queue
}

func (c *InternalHelixServer) addSchedulingDecision(filter types.InferenceRequestFilter, model, runnerID, sessionID, interactionID string) {

	decision := &types.GlobalSchedulingDecision{
		Created:       time.Now(),
		RunnerID:      runnerID,
		SessionID:     sessionID,
		InteractionID: interactionID,
		Filter: types.SessionFilter{
			Mode:  types.SessionModeInference,
			Older: types.Duration(filter.Older),
		},
		ModelName: types.ModelName(model),
		Mode:      types.SessionModeInference,
	}

	c.schedulingDecisions = append([]*types.GlobalSchedulingDecision{decision}, c.schedulingDecisions...)

	if len(c.schedulingDecisions) > schedulingDecisionHistorySize {
		c.schedulingDecisions = c.schedulingDecisions[:len(c.schedulingDecisions)-1]
	}
}
