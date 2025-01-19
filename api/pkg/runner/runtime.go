package runner

import (
	"context"
	"time"
)

type PullProgress struct {
	Status    string
	Completed int64
	Total     int64
}

type Model struct {
	Name              string    `json:"model"`
	ModifiedAt        time.Time `json:"modified_at"`
	Size              int64     `json:"size"`
	Digest            string    `json:"digest"`
	ParentModel       string    `json:"parent_model"`
	Format            string    `json:"format"`
	Family            string    `json:"family"`
	Families          []string  `json:"families"`
	ParameterSize     string    `json:"parameter_size"`
	QuantizationLevel string    `json:"quantization_level"`
}

type Runtime interface {
	Start(ctx context.Context) error
	Stop() error
	PullModel(ctx context.Context, modelName string, pullProgressFunc func(progress PullProgress) error) error
	ListModels(ctx context.Context) ([]Model, error)
}
