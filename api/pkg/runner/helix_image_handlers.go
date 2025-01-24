package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/helixml/helix/api/pkg/system"
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

	log.Trace().Str("body", string(body)).Msg("parsing nats reply request")

	var imageRequest openai.ImageRequest
	err = json.Unmarshal(body, &imageRequest)
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

	// Parse Session ID header from request
	sessionID := r.Header.Get(types.SessionIDHeader)
	if sessionID == "" {
		http.Error(w, "session id header is required", http.StatusBadRequest)
		return
	}

	// TODO(Phil)
	// Just use the standard openai image generation for now, because I haven't implemented
	// streaming in the python code yet.

	response, err := slot.Runtime.OpenAIClient().CreateImage(r.Context(), imageRequest)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create image: %s", err.Error()), http.StatusInternalServerError)
		return
	}

	// Intercept the result and upload the files to the control plane
	clientOptions := system.ClientOptions{
		Host:  s.runnerOptions.APIHost,
		Token: s.runnerOptions.APIToken,
	}
	fileHandler := NewFileHandler(s.runnerOptions.ID, clientOptions, func(response *types.RunnerTaskResponse) {
		log.Debug().Interface("response", response).Msg("File handler event")
	})
	localFiles := []string{}
	for _, image := range response.Data {
		localFiles = append(localFiles, image.URL)
	}
	resFiles, err := fileHandler.uploadFiles(sessionID, localFiles, types.FilestoreResultsDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to upload files: %w", err), http.StatusInternalServerError)
		return
	}
	// Overwrite the original urls with the new ones
	for i, image := range response.Data {
		image.URL = resFiles[i]
	}

	log.Trace().Interface("response", response).Msg("Image generation response")

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

		bts, err := json.Marshal(response)
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
