package types

import (
	"time"

	"github.com/google/uuid"
)

type Request struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

type Response struct {
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
}

type StreamingResponse struct {
	Body string `json:"body"`
	Done bool   `json:"done"`
}

type RunnerStatus struct {
	ID          string            `json:"id"`
	Created     time.Time         `json:"created"`
	Updated     time.Time         `json:"updated"`
	Version     string            `json:"version"`
	TotalMemory uint64            `json:"total_memory"`
	FreeMemory  int64             `json:"free_memory"`
	Labels      map[string]string `json:"labels"`
}

type Runtime string

const (
	RuntimeOllama    Runtime = "ollama"
	RuntimeDiffusers Runtime = "diffusers"
)

type CreateRunnerSlotAttributes struct {
	Runtime Runtime `json:"runtime"`
	Model   string  `json:"model"`
}

type CreateRunnerSlotRequest struct {
	ID         uuid.UUID                  `json:"id"`
	Attributes CreateRunnerSlotAttributes `json:"attributes"`
}

type RunnerSlot struct {
	ID      uuid.UUID `json:"id"`
	Runtime Runtime   `json:"runtime"`
	Model   string    `json:"model"`
	Version string    `json:"version"`
	// ...
	// TODO(phil): add more fields
}

type ListRunnerSlotsResponse struct {
	Slots []RunnerSlot `json:"slots"`
}
