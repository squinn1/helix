package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"bufio"

	"github.com/helixml/helix/api/pkg/pubsub"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
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
	// Parse the server URL to make sure it's valid
	parsedURL, err := url.Parse(config.ServerURL)
	if err != nil {
		return nil, err
	}

	controller := &NatsController{
		pubsub:    config.PS,
		serverURL: parsedURL.Scheme + "://" + parsedURL.Host, // Just get the scheme and host
	}

	log.Debug().Str("runner_id", config.RunnerID).Str("queue", pubsub.GetRunnerQueue(config.RunnerID)).Msg("Subscribing to NATS queue")
	subscription, err := config.PS.SubscribeWithCtx(ctx, pubsub.GetRunnerQueue(config.RunnerID), controller.handler)
	if err != nil {
		return nil, err
	}

	go func() {
		<-ctx.Done()
		subscription.Unsubscribe()
	}()

	log.Debug().Str("runner_id", config.RunnerID).Str("queue", pubsub.GetRunnerConnectedQueue(config.RunnerID)).Msg("Publishing to runner.connected queue")
	err = config.PS.Publish(ctx, pubsub.GetRunnerConnectedQueue(config.RunnerID), []byte("connected"))
	if err != nil {
		return nil, err
	}

	// TODO(Phil): Also remember to register some way of detecting disconnection. It must reconnect.

	return controller, nil
}

func (c *NatsController) handler(ctx context.Context, msg *nats.Msg) error {
	var req types.Request
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		return err
	}

	log.Trace().Str("method", req.Method).Str("url", req.URL).Msg("received request")

	// Check if this is a streaming request
	if msg.Header.Get(pubsub.StreamingHeader) == "true" {
		return c.handleStreamingRequest(ctx, msg, &req)
	}

	// Execute the task via an HTTP handler
	response := c.executeTaskViaHTTP(req)

	responseBytes, err := json.Marshal(response)
	if err != nil {
		return err
	}
	msg.Respond(responseBytes)
	return nil
}

func (c *NatsController) handleStreamingRequest(ctx context.Context, msg *nats.Msg, req *types.Request) error {
	// Parse the request URL
	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		return err
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, c.serverURL+parsedURL.Path, bytes.NewBuffer([]byte(req.Body)))
	if err != nil {
		return err
	}

	// Set headers for streaming
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
	httpReq.Header.Set("Connection", "keep-alive")

	// Execute request
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create a buffered reader for the response
	reader := bufio.NewReader(resp.Body)

	// Stream the response chunks back
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Read the next chunk
			chunk, err := reader.ReadBytes('\n')
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}

			// Skip empty lines
			if len(bytes.TrimSpace(chunk)) == 0 {
				continue
			}

			// Send the chunk back through the reply subject
			log.Trace().Interface("header", msg.Header).Msg("sending response")
			if err := c.pubsub.StreamChatRespond(ctx, &pubsub.Message{
				Reply:  msg.Header.Get(pubsub.HelixNatsReplyHeader),
				Header: msg.Header,
			}, chunk); err != nil {
				return err
			}
		}
	}
}

func (c *NatsController) executeTaskViaHTTP(task types.Request) *types.Response {
	// Parse the request URL (so we can just grab the path)
	parsedURL, err := url.Parse(task.URL)
	if err != nil {
		return &types.Response{StatusCode: 400, Body: "Unable to parse request URL"}
	}

	req, err := http.NewRequest(task.Method, c.serverURL+parsedURL.Path, bytes.NewBuffer([]byte(task.Body)))
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
