package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"

	"github.com/helixml/helix/api/pkg/data"
	"github.com/helixml/helix/api/pkg/server"
	"github.com/helixml/helix/api/pkg/system"
	"github.com/helixml/helix/api/pkg/types"

	_ "net/http/pprof" // enable profiling
)

const APIPrefix = "/api/v1"

type RunnerServerOptions struct {
	ID         string
	Host       string
	Port       int
	CLIContext context.Context
}

type HelixRunnerAPIServer struct {
	cfg        *RunnerServerOptions
	slots      map[uuid.UUID]*Slot
	cliContext context.Context
}

func NewHelixRunnerAPIServer(
	cfg *RunnerServerOptions,
) (*HelixRunnerAPIServer, error) {
	if cfg.CLIContext == nil {
		return nil, fmt.Errorf("cli context is required")
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}

	if cfg.Port == 0 {
		cfg.Port = 8080
	}

	return &HelixRunnerAPIServer{
		cfg:        cfg,
		slots:      make(map[uuid.UUID]*Slot),
		cliContext: cfg.CLIContext,
	}, nil
}

func (apiServer *HelixRunnerAPIServer) ListenAndServe(ctx context.Context, _ *system.CleanupManager) error {
	apiRouter, err := apiServer.registerRoutes(ctx)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", apiServer.cfg.Host, apiServer.cfg.Port),
		WriteTimeout:      time.Minute * 15,
		ReadTimeout:       time.Minute * 15,
		ReadHeaderTimeout: time.Minute * 15,
		IdleTimeout:       time.Minute * 60,
		Handler:           apiRouter,
	}
	return srv.ListenAndServe()
}

func (apiServer *HelixRunnerAPIServer) registerRoutes(_ context.Context) (*mux.Router, error) {
	router := mux.NewRouter()

	// we do token extraction for all routes
	// if there is a token we will assign the user if not then oh well no user it's all gravy
	router.Use(server.ErrorLoggingMiddleware)

	// any route that lives under /api/v1
	subRouter := router.PathPrefix(APIPrefix).Subrouter()
	subRouter.HandleFunc("/healthz", apiServer.healthz).Methods(http.MethodGet)
	subRouter.HandleFunc("/status", apiServer.status).Methods(http.MethodGet)
	subRouter.HandleFunc("/slots", apiServer.createSlot).Methods(http.MethodPost)
	subRouter.HandleFunc("/slots", apiServer.listSlots).Methods(http.MethodGet)
	subRouter.HandleFunc("/slots/{slot_id}", apiServer.deleteSlot).Methods(http.MethodDelete)
	subRouter.HandleFunc("/slots/{slot_id}/v1/chat/completions", apiServer.createChatCompletion).Methods(http.MethodPost, http.MethodOptions)
	subRouter.HandleFunc("/slots/{slot_id}/v1/models", apiServer.listModels).Methods(http.MethodGet, http.MethodOptions)
	subRouter.HandleFunc("/slots/{slot_id}/v1/embeddings", apiServer.createEmbedding).Methods(http.MethodPost, http.MethodOptions)

	// register pprof routes
	router.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)

	return subRouter, nil
}

func (apiServer *HelixRunnerAPIServer) healthz(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
	w.WriteHeader(http.StatusOK)
}

func (apiServer *HelixRunnerAPIServer) status(w http.ResponseWriter, r *http.Request) {
	status := &types.RunnerStatus{
		ID:      apiServer.cfg.ID,
		Created: time.Now(),
		Version: data.GetHelixVersion(),
	}
	json.NewEncoder(w).Encode(status)
	w.WriteHeader(http.StatusOK)
}

func (apiServer *HelixRunnerAPIServer) createSlot(w http.ResponseWriter, r *http.Request) {
	slot := &types.CreateRunnerSlotRequest{}
	json.NewDecoder(r.Body).Decode(slot)

	// Validate the request
	if slot.ID == uuid.Nil {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if slot.Attributes.Runtime == "" {
		http.Error(w, "runtime is required", http.StatusBadRequest)
		return
	}
	if slot.Attributes.Model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return
	}

	log.Debug().Str("slot_id", slot.ID.String()).Msg("creating slot")

	// Must pass the context from the cli to ensure that the underlying runtime continues to run so
	// long as the cli is running
	s, err := CreateSlot(apiServer.cliContext, slot.ID, apiServer.cfg.ID, slot.Attributes.Runtime, slot.Attributes.Model)
	if err != nil {
		if strings.Contains(err.Error(), "pull model manifest: file does not exist") {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	apiServer.slots[slot.ID] = s

	// TODO(Phil): Return some representation of the slot
	w.WriteHeader(http.StatusCreated)
}

func (apiServer *HelixRunnerAPIServer) listSlots(w http.ResponseWriter, r *http.Request) {
	slotList := make([]types.RunnerSlot, 0, len(apiServer.slots))
	for id, slot := range apiServer.slots {
		slotList = append(slotList, types.RunnerSlot{
			ID:      id,
			Runtime: slot.Runtime.Runtime,
			Version: slot.Runtime.Version,
			Model:   slot.Model,
		})
	}
	response := &types.ListRunnerSlotsResponse{
		Slots: slotList,
	}
	json.NewEncoder(w).Encode(response)
}

func (apiServer *HelixRunnerAPIServer) deleteSlot(w http.ResponseWriter, r *http.Request) {
	slotID := mux.Vars(r)["slot_id"]
	slotUUID, err := uuid.Parse(slotID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	slot, ok := apiServer.slots[slotUUID]
	if !ok {
		http.Error(w, "slot not found", http.StatusNotFound)
		return
	}
	err = slot.Runtime.Stop()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	delete(apiServer.slots, slotUUID)
	w.WriteHeader(http.StatusNoContent)
}
