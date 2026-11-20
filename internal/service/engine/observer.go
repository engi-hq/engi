package engine

import (
	"context"

	"github.com/engi-hq/engi/internal/domain"
)

// Observer receives progress events. Implementations must return quickly and
// must not call back into the engine; the worker forwards events to the bus.
type Observer interface {
	ExecutionStarted(ctx context.Context, ex ExecutionInfo)
	NodeStarted(ctx context.Context, node string, runIndex int)
	NodeFinished(ctx context.Context, node string, run domain.NodeRun)
	ExecutionFinished(ctx context.Context, result Result)
}

// NopObserver ignores every event.
type NopObserver struct{}

// ExecutionStarted implements Observer.
func (NopObserver) ExecutionStarted(context.Context, ExecutionInfo) {}

// NodeStarted implements Observer.
func (NopObserver) NodeStarted(context.Context, string, int) {}

// NodeFinished implements Observer.
func (NopObserver) NodeFinished(context.Context, string, domain.NodeRun) {}

// ExecutionFinished implements Observer.
func (NopObserver) ExecutionFinished(context.Context, Result) {}
