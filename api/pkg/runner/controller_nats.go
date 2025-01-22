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

	// Subscribe to regular NATS messages
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

	// Execute the task via an HTTP handler
	response := c.executeTaskViaHTTP(ctx, msg.Header, req)

	responseBytes, err := json.Marshal(response)
	if err != nil {
		return err
	}
	msg.Respond(responseBytes)
	return nil
}

func (c *NatsController) executeTaskViaHTTP(ctx context.Context, headers nats.Header, task types.Request) *types.Response {
	// Parse the request URL (so we can just grab the path)
	parsedURL, err := url.Parse(task.URL)
	if err != nil {
		return &types.Response{StatusCode: 400, Body: "Unable to parse request URL"}
	}

	start := time.Now()
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

	// If the request has a reply header, we need to send the response there
	replySubject := headers.Get(pubsub.HelixNatsReplyHeader)
	if replySubject != "" {
		// Check if this response is a streaming response
		contentType := resp.Header.Get("Content-Type")
		if strings.Contains(contentType, "text/event-stream") {
			// Create a buffered reader for the response
			reader := bufio.NewReader(resp.Body)

			log.Trace().Msg("starting to stream response")
			// Stream the response chunks back
			for {
				select {
				case <-ctx.Done():
					return &types.Response{StatusCode: 500, Body: "Stream closed"}
				default:
					// Read the next chunk
					chunk, err := reader.ReadBytes('\n')
					if err == io.EOF {
						return &types.Response{StatusCode: 200, Body: "Stream closed"}
					}
					if err != nil {
						return &types.Response{StatusCode: 500, Body: "Internal Error"}
					}

					// Skip empty lines
					if len(bytes.TrimSpace(chunk)) == 0 {
						continue
					}

					log.Trace().Str("chunk", string(chunk)).Msg("received stream chunk, parsing")

					// Remove the SSE data: prefix
					chunk = bytes.TrimPrefix(chunk, []byte("data: "))

					// Try to unmarshal the chunk into a ChatCompletionStreamResponse
					var streamResp openai.ChatCompletionStreamResponse
					if err := json.Unmarshal(chunk, &streamResp); err != nil {
						log.Error().Err(err).Msg("error unmarshalling stream response")
						continue
					}

					// Create a response object for each chunk
					resp := &types.RunnerLLMInferenceResponse{
						RequestID:      headers.Get(pubsub.RequestIDHeader),
						OwnerID:        headers.Get(pubsub.OwnerIDHeader),
						SessionID:      headers.Get(pubsub.SessionIDHeader),
						InteractionID:  headers.Get(pubsub.InteractionIDHeader),
						DurationMs:     time.Since(start).Milliseconds(),
						Done:           streamResp.Choices[0].FinishReason != "",
						StreamResponse: &streamResp,
					}

					// Marshal and publish the response
					respData, err := json.Marshal(resp)
					if err != nil {
						log.Error().Err(err).Msg("error marshalling response")
						continue
					}

					log.Trace().Str("subject", replySubject).Str("response", string(respData)).Msg("publishing response")

					// Publish to the responses queue
					if err := c.pubsub.Publish(ctx, replySubject, respData); err != nil {
						log.Error().Err(err).Msg("error publishing response")
					}
				}
			}
		} else {
			// Just respond to the reply location
			body, _ := io.ReadAll(resp.Body)
			log.Trace().Msg("sending response")
			headerMap := map[string]string{}
			for k, v := range headers {
				headerMap[k] = v[0]
			}
			err = c.pubsub.PublishWithHeader(ctx, headers.Get(pubsub.HelixNatsReplyHeader), headerMap, body)
			if err != nil {
				return &types.Response{StatusCode: 500, Body: "Internal Error"}
			}
			return &types.Response{
				StatusCode: resp.StatusCode,
				Body:       string(body),
			}
		}
	}

	// Otherwise this is a normal HTTP like request/response
	body, _ := io.ReadAll(resp.Body)
	return &types.Response{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}
