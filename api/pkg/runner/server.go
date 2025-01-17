package runner

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/helixml/helix/api/pkg/server"
	"github.com/helixml/helix/api/pkg/system"

	_ "net/http/pprof" // enable profiling
)

const APIPrefix = "/api/v1"

type RunnerServerOptions struct {
	Host string
	Port int
}

type HelixRunnerAPIServer struct {
	cfg *RunnerServerOptions
}

func NewHelixRunnerAPIServer(
	cfg *RunnerServerOptions,
) (*HelixRunnerAPIServer, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("server host is required")
	}

	if cfg.Port == 0 {
		return nil, fmt.Errorf("server port is required")
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

	// register pprof routes
	router.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)

	return subRouter, nil
}

func (apiServer *HelixRunnerAPIServer) healthz(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
	w.WriteHeader(http.StatusOK)
}
