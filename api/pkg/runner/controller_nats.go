package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"bufio"

	"github.com/helixml/helix/api/pkg/pubsub"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
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
	js        jetstream.JetStream
	conn      *nats.Conn
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

	// Get the NATS connection from the pubsub interface
	natsPS, ok := config.PS.(*pubsub.Nats)
	if !ok {
		return nil, fmt.Errorf("pubsub must be a NATS implementation")
	}

	conn := natsPS.GetConnection()
	if conn == nil {
		return nil, fmt.Errorf("failed to get NATS connection")
	}

	js := natsPS.GetJetStream()
	if js == nil {
		return nil, fmt.Errorf("failed to get JetStream context")
	}

	controller := &NatsController{
		pubsub:    config.PS,
		serverURL: parsedURL.Scheme + "://" + parsedURL.Host,
		js:        js,
		conn:      conn,
	}

	// Create a valid stream name for this runner
	streamName := fmt.Sprintf("STREAM_%s", strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, pubsub.GetRunnerQueue(config.RunnerID)))

	log.Trace().Str("stream_name", streamName).Msg("creating stream")

	// Create or update the consumer for this runner
	stream, err := controller.js.Stream(ctx, streamName)
	if err == nil {
		// Stream exists, create or update consumer
		consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
			Name:      fmt.Sprintf("CONSUMER_%s", config.RunnerID),
			AckPolicy: jetstream.AckExplicitPolicy,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create consumer: %w", err)
		}

		// Start consuming messages
		msgs, err := consumer.Messages()
		if err != nil {
			return nil, fmt.Errorf("failed to get message channel: %w", err)
		}

		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					msg, err := msgs.Next()
					if err != nil {
						log.Error().Err(err).Msg("error getting next message")
						continue
					}

					// Handle the message
					if err := controller.handleJetStreamMsg(ctx, msg); err != nil {
						log.Error().Err(err).Msg("error handling jetstream message")
						msg.Nak()
						continue
					}
					msg.Ack()
				}
			}
		}()
	} else {
		log.Error().Err(err).Msg("error getting stream")
	}

	// Subscribe to regular NATS messages too
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

	return controller, nil
}

func (c *NatsController) handleJetStreamMsg(ctx context.Context, msg jetstream.Msg) error {
	log.Trace().Str("stream_name", msg.Subject()).Msg("handling jetstream message")
	// Convert JetStream message to regular NATS message for compatibility
	natsMsg := &nats.Msg{
		Subject: msg.Subject(),
		Data:    msg.Data(),
		Header:  msg.Headers(),
	}

	// Use the existing handler
	return c.handler(ctx, natsMsg)
}

func (c *NatsController) handler(ctx context.Context, msg *nats.Msg) error {
	var req types.Request
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		return err
	}

	log.Trace().Str("method", req.Method).Str("url", req.URL).Msg("received request")

	// Check if this is a streaming request
	if msg.Header.Get(pubsub.StreamingHeader) == "true" {
		log.Trace().Msg("handling streaming request")
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

	log.Trace().Interface("header", msg.Header).Msg("starting to stream response")
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
