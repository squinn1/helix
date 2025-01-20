package runner

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"time"

	"github.com/helixml/helix/api/pkg/freeport"
	"github.com/helixml/helix/api/pkg/system"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/ollama/ollama/api"
	"github.com/rs/zerolog/log"
	openai "github.com/sashabaranov/go-openai"
)

var _ Runtime = &ollamaRuntime{}

type ollamaRuntime struct {
	openaiClient *openai.Client
	ollamaClient *api.Client
	cacheDir     string
	cmd          *exec.Cmd
	cancel       context.CancelFunc
	port         int
	startTimeout time.Duration
}

type OllamaRuntimeParams struct {
	CacheDir     *string        // Where to store the models
	Port         *int           // If nil, will be assigned a random port
	StartTimeout *time.Duration // How long to wait for ollama to start
}

func NewOllamaRuntime(ctx context.Context, params OllamaRuntimeParams) (*ollamaRuntime, error) {
	defaultCacheDir := os.TempDir()
	if params.CacheDir == nil {
		params.CacheDir = &defaultCacheDir
	}

	defaultStartTimeout := 30 * time.Second
	if params.StartTimeout == nil {
		params.StartTimeout = &defaultStartTimeout
	}
	if params.Port == nil {
		port, err := freeport.GetFreePort()
		if err != nil {
			return nil, fmt.Errorf("error getting free port: %s", err.Error())
		}
		params.Port = &port
		log.Debug().Int("port", *params.Port).Msg("Found free port")
	}

	return &ollamaRuntime{
		cacheDir:     *params.CacheDir,
		port:         *params.Port,
		startTimeout: *params.StartTimeout,
	}, nil
}

func (i *ollamaRuntime) Start(ctx context.Context) error {
	log.Debug().Msg("Starting Ollama runtime")

	// Make sure the port is not already in use
	if isPortInUse(i.port) {
		return fmt.Errorf("port %d is already in use", i.port)
	}

	// Check if the cache dir exists, if not create it
	if _, err := os.Stat(i.cacheDir); os.IsNotExist(err) {
		if err := os.MkdirAll(i.cacheDir, 0755); err != nil {
			return fmt.Errorf("error creating cache dir: %s", err.Error())
		}
	}
	// Check that the cache dir is writable
	if _, err := os.Stat(i.cacheDir); os.IsPermission(err) {
		return fmt.Errorf("cache dir is not writable: %s", i.cacheDir)
	}

	// Create openai client
	config := openai.DefaultConfig("ollama")
	config.BaseURL = fmt.Sprintf("http://localhost:%d/v1", i.port)
	i.openaiClient = openai.NewClientWithConfig(config)
	log.Debug().Str("base_url", config.BaseURL).Msg("Created openai client")

	// Prepare ollama cmd context (a cancel context)
	log.Debug().Msg("Preparing ollama context")
	ctx, cancel := context.WithCancel(ctx)
	i.cancel = cancel
	var err error
	defer func() {
		// If there is an error at any point after this, cancel the context to cancel the cmd
		if err != nil {
			i.cancel()
		}
	}()

	// Start ollama cmd
	cmd, err := startOllamaCmd(ctx, ollamaCommander, i.port, i.cacheDir)
	if err != nil {
		return fmt.Errorf("error building ollama cmd: %w", err)
	}
	i.cmd = cmd

	// Create ollama client
	url, err := url.Parse(fmt.Sprintf("http://localhost:%d", i.port))
	if err != nil {
		return fmt.Errorf("error parsing ollama url: %w", err)
	}
	log.Debug().Str("url", url.String()).Msg("Creating Ollama client")
	ollamaClient := api.NewClient(url, http.DefaultClient)
	i.ollamaClient = ollamaClient

	// Wait for ollama to be ready
	log.Debug().Str("url", url.String()).Dur("timeout", i.startTimeout).Msg("Waiting for Ollama to start")
	err = i.waitUntilOllamaIsReady(ctx, i.startTimeout)
	if err != nil {
		return fmt.Errorf("error waiting for Ollama to start: %s", err.Error())
	}
	log.Info().Msg("Ollama has started")

	return nil
}

func (i *ollamaRuntime) Stop() error {
	if i.cmd == nil {
		return nil
	}
	log.Info().Msg("Stopping Ollama runtime")
	if err := killProcessTree(i.cmd.Process.Pid); err != nil {
		log.Error().Msgf("error stopping Ollama model process: %s", err.Error())
		return err
	}
	i.cancel()
	log.Info().Msg("Ollama runtime stopped")

	return nil
}

