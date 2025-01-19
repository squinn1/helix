package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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
	ID          string
	Host        string
	Port        int
	SlotFactory SlotFactory
}

type HelixRunnerAPIServer struct {
	cfg *RunnerServerOptions
}

func NewHelixRunnerAPIServer(
	cfg *RunnerServerOptions,
) (*HelixRunnerAPIServer, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}

	if cfg.Port == 0 {
		cfg.Port = 8080
	}

	return &HelixRunnerAPIServer{
		cfg: cfg,
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

	log.Debug().Str("slot_id", slot.ID.String()).Msg("creating slot")

	_, err := apiServer.cfg.SlotFactory.CreateSlot(r.Context(), slot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// TODO(Phil): Return some representation of the slot
	w.WriteHeader(http.StatusCreated)
}
