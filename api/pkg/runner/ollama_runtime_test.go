package runner

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/helixml/helix/api/pkg/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
)

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

				mockCommander := NewMockCommander(ctrl)

				// Mock LookPath to return a fake ollama path
				mockCommander.EXPECT().
					LookPath("ollama").
					Return("/usr/local/bin/ollama", nil)

				// Mock CommandContext to return a command that will succeed
				mockCommander.EXPECT().
					CommandContext(gomock.Any(), "/usr/local/bin/ollama", "serve").
					DoAndReturn(func(ctx context.Context, name string, args ...string) *exec.Cmd {
						cmd := exec.Command("echo", fmt.Sprintf("mock ollama server started on port %d", port))
						// Need to start a dummy server to handle the health check
						t.Logf("Starting dummy server on port %d", port)
						go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							w.WriteHeader(http.StatusOK)
						}))
						return cmd
					})

				// Store the original commander and restore it after the test
				originalCommander := ollamaCommander
				ollamaCommander = mockCommander
				t.Cleanup(func() {
					ollamaCommander = originalCommander
				})

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
				mockCommander := NewMockCommander(ctrl)

				// Mock LookPath
				mockCommander.EXPECT().
					LookPath("ollama").
					Return("/usr/local/bin/ollama", nil)

				// Mock CommandContext
				mockCommander.EXPECT().
					CommandContext(gomock.Any(), "/usr/local/bin/ollama", "serve").
					DoAndReturn(func(ctx context.Context, name string, args ...string) *exec.Cmd {
						return exec.Command("sleep", "10") // Command that will be interrupted
					})

				// Store and restore original commander
				originalCommander := ollamaCommander
				ollamaCommander = mockCommander
				t.Cleanup(func() {
					ollamaCommander = originalCommander
				})

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond) // Ensure timeout

				runtime, err := NewOllamaRuntime(ctx, OllamaRuntimeParams{})
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
	tests := []struct {
		name          string
		setup         func(t *testing.T) *ollamaRuntime
		expectedError string
	}{
		{
			name: "successful stop",
			setup: func(t *testing.T) *ollamaRuntime {
				runtime := &ollamaRuntime{
					cacheDir: t.TempDir(),
				}
				err := runtime.Start(context.Background())
				require.NoError(t, err)
				return runtime
			},
		},
		{
			name: "stop without start",
			setup: func(t *testing.T) *ollamaRuntime {
				return &ollamaRuntime{
					cacheDir: t.TempDir(),
				}
			},
		},
		{
			name: "double stop",
			setup: func(t *testing.T) *ollamaRuntime {
				runtime := &ollamaRuntime{
					cacheDir: t.TempDir(),
				}
				err := runtime.Start(context.Background())
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
		})
	}
}

// Mock Commander for testing
type mockCommander struct {
	lookPathFunc       func(file string) (string, error)
	commandContextFunc func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func (m *mockCommander) LookPath(file string) (string, error) {
	return m.lookPathFunc(file)
}

func (m *mockCommander) CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return m.commandContextFunc(ctx, name, args...)
}
