package scheduler

import (
	"testing"
	"time"

	"github.com/helixml/helix/api/pkg/model"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
)

const (
	testModelStr = model.Model_Ollama_Llama3_8b
)

var (
	dummyTimeout = func(runnerID string, lastActivityTime time.Time) bool {
		return false
	}
	instantTimeout = func(runnerID string, lastActivityTime time.Time) bool {
		return true
	}
	testModel, _ = model.GetModel(testModelStr)
)

func TestPlacement_MaxSpread_Simple(t *testing.T) {
	c := NewCluster(dummyTimeout)
	c.UpdateRunner(&types.RunnerState{
		ID:          "test-runner-1",
		TotalMemory: testModel.GetMemoryRequirements(types.SessionModeInference) * 2,
	})
	a := NewWorkloadAllocator(dummyTimeout)

	req := createPlacementWork("test", model.NewModel(testModelStr))

	runnerID, err := MaxSpreadStrategy(c, a, req)
	assert.NoError(t, err)
	assert.Equal(t, "test-runner-1", runnerID)
	a.AllocateNewSlot(runnerID, req)

	runnerID, err = MaxSpreadStrategy(c, a, req)
	assert.NoError(t, err)
	assert.Equal(t, "test-runner-1", runnerID)
}

func TestPlacement_MaxSpread_MultiMachine(t *testing.T) {
	c := NewCluster(dummyTimeout)
	c.UpdateRunner(&types.RunnerState{
		ID:          "test-runner-1",
		TotalMemory: 2 * testModel.GetMemoryRequirements(types.SessionModeInference),
	})
	a := NewWorkloadAllocator(dummyTimeout)
	req := createPlacementWork("test", model.NewModel(testModelStr))
	a.AllocateNewSlot("test-runner-1", req)

	// Add a second runner
	c.UpdateRunner(&types.RunnerState{
		ID:          "test-runner-2",
		TotalMemory: 2 * testModel.GetMemoryRequirements(types.SessionModeInference),
	})

	runnerID, err := MaxSpreadStrategy(c, a, req)
	assert.NoError(t, err)
	assert.Equal(t, "test-runner-2", runnerID)
}

func TestStrategy_SlotMostStaleDeleted(t *testing.T) {
	c := NewCluster(dummyTimeout)
	c.UpdateRunner(&types.RunnerState{
		ID:          "test-runner-1",
		TotalMemory: 2 * testModel.GetMemoryRequirements(types.SessionModeInference),
	})
	a := NewWorkloadAllocator(instantTimeout)
	req := createPlacementWork("test", model.NewModel(testModelStr))
	a.AllocateNewSlot("test-runner-1", req)
	req2 := createPlacementWork("test2", model.NewModel(testModelStr))
	a.AllocateNewSlot("test-runner-1", req2)

	// Simulate runner finishing work
	slots := a.RunnerSlots("test-runner-1")
	a.ReleaseSlot(slots[0].ID) // This slot should be deleted
	a.ReleaseSlot(slots[1].ID)

	// The timeout was set to instant so they both should be stale
	err := DeleteMostStaleStrategy(a, "test-runner-1", 2*testModel.GetMemoryRequirements(types.SessionModeInference), testModel.GetMemoryRequirements(types.SessionModeInference))
	assert.NoError(t, err)
	newSlots := a.RunnerSlots("test-runner-1")
	assert.Len(t, newSlots, 1)
	assert.Equal(t, slots[1].ID.String(), newSlots[0].ID.String())
}

func createPlacementWork(name string, model model.ModelName) *Workload {
	req := &types.RunnerLLMInferenceRequest{
		RequestID: name,
		Request: &openai.ChatCompletionRequest{
			Model: model.String(),
		},
	}
	work, _ := NewLLMWorkload(req)
	return work
}
