package runner

import (
	"context"
)

type PullProgress struct {
	Status    string
	Completed int64
	Total     int64
}

type Runtime interface {
	Start(ctx context.Context) error
	Stop() error
	PullModel(ctx context.Context, modelName string, pullProgressFunc func(progress PullProgress) error) error
}
