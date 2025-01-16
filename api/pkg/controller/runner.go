package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/helixml/helix/api/pkg/pubsub"
)

type Request struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

type Response struct {
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
}

type RunnerController struct {
	pubsub pubsub.PubSub
}

func NewRunnerController(pubsub pubsub.PubSub) *RunnerController {
	return &RunnerController{
		pubsub: pubsub,
	}
}

func (r *RunnerController) Send(ctx context.Context, runnerId string, req *Request) (*Response, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("error marshalling request: %w", err)
	}

	// Publish the task to the "tasks" subject
	response, err := r.pubsub.Request(ctx, pubsub.GetRunnerQueue(runnerId), data, 1*time.Second)
	if err != nil {
		return nil, fmt.Errorf("error sending request to runner: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(response, &resp); err != nil {
		return nil, fmt.Errorf("error unmarshalling response: %w", err)
	}

	return &resp, nil
}
