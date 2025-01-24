package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/rs/zerolog/log"
	openai "github.com/sashabaranov/go-openai"
)

func (s *HelixRunnerAPIServer) createHelixImageGeneration(w http.ResponseWriter, r *http.Request) {
	addCorsHeaders(w)
	if r.Method == http.MethodOptions {
		return
	}

	slot_id := mux.Vars(r)["slot_id"]
	slot_uuid, err := uuid.Parse(slot_id)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid slot id: %s", slot_id), http.StatusBadRequest)
		return
	}

	slot, ok := s.slots[slot_uuid]
	if !ok {
		http.Error(w, fmt.Sprintf("slot %s not found", slot_id), http.StatusNotFound)
		return
	}
	log.Trace().Str("slot_id", slot_id).Msg("create helix image generation")

	body, err := io.ReadAll(io.LimitReader(r.Body, 10*MEGABYTE))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req types.RunnerNatsReplyRequest
	err = json.Unmarshal(body, &req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var imageRequest openai.ImageRequest
	err = json.Unmarshal(req.Request, &imageRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if imageRequest.Model == "" {
		imageRequest.Model = slot.Model
	}
	if imageRequest.Model != slot.Model {
		http.Error(w, fmt.Sprintf("model mismatch, expecting %s", slot.Model), http.StatusBadRequest)
		return
	}

	// TODO(Phil)
	// Just use the standard openai image generation for now, because I haven't implemented
	// streaming in the python code yet.

	response, err := slot.Runtime.OpenAIClient().CreateImage(r.Context(), imageRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Write the stream into the response
	for {
		// response, err := stream.Recv()
		// if errors.Is(err, io.EOF) {
		// break
		// }
		// if err != nil {
		// 	http.Error(rw, err.Error(), http.StatusInternalServerError)
		// 	return
		// }

		responseBytes, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Convert the response to a helix ImageInferenceResponse
		helixResponse := types.RunnerNatsReplyResponse{
			RequestID:     req.RequestID,
			CreatedAt:     time.Now(),
			OwnerID:       req.OwnerID,
			SessionID:     req.SessionID,
			InteractionID: req.InteractionID,
			Progress:      1.0,
			Response:      responseBytes,
		}

		// Write the response to the client
		bts, err := json.Marshal(helixResponse)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := writeChunk(w, bts); err != nil {
			log.Error().Msgf("failed to write completion chunk: %v", err)
		}
		break
	}
}
