package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/helixml/helix/api/pkg/types"
	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateChatCompletion(t *testing.T) {
	existingSlotID := uuid.New()
	existingModel := "test-model"
	existingSlot := &Slot{
		ID:      existingSlotID,
		Model:   existingModel,
		Runtime: &OllamaRuntime{},
	}
	tests := []struct {
		name             string
		slotID           string
		setupServer      func() *HelixRunnerAPIServer
		requestBody      interface{}
		expectedStatus   int
		expectedResponse string
		validateResponse func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:   "invalid slot ID format",
			slotID: "invalid-uuid",
			setupServer: func() *HelixRunnerAPIServer {
				return &HelixRunnerAPIServer{slots: make(map[uuid.UUID]*Slot)}
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:   "invalid request body",
			slotID: uuid.New().String(),
			setupServer: func() *HelixRunnerAPIServer {
				slotID := uuid.New()
				config := openai.DefaultConfig("test-key")
				config.BaseURL = "http://localhost:8080"
				client := openai.NewClientWithConfig(config)
				server := &HelixRunnerAPIServer{slots: make(map[uuid.UUID]*Slot)}
				server.slots[slotID] = &Slot{
					Model: existingModel,
					Runtime: &OllamaRuntime{
						OpenAIClient: client,
					},
				}
				return server
			},
			requestBody:    "invalid json",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:   "non-existent slot",
			slotID: uuid.New().String(),
			setupServer: func() *HelixRunnerAPIServer {
				return &HelixRunnerAPIServer{slots: make(map[uuid.UUID]*Slot)}
			},
			expectedStatus: http.StatusNotFound,
			requestBody:    openai.ChatCompletionRequest{},
		},
		{
			name:   "model mismatch",
			slotID: existingSlotID.String(),
			setupServer: func() *HelixRunnerAPIServer {
				server := &HelixRunnerAPIServer{slots: make(map[uuid.UUID]*Slot)}
				server.slots[existingSlotID] = existingSlot
				return server
			},
			requestBody: openai.ChatCompletionRequest{
				Model: "different-model",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:   "successful non-streaming request",
			slotID: existingSlotID.String(),
			setupServer: func() *HelixRunnerAPIServer {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					response := openai.ChatCompletionResponse{
						Choices: []openai.ChatCompletionChoice{
							{
								Message: openai.ChatCompletionMessage{
									Content: "test response",
								},
							},
						},
					}
					json.NewEncoder(w).Encode(response)
				}))
				server := &HelixRunnerAPIServer{slots: make(map[uuid.UUID]*Slot)}
				server.slots[existingSlotID] = existingSlot
				client, err := CreateOpenaiClient(context.Background(), ts.URL)
				require.NoError(t, err)
				server.slots[existingSlotID].Runtime.OpenAIClient = client
				return server
			},
			requestBody: openai.ChatCompletionRequest{
				Model: existingModel,
				Messages: []openai.ChatCompletionMessage{
					{
						Role:    "user",
						Content: "test message",
					},
				},
			},
			expectedStatus: http.StatusOK,
			validateResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var resp openai.ChatCompletionResponse
				err := json.NewDecoder(rec.Body).Decode(&resp)
				require.NoError(t, err)
				assert.Equal(t, "test response", resp.Choices[0].Message.Content)
			},
		},
		{
			name:   "successful streaming request",
			slotID: existingSlotID.String(),
			setupServer: func() *HelixRunnerAPIServer {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("Connection", "keep-alive")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}))
				server := &HelixRunnerAPIServer{slots: make(map[uuid.UUID]*Slot)}
				server.slots[existingSlotID] = existingSlot
				client, err := CreateOpenaiClient(context.Background(), ts.URL)
				require.NoError(t, err)
				server.slots[existingSlotID].Runtime.OpenAIClient = client
				return server
			},
			requestBody: openai.ChatCompletionRequest{
				Model:  existingModel,
				Stream: true,
				Messages: []openai.ChatCompletionMessage{
					{
						Role:    "user",
						Content: "test message",
					},
				},
			},
			expectedStatus: http.StatusOK,
			validateResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
				assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
				assert.Equal(t, "keep-alive", rec.Header().Get("Connection"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()

			// Create request
			var body io.Reader
			if tt.requestBody != nil {
				bodyBytes, err := json.Marshal(tt.requestBody)
				require.NoError(t, err)
				body = bytes.NewReader(bodyBytes)
			}

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions/"+tt.slotID, body)
			req = mux.SetURLVars(req, map[string]string{"slot_id": tt.slotID})

			// Add user context
			ctx := context.WithValue(req.Context(), userKey, types.User{
				ID: "test-user",
			})
			req = req.WithContext(ctx)

			// Create response recorder
			rec := httptest.NewRecorder()

			// Call handler
			server.createChatCompletion(rec, req)

			// Check status code
			assert.Equal(t, tt.expectedStatus, rec.Code)

			// Additional response validation if provided
			if tt.validateResponse != nil {
				tt.validateResponse(t, rec)
			}
		})
	}
}

func TestCreateChatCompletion_RequestBodyTooLarge(t *testing.T) {
	config := openai.DefaultConfig("test-key")
	config.BaseURL = "http://localhost:8080"
	client := openai.NewClientWithConfig(config)
	server := &HelixRunnerAPIServer{slots: make(map[uuid.UUID]*Slot)}
	slotID := uuid.New()
	server.slots[slotID] = &Slot{
		Model: "test-model",
		Runtime: &OllamaRuntime{
			OpenAIClient: client,
		},
	}

	// Create a large body that exceeds the 10MB limit
	largeBody := make([]byte, 11*MEGABYTE)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions/"+slotID.String(), bytes.NewReader(largeBody))
	req = mux.SetURLVars(req, map[string]string{"slot_id": slotID.String()})

	// Add user context
	ctx := context.WithValue(req.Context(), userKey, types.User{
		ID: "test-user",
	})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	server.createChatCompletion(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateChatCompletion_UninitializedClient(t *testing.T) {
	server := &HelixRunnerAPIServer{slots: make(map[uuid.UUID]*Slot)}
	slotID := uuid.New()
	server.slots[slotID] = &Slot{
		Model:   "test-model",
		Runtime: &OllamaRuntime{}, // OpenAIClient is nil
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions/"+slotID.String(), bytes.NewReader([]byte("{}")))
	req = mux.SetURLVars(req, map[string]string{"slot_id": slotID.String()})

	// Add user context
	ctx := context.WithValue(req.Context(), userKey, types.User{
		ID: "test-user",
	})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	server.createChatCompletion(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "openai client not initialized")
}
