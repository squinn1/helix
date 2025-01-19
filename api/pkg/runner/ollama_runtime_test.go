package runner

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/helixml/helix/api/pkg/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
)

func setupMockCommander(t *testing.T, ctrl *gomock.Controller, port int, cmdFn func(context.Context, string, ...string) *exec.Cmd) {
	mockCommander := NewMockCommander(ctrl)

	// Mock LookPath to return a fake ollama path
	mockCommander.EXPECT().
		LookPath("ollama").
		Return("/usr/local/bin/ollama", nil)

	// Mock CommandContext with the provided command function or use default
	if cmdFn == nil {
		cmdFn = func(ctx context.Context, name string, args ...string) *exec.Cmd {
			cmd := exec.Command("echo", fmt.Sprintf("mock ollama server started on port %d", port))
			// Need to start a dummy server to handle the health check
			t.Logf("Starting dummy server on port %d", port)
			go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"version": "test"}`))
			}))
			return cmd
		}
	}

	mockCommander.EXPECT().
		CommandContext(gomock.Any(), "/usr/local/bin/ollama", "serve").
		DoAndReturn(cmdFn)

	// Store the original commander and restore it after the test
	originalCommander := ollamaCommander
	ollamaCommander = mockCommander
	t.Cleanup(func() {
		ollamaCommander = originalCommander
	})
}

func TestOllamaRuntime_Start(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tests := []struct {
		name          string
		setup         func(t *testing.T) (*ollamaRuntime, context.Context)
		expectedError string
	}{
		{
			name: "successful start with real ollama",
			setup: func(t *testing.T) (*ollamaRuntime, context.Context) {
				// Skip test if ollama is not installed
				if _, err := exec.LookPath("ollama"); err != nil {
					t.Skip("ollama not found in PATH, skipping test")
				}

				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{})
				require.NoError(t, err)
				return runtime, context.Background()
			},
		},
		{
			name: "successful start mocked",
			setup: func(t *testing.T) (*ollamaRuntime, context.Context) {
				port, err := freeport.GetFreePort()
				require.Nil(t, err)

				setupMockCommander(t, ctrl, port, nil)

				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{
					Port: &port,
				})
				require.NoError(t, err)
				return runtime, context.Background()
			},
		},
		{
			name: "context cancelled",
			setup: func(t *testing.T) (*ollamaRuntime, context.Context) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Cancel immediately
				runtime, err := NewOllamaRuntime(ctx, OllamaRuntimeParams{})
				require.NoError(t, err)
				return runtime, ctx
			},
			expectedError: "context canceled",
		},
		{
			name: "context timeout",
			setup: func(t *testing.T) (*ollamaRuntime, context.Context) {
				port, err := freeport.GetFreePort()
				require.NoError(t, err)

				setupMockCommander(t, ctrl, port, func(ctx context.Context, name string, args ...string) *exec.Cmd {
					return exec.Command("sleep", "10") // Command that will be interrupted
				})

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond) // Ensure timeout

				runtime, err := NewOllamaRuntime(ctx, OllamaRuntimeParams{
					Port: &port,
				})
				require.NoError(t, err)
				return runtime, ctx
			},
			expectedError: "context deadline exceeded",
		},
		{
			name: "port already in use",
			setup: func(t *testing.T) (*ollamaRuntime, context.Context) {
				// Create a listener to occupy a port
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				t.Cleanup(func() {
					listener.Close()
				})

				port := listener.Addr().(*net.TCPAddr).Port

				timeout := 1 * time.Second
				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{
					Port:         &port,
					StartTimeout: &timeout,
				})
				require.NoError(t, err)
				return runtime, context.Background()
			},
			expectedError: "is already in use",
		},
		{
			name: "stop with permission error",
			setup: func(t *testing.T) (*ollamaRuntime, context.Context) {
				port, err := freeport.GetFreePort()
				require.NoError(t, err)

				setupMockCommander(t, ctrl, port, func(ctx context.Context, name string, args ...string) *exec.Cmd {
					cmd := exec.Command("sleep", "1")
					cmd.SysProcAttr = &syscall.SysProcAttr{
						Credential: &syscall.Credential{
							Uid: 0, // Root user
							Gid: 0,
						},
					}
					go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusOK)
					}))
					return cmd
				})

				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{
					Port: &port,
				})
				require.NoError(t, err)
				return runtime, context.Background()
			},
			expectedError: "not permitted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime, ctx := tt.setup(t)
			err := runtime.Start(ctx)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
				// Cleanup
				err = runtime.Stop()
				assert.NoError(t, err)
			}
		})
	}
}

func TestOllamaRuntime_Stop(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tests := []struct {
		name          string
		setup         func(t *testing.T) *ollamaRuntime
		expectedError string
		checkCleanup  func(t *testing.T, runtime *ollamaRuntime)
	}{
		{
			name: "successful stop with real ollama",
			setup: func(t *testing.T) *ollamaRuntime {
				// Skip if ollama not installed
				if _, err := exec.LookPath("ollama"); err != nil {
					t.Skip("ollama not found in PATH, skipping test")
				}

				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{})
				require.NoError(t, err)
				err = runtime.Start(context.Background())
				require.NoError(t, err)
				return runtime
			},
			checkCleanup: func(t *testing.T, runtime *ollamaRuntime) {
				// Verify port is released
				conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", runtime.port))
				assert.Error(t, err, "port should be released")
				if err == nil {
					conn.Close()
				}
			},
		},
		{
			name: "stop without start",
			setup: func(t *testing.T) *ollamaRuntime {
				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{})
				require.NoError(t, err)
				return runtime
			},
		},
		{
			name: "double stop",
			setup: func(t *testing.T) *ollamaRuntime {
				port, err := freeport.GetFreePort()
				require.Nil(t, err)

				setupMockCommander(t, ctrl, port, nil)

				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{
					Port: &port,
				})
				require.NoError(t, err)
				err = runtime.Start(context.Background())
				require.NoError(t, err)
				err = runtime.Stop()
				require.NoError(t, err)
				return runtime
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := tt.setup(t)
			err := runtime.Stop()

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}

			if tt.checkCleanup != nil {
				tt.checkCleanup(t, runtime)
			}
		})
	}
}

func TestOllamaRuntime_PullModel(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tests := []struct {
		name          string
		setup         func(t *testing.T) *ollamaRuntime
		modelName     string
		expectedError string
	}{
		{
			name: "pull model with real ollama",
			setup: func(t *testing.T) *ollamaRuntime {
				// Skip test if ollama is not installed
				if _, err := exec.LookPath("ollama"); err != nil {
					t.Skip("ollama not found in PATH, skipping test")
				}

				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{})
				require.NoError(t, err)
				err = runtime.Start(context.Background())
				require.NoError(t, err)
				return runtime
			},
			modelName:     "nomic-embed-text",
			expectedError: "",
		},
		{
			name: "pull model with mocked ollama",
			setup: func(t *testing.T) *ollamaRuntime {
				port, err := freeport.GetFreePort()
				require.NoError(t, err)

				setupMockCommander(t, ctrl, port, nil)

				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{
					Port: &port,
				})
				require.NoError(t, err)
				err = runtime.Start(context.Background())
				require.NoError(t, err)
				return runtime
			},
			modelName:     "nomic-embed-text",
			expectedError: "",
		},
		{
			name: "pull without starting runtime",
			setup: func(t *testing.T) *ollamaRuntime {
				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{})
				require.NoError(t, err)
				return runtime
			},
			modelName:     "model1",
			expectedError: "ollama client not initialized",
		},
		{
			name: "pull with invalid model name",
			setup: func(t *testing.T) *ollamaRuntime {
				port, err := freeport.GetFreePort()
				require.NoError(t, err)

				setupMockCommander(t, ctrl, port, nil)

				runtime, err := NewOllamaRuntime(context.Background(), OllamaRuntimeParams{
					Port: &port,
				})
				require.NoError(t, err)
				err = runtime.Start(context.Background())
				require.NoError(t, err)
				return runtime
			},
			modelName:     "",
			expectedError: "model name cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := tt.setup(t)
			err := runtime.PullModel(context.Background(), tt.modelName, func(progress PullProgress) error {
				t.Logf("Pulling model: %s", progress.Status)
				return nil
			})

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
				err = runtime.Stop()
				assert.NoError(t, err)
			}
		})
	}
}
