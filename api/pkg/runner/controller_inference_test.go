package runner

import (
	"context"
	"io"
	"time"

	"github.com/helixml/helix/api/pkg/config"
	"github.com/helixml/helix/api/pkg/model"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/puzpuzpuz/xsync/v3"
	"go.uber.org/mock/gomock"
)

// MockExecCmd is a mock implementation of ExecCmd for testing
type MockExecCmd struct {
	*gomock.Controller
	startFunc  func() error
	waitFunc   func() error
	stderrPipe func() (io.ReadCloser, error)
}

func NewMockExecCmd(ctrl *gomock.Controller) *MockExecCmd {
	return &MockExecCmd{Controller: ctrl}
}

func (m *MockExecCmd) Start() error {
	if m.startFunc != nil {
		return m.startFunc()
	}
	return nil
}

func (m *MockExecCmd) Wait() error {
	if m.waitFunc != nil {
		return m.waitFunc()
	}
	return nil
}

func (m *MockExecCmd) StderrPipe() (io.ReadCloser, error) {
	if m.stderrPipe != nil {
		return m.stderrPipe()
	}
	return nil, nil
}

// MockReadCloser is a mock implementation of io.ReadCloser
type MockReadCloser struct {
	*gomock.Controller
	readFunc  func(p []byte) (n int, err error)
	closeFunc func() error
}

func NewMockReadCloser(ctrl *gomock.Controller) *MockReadCloser {
	return &MockReadCloser{Controller: ctrl}
}

func (m *MockReadCloser) Read(p []byte) (n int, err error) {
	if m.readFunc != nil {
		return m.readFunc(p)
	}
	return 0, io.EOF
}

func (m *MockReadCloser) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func createTestRunner(memoryBytes uint64, instanceTTL time.Duration) *Runner {
	return &Runner{
		Ctx: context.Background(),
		Options: RunnerOptions{
			ID:          "test-id",
			ApiHost:     "http://localhost",
			ApiToken:    "test-token",
			MemoryBytes: memoryBytes,
			Config: &config.RunnerConfig{
				Runtimes: config.Runtimes{
					Ollama: config.OllamaRuntimeConfig{
						Enabled:     true,
						InstanceTTL: instanceTTL,
					},
				},
			},
			SchedulingDecisionBufferSize: 10,
		},
		activeModelInstances: xsync.NewMapOf[string, ModelInstance](),
		schedulingDecisions:  []string{},
	}
}

func createMockModelInstance(ctrl *gomock.Controller, id string, stale bool, m *model.MockModel, sessionMode types.SessionMode) *MockModelInstance {
	instance := NewMockModelInstance(ctrl)
	instance.EXPECT().ID().Return(id).AnyTimes()
	instance.EXPECT().Stale().Return(stale).AnyTimes()
	instance.EXPECT().Model().Return(m).AnyTimes()
	instance.EXPECT().Filter().Return(types.SessionFilter{
		Mode: sessionMode,
	}).AnyTimes()
	instance.EXPECT().Stop().Return(nil).AnyTimes()
	return instance
}
