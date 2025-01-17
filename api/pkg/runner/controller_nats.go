package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/helixml/helix/api/pkg/pubsub"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

type NatsController struct {
	pubsub pubsub.PubSub
}

func NewNatsController(ctx context.Context, ps pubsub.PubSub, runnerID string) (*NatsController, error) {
	controller := &NatsController{
		pubsub: ps,
	}

	subscription, err := ps.SubscribeWithCtx(ctx, pubsub.GetRunnerQueue(runnerID), controller.handler)
	if err != nil {
		return nil, err
	}

	go func() {
		<-ctx.Done()
		subscription.Unsubscribe()
	}()

	// Publish a message to the runner.connected queue to indicate that the runner is connected
	err = ps.Publish(ctx, pubsub.GetRunnerConnectedQueue(runnerID), []byte("connected"))
	if err != nil {
		return nil, err
	}

	return controller, nil
}

func (c *NatsController) handler(ctx context.Context, msg *nats.Msg) error {
	var req types.Request
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		return err
	}

	log.Trace().Str("method", req.Method).Str("url", req.URL).Msg("received request")

	// Execute the task via an HTTP handler
	response := executeTaskViaHTTP(req)

	responseBytes, err := json.Marshal(response)
	if err != nil {
		return err
	}
	msg.Respond(responseBytes)
	return nil
}

func executeTaskViaHTTP(task types.Request) *types.Response {
	req, err := http.NewRequest(task.Method, "http://localhost:9000"+task.URL, bytes.NewBuffer([]byte(task.Body)))
	if err != nil {
		return &types.Response{StatusCode: 500, Body: "Internal Error"}
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return &types.Response{StatusCode: 500, Body: "Internal Error"}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return &types.Response{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}
