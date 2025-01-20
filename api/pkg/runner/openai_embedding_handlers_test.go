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

func TestCreateEmbedding(t *testing.T) {
	existingSlotID := uuid.New()
	existingModel := openai.AdaEmbeddingV2
	existingSlot := &Slot{
		ID:      existingSlotID,
		Model:   string(existingModel),
		Runtime: &OllamaRuntime{},
	}

	tests := []struct {
		name             string
		slotID           string
		setupServer      func() *HelixRunnerAPIServer
		requestBody      interface{}
		expectedStatus   int
		validateResponse func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:   "invalid slot ID format",
			slotID: "invalid-uuid",
			setupServer: func() *HelixRunnerAPIServer {
				return &HelixRunnerAPIServer{cfg: &RunnerServerOptions{ID: "test-runner"}, slots: make(map[uuid.UUID]*Slot)}
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:   "invalid request body",
			slotID: existingSlotID.String(),
			setupServer: func() *HelixRunnerAPIServer {
				server := &HelixRunnerAPIServer{cfg: &RunnerServerOptions{ID: "test-runner"}, slots: make(map[uuid.UUID]*Slot)}
				server.slots[existingSlotID] = existingSlot
				return server
			},
			requestBody:    "invalid json",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:   "non-existent slot",
			slotID: uuid.New().String(),
			setupServer: func() *HelixRunnerAPIServer {
				return &HelixRunnerAPIServer{cfg: &RunnerServerOptions{ID: "test-runner"}, slots: make(map[uuid.UUID]*Slot)}
			},
			requestBody:    openai.EmbeddingRequest{},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:   "model mismatch",
			slotID: existingSlotID.String(),
			setupServer: func() *HelixRunnerAPIServer {
				server := &HelixRunnerAPIServer{cfg: &RunnerServerOptions{ID: "test-runner"}, slots: make(map[uuid.UUID]*Slot)}
				server.slots[existingSlotID] = existingSlot // Runtime.OpenAIClient is nil
				return server
			},
			requestBody: openai.EmbeddingRequest{
				Model: "different-model",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:   "uninitialized OpenAI client",
			slotID: existingSlotID.String(),
			setupServer: func() *HelixRunnerAPIServer {
				server := &HelixRunnerAPIServer{cfg: &RunnerServerOptions{ID: "test-runner"}, slots: make(map[uuid.UUID]*Slot)}
				server.slots[existingSlotID] = existingSlot // Runtime.OpenAIClient is nil
				return server
			},
			requestBody: openai.EmbeddingRequest{
				Model: existingModel,
				Input: []string{"test input"},
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:   "successful embedding request",
			slotID: existingSlotID.String(),
			setupServer: func() *HelixRunnerAPIServer {
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					response := openai.EmbeddingResponse{
						Data: []openai.Embedding{
							{
								Embedding: []float32{0.1, 0.2, 0.3},
								Index:     0,
							},
						},
					}
					json.NewEncoder(w).Encode(response)
				}))
				server := &HelixRunnerAPIServer{cfg: &RunnerServerOptions{ID: "test-runner"}, slots: make(map[uuid.UUID]*Slot)}
				server.slots[existingSlotID] = existingSlot
				client, err := CreateOpenaiClient(context.Background(), ts.URL)
				require.NoError(t, err)
				server.slots[existingSlotID].Runtime.OpenAIClient = client
				return server
			},
			requestBody: openai.EmbeddingRequest{
				Model: existingModel,
				Input: []string{"test input"},
			},
			expectedStatus: http.StatusOK,
			validateResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var resp openai.EmbeddingResponse
				err := json.NewDecoder(rec.Body).Decode(&resp)
				require.NoError(t, err)
				assert.Len(t, resp.Data, 1)
				assert.Len(t, resp.Data[0].Embedding, 3)
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

			req := httptest.NewRequest(http.MethodPost, "/v1/embeddings/"+tt.slotID, body)
			req = mux.SetURLVars(req, map[string]string{"slot_id": tt.slotID})

			// Add user context
			ctx := context.WithValue(req.Context(), userKey, types.User{
				ID: "test-user",
			})
			req = req.WithContext(ctx)

			// Create response recorder
			rec := httptest.NewRecorder()

			// Call handler
			server.createEmbedding(rec, req)

			// Check status code
			assert.Equal(t, tt.expectedStatus, rec.Code)

			// Additional response validation if provided
			if tt.validateResponse != nil {
				tt.validateResponse(t, rec)
			}
		})
	}
}

func TestCreateEmbedding_RequestBodyTooLarge(t *testing.T) {
	server := &HelixRunnerAPIServer{cfg: &RunnerServerOptions{ID: "test-runner"}, slots: make(map[uuid.UUID]*Slot)}
	slotID := uuid.New()
	server.slots[slotID] = &Slot{
		Model:   string(openai.AdaEmbeddingV2),
		Runtime: &OllamaRuntime{},
	}

	// Create a large body that exceeds the 10MB limit
	largeBody := make([]byte, 11*MEGABYTE)

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings/"+slotID.String(), bytes.NewReader(largeBody))
	req = mux.SetURLVars(req, map[string]string{"slot_id": slotID.String()})

	// Add user context
	ctx := context.WithValue(req.Context(), userKey, types.User{
		ID: "test-user",
	})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	server.createEmbedding(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
