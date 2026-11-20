package app

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// unavailableSubworkflows is the runner of a registry built without one.
type unavailableSubworkflows struct{}

func (unavailableSubworkflows) RunSubworkflow(context.Context, nodesdk.SubworkflowCall) (nodesdk.SubworkflowResult, error) {
	return nodesdk.SubworkflowResult{}, fmt.Errorf("sub-workflows: %w (run with a persistent runtime, e.g. engi run --persist)", nodesdk.ErrUnavailable)
}

// subworkflowBridge breaks a construction cycle: the Execute Workflow node is
// built with the registry, the registry with the engine, and the real runner
// needs the worker, which needs the engine. The bridge is handed to the node
// first and pointed at the real runner once the runtime exists.
type subworkflowBridge struct {
	target atomic.Pointer[nodesdk.SubworkflowRunner]
}

var _ nodesdk.SubworkflowRunner = (*subworkflowBridge)(nil)

func (b *subworkflowBridge) set(r nodesdk.SubworkflowRunner) { b.target.Store(&r) }

// RunSubworkflow implements nodesdk.SubworkflowRunner.
func (b *subworkflowBridge) RunSubworkflow(ctx context.Context, call nodesdk.SubworkflowCall) (nodesdk.SubworkflowResult, error) {
	t := b.target.Load()
	if t == nil {
		return unavailableSubworkflows{}.RunSubworkflow(ctx, call)
	}
	return (*t).RunSubworkflow(ctx, call) //nolint:wrapcheck // the real runner's errors are descriptive
}