func (i *ollamaRuntime) PullModel(ctx context.Context, modelName string, pullProgressFunc func(progress PullProgress) error) error {
	if i.ollamaClient == nil {
		return fmt.Errorf("ollama client not initialized")
	}

	// Validate model name
	if modelName == "" {
		return fmt.Errorf("model name cannot be empty")
	}

	log.Info().Msgf("Pulling model: %s", modelName)
	err := i.ollamaClient.Pull(ctx, &api.PullRequest{
		Model: modelName,
	}, func(progress api.ProgressResponse) error {
		return pullProgressFunc(PullProgress{
			Status:    progress.Status,
			Completed: progress.Completed,
			Total:     progress.Total,
		})
	})
	if err != nil {
		return fmt.Errorf("error pulling model: %w", err)
	}
	log.Info().Msgf("Finished pulling model: %s", modelName)
	return nil
}

func (i *ollamaRuntime) ListModels(ctx context.Context) ([]Model, error) {
	if i.ollamaClient == nil {
		return nil, fmt.Errorf("ollama client not initialized")
	}
	models, err := i.ollamaClient.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("error listing models: %w", err)
	}

	modelList := []Model{}
	for _, model := range models.Models {
		modelList = append(modelList, Model{
			Name:              model.Model,
			ModifiedAt:        model.ModifiedAt,
			Size:              model.Size,
			Digest:            model.Digest,
			ParentModel:       model.Details.ParentModel,
			Format:            model.Details.Format,
			Family:            model.Details.Family,
			Families:          model.Details.Families,
			ParameterSize:     model.Details.ParameterSize,
			QuantizationLevel: model.Details.QuantizationLevel,
		})
	}
	return modelList, nil
}

func (i *ollamaRuntime) Info(ctx context.Context) (*Info, error) {
	version, err := i.ollamaClient.Version(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting ollama info: %w", err)
	}
	return &Info{
		Runtime: types.RuntimeOllama,
		Version: version,
	}, nil
}

func (i *ollamaRuntime) waitUntilOllamaIsReady(ctx context.Context, startTimeout time.Duration) error {
	startCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-startCtx.Done():
			return startCtx.Err()
		case <-ticker.C:
			err := i.ollamaClient.Heartbeat(ctx)
			if err != nil {
				continue
			}
			return nil
		}
	}
}

func startOllamaCmd(ctx context.Context, commander Commander, port int, cacheDir string) (*exec.Cmd, error) {
	// Find ollama on the path
	ollamaPath, err := commander.LookPath("ollama")
	if err != nil {
		return nil, fmt.Errorf("ollama not found in PATH")
	}
	log.Debug().Str("ollama_path", ollamaPath).Msg("Found ollama")

	// Prepare ollama serve command
	log.Debug().Msg("Preparing ollama serve command")
	cmd := commander.CommandContext(ctx, ollamaPath, "serve")
	ollamaHost := fmt.Sprintf("127.0.0.1:%d", port)
	cmd.Env = append(cmd.Env,
		"HOME="+os.Getenv("HOME"),
		"HTTP_PROXY="+os.Getenv("HTTP_PROXY"),
		"HTTPS_PROXY="+os.Getenv("HTTPS_PROXY"),
		"OLLAMA_KEEP_ALIVE=-1",
		"OLLAMA_HOST="+ollamaHost, // Bind on localhost with random port
		"OLLAMA_MODELS="+cacheDir, // Where to store the models
	)
	log.Debug().Interface("env", cmd.Env).Msg("Ollama serve command")

	// Prepare stdout and stderr
	log.Debug().Msg("Preparing stdout and stderr")
	cmd.Stdout = os.Stdout
	// this buffer is so we can keep the last 10kb of stderr so if
	// there is an error we can send it to the api
	stderrBuf := system.NewLimitedBuffer(1024 * 10)
	stderrWriters := []io.Writer{os.Stderr, stderrBuf}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	// stream stderr to os.Stderr (so we can see it in the logs)
	// and also the error buffer we will use to post the error to the api
	go func() {
		_, err := io.Copy(io.MultiWriter(stderrWriters...), stderrPipe)
		if err != nil {
			log.Error().Msgf("Error copying stderr: %v", err)
		}
	}()

	log.Debug().Msg("Starting ollama serve")
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("error starting Ollama model instance: %w", err)
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			errMsg := string(stderrBuf.Bytes())
			log.Error().Err(err).Str("stderr", errMsg).Int("exit_code", cmd.ProcessState.ExitCode()).Msg("Ollama exited with error")

			return
		}
	}()

	return cmd, nil
}

func isPortInUse(port int) bool {
	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
