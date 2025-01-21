package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/helixml/helix/api/pkg/pubsub"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
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
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("error marshalling request: %w", err)
	}

	// Publish the task to the "tasks" subject
	response, err := r.ps.Request(ctx, pubsub.GetRunnerQueue(runnerId), data, 1*time.Second)
	if err != nil {
		return nil, fmt.Errorf("error sending request to runner: %w", err)
	}

	var resp types.Response
	if err := json.Unmarshal(response, &resp); err != nil {
		return nil, fmt.Errorf("error unmarshalling response: %w", err)
	}

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

// UpdateRunner implements scheduler.Cluster.
func (c *RunnerController) UpdateRunner(props *types.RunnerState) {
	panic("unimplemented")
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
