package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListModels(t *testing.T) {
	mockModelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": [{"id": "ada-v2"}]}`))
	}))
	defer mockModelServer.Close()

	existingSlotID := uuid.New()
	existingModel := openai.AdaEmbeddingV2
	client, err := CreateOpenaiClient(context.Background(), mockModelServer.URL)
	require.NoError(t, err)
	existingSlot := &Slot{
		ID:      existingSlotID,
		Model:   string(existingModel),
		Runtime: &OllamaRuntime{},
	}

	tests := []struct {
		name         string
		slotID       string
		setupServer  func() *HelixRunnerAPIServer
		expectedCode int
		expectedBody string
		method       string
	}{
		{
			name:   "invalid UUID format",
			slotID: "invalid-uuid",
			setupServer: func() *HelixRunnerAPIServer {
				return &HelixRunnerAPIServer{
					slots: make(map[uuid.UUID]*Slot),
				}
			},
			expectedCode: http.StatusBadRequest,
			expectedBody: "invalid slot id: invalid-uuid",
			method:       http.MethodGet,
		},
		{
			name:   "slot not found",
			slotID: uuid.New().String(),
			setupServer: func() *HelixRunnerAPIServer {
				return &HelixRunnerAPIServer{
					slots: make(map[uuid.UUID]*Slot),
				}
			},
			expectedCode: http.StatusNotFound,
			expectedBody: "slot .* not found",
			method:       http.MethodGet,
		},
		{
			name:   "openai client not initialized",
			slotID: existingSlotID.String(),
			setupServer: func() *HelixRunnerAPIServer {
				slotID := existingSlotID
				server := &HelixRunnerAPIServer{
					cfg:   &RunnerServerOptions{ID: "test-runner"},
					slots: make(map[uuid.UUID]*Slot),
				}
				server.slots[slotID] = existingSlot
				return server
			},
			expectedCode: http.StatusInternalServerError,
			expectedBody: "openai client not initialized, please start the runtime first",
			method:       http.MethodGet,
		},
		{
			name:   "test valid list models request",
			slotID: existingSlotID.String(),
			setupServer: func() *HelixRunnerAPIServer {
				server := &HelixRunnerAPIServer{
					cfg:   &RunnerServerOptions{ID: "test-runner"},
					slots: make(map[uuid.UUID]*Slot),
				}
				server.slots[existingSlotID] = existingSlot
				server.slots[existingSlotID].Runtime.OpenAIClient = client
				return server
			},
			expectedCode: http.StatusOK,
			method:       http.MethodGet,
		},
		{
			name:   "options request",
			slotID: existingSlotID.String(),
			setupServer: func() *HelixRunnerAPIServer {
				server := &HelixRunnerAPIServer{
					cfg:   &RunnerServerOptions{ID: "test-runner"},
					slots: make(map[uuid.UUID]*Slot),
				}
				server.slots[existingSlotID] = existingSlot
				return server
			},
			expectedCode: http.StatusOK,
			method:       http.MethodOptions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()

			req, err := http.NewRequest(tt.method, "/v1/slots/"+tt.slotID+"/models", nil)
			assert.NoError(t, err)

			vars := map[string]string{
				"slot_id": tt.slotID,
			}
			req = mux.SetURLVars(req, vars)

			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(server.listModels)
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedCode, rr.Code)

			if tt.expectedBody != "" {
				assert.Regexp(t, tt.expectedBody, rr.Body.String())
			}

			if tt.method == http.MethodOptions {
				assert.Contains(t, rr.Header().Get("Access-Control-Allow-Origin"), "*")
			}
		})
	}
}
