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

type RunnerStatus struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Version string    `json:"version"`
}

type Runtime string

const (
	RuntimeOllama    Runtime = "ollama"
	RuntimeDiffusers Runtime = "diffusers"
)

type CreateRunnerSlotAttributes struct {
	Runtime Runtime `json:"runtime"`
}

type CreateRunnerSlotRequest struct {
	ID         uuid.UUID                  `json:"id"`
	Attributes CreateRunnerSlotAttributes `json:"attributes"`
}
