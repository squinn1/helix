package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/helixml/helix/api/pkg/freeport"
	"github.com/helixml/helix/api/pkg/system"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/rs/zerolog/log"
)

var (
	axolotlCommander Commander = &RealCommander{}
)

type AxolotlRuntime struct {
	version       string
	axolotlClient *AxolotlClient
	port          int
	cmd           *exec.Cmd
	cancel        context.CancelFunc
	startTimeout  time.Duration
}

type AxolotlRuntimeParams struct {
	Port         *int           // If nil, will be assigned a random port
	StartTimeout *time.Duration // How long to wait for axolotl to start
}

func NewAxolotlRuntime(ctx context.Context, params AxolotlRuntimeParams) (*AxolotlRuntime, error) {
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
	return &AxolotlRuntime{
		port:         *params.Port,
		startTimeout: *params.StartTimeout,
	}, nil
}

func (d *AxolotlRuntime) Start(ctx context.Context) error {
	log.Debug().Msg("Starting Axolotl runtime")

	// Make sure the port is not already in use
	if isPortInUse(d.port) {
		return fmt.Errorf("port %d is already in use", d.port)
	}

	// Prepare axolotl cmd context (a cancel context)
	log.Debug().Msg("Preparing axolotl context")
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	var err error
	defer func() {
		// If there is an error at any point after this, cancel the context to cancel the cmd
		if err != nil {
			d.cancel()
		}
	}()

	// Start axolotl cmd
	cmd, err := startAxolotlCmd(ctx, axolotlCommander, d.port)
	if err != nil {
		return fmt.Errorf("error building axolotl cmd: %w", err)
	}
	d.cmd = cmd

	// Create Axolotl Client
	url, err := url.Parse(fmt.Sprintf("http://localhost:%d", d.port))
	if err != nil {
		return fmt.Errorf("error parsing axolotl url: %w", err)
	}
	d.axolotlClient, err = NewAxolotlClient(ctx, url.String())
	if err != nil {
		return fmt.Errorf("error creating axolotl client: %w", err)
	}

	// Wait for axolotl to be ready
	log.Debug().Str("url", url.String()).Dur("timeout", d.startTimeout).Msg("Waiting for diffusers to start")
	err = d.waitUntilReady(ctx)
	if err != nil {
		return fmt.Errorf("error waiting for diffusers to start: %s", err.Error())
	}
	log.Info().Msg("diffusers has started")

	// Set the version
	version, err := d.axolotlClient.Version(ctx)
	if err != nil {
		return fmt.Errorf("error getting diffusers info: %w", err)
	}
	d.version = version

	return nil
}

func (d *AxolotlRuntime) Stop() error {
	if d.cmd == nil {
		return nil
	}
	log.Info().Msg("Stopping Diffusers runtime")
	if err := killProcessTree(d.cmd.Process.Pid); err != nil {
		log.Error().Msgf("error stopping Diffusers model process: %s", err.Error())
		return err
	}
	d.cancel()
	log.Info().Msg("Diffusers runtime stopped")

	return nil
}

func (d *AxolotlRuntime) URL() string {
	return fmt.Sprintf("http://localhost:%d", d.port)
}

func (d *AxolotlRuntime) Runtime() types.Runtime {
	return types.RuntimeDiffusers
}

func (d *AxolotlRuntime) PullModel(ctx context.Context, model string, progress func(PullProgress) error) error {
	return nil
}

func (d *AxolotlRuntime) Warm(ctx context.Context, model string) error {
	return nil
}

func (d *AxolotlRuntime) Version() string {
	return d.version
}

func startAxolotlCmd(ctx context.Context, commander Commander, port int) (*exec.Cmd, error) {
	log.Trace().Msg("Preparing Axolotl command")
	var cmd *exec.Cmd
	cmd = commander.CommandContext(
		ctx,
		"uvicorn", "axolotl_finetune_server:app",
		"--host", "0.0.0.0",
		"--port", strconv.Itoa(port),
	)

	// Set the working directory to the runner dir (which makes relative path stuff easier)
	cmd.Dir = "runner"

	// Inherit all the parent environment variables
	cmd.Env = append(cmd.Env,
		os.Environ()...,
	)
	cmd.Env = append(cmd.Env,
		// Add the APP_FOLDER environment variable which is required by the old code
		fmt.Sprintf("APP_FOLDER=%s", path.Clean(path.Join("..", "..", "axolotl"))),
		// Set python to be unbuffered so we get logs in real time
		"PYTHONUNBUFFERED=1",
		// Set the log level, which is a name, but must be uppercased
		fmt.Sprintf("LOG_LEVEL=%s", strings.ToUpper(os.Getenv("LOG_LEVEL"))),
	)
	log.Trace().Interface("env", cmd.Env).Msg("Diffusers serve command")

	// Prepare stdout and stderr
	log.Trace().Msg("Preparing stdout and stderr")
	cmd.Stdout = os.Stdout
	// this buffer is so we can keep the last 10kb of stderr so if
	// there is an error we can send it to the api
	stderrBuf := system.NewLimitedBuffer(1024 * 10)
	stderrWriters := []io.Writer{os.Stderr, stderrBuf}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	go func() {
		_, err := io.Copy(io.MultiWriter(stderrWriters...), stderrPipe)
		if err != nil {
			log.Error().Msgf("Error copying stderr: %v", err)
		}
	}()

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	log.Trace().Msg("Starting Diffusers")
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("error starting Diffusers: %w", err)
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			errMsg := string(stderrBuf.Bytes())
			log.Error().Err(err).Str("stderr", errMsg).Int("exit_code", cmd.ProcessState.ExitCode()).Msg("Diffusers exited with error")

			return
		}
	}()

	return cmd, nil
}

func (d *AxolotlRuntime) waitUntilReady(ctx context.Context) error {
	startCtx, cancel := context.WithTimeout(ctx, d.startTimeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-startCtx.Done():
			return startCtx.Err()
		case <-ticker.C:
			err := d.axolotlClient.Healthz(ctx)
			if err != nil {
				continue
			}
			return nil
		}
	}
}

type AxolotlClient struct {
	client HTTPDoer
	url    string
}

func NewAxolotlClient(ctx context.Context, url string) (*AxolotlClient, error) {
	return &AxolotlClient{
		client: http.DefaultClient,
		url:    url,
	}, nil
}

func (d *AxolotlClient) Healthz(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", d.url+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("axolotl healthz returned status %d", resp.StatusCode)
	}
	return nil
}

type AxolotlVersionResponse struct {
	Version string `json:"version"`
}

func (d *AxolotlClient) Version(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", d.url+"/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("axolotl version returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var versionResp AxolotlVersionResponse
	if err := json.Unmarshal(body, &versionResp); err != nil {
		return "", err
	}
	return versionResp.Version, nil
}
