package runner

import "context"

type Runtime interface {
	Start(ctx context.Context) error
	Stop() error
}
