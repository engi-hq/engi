package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// RunRequest describes a one-shot workflow run.
type RunRequest struct {
	Workflow domain.Workflow
	// StartNode is the node to start at. Empty picks the workflow's only
	// trigger node.
	StartNode string
	// Items feed the start node. Empty means one empty item.
	Items []domain.Item
}

// Validate checks a workflow against the registered node types.
func (a *App) Validate(wf domain.Workflow) error {
	if err := a.Engine.Validate(wf); err != nil {
		return fmt.Errorf("validating workflow: %w", err)
	}
	return nil
}

// Run executes a workflow to completion. Time-based waits (the Wait node) are
// honoured by sleeping until they are due and resuming; an execution waiting
// for an external signal is returned as it is, with status waiting.
func (a *App) Run(ctx context.Context, req RunRequest) (*engine.State, error) {
	start := req.StartNode
	if start == "" {
		var err error
		if start, err = a.FindStartNode(req.Workflow); err != nil {
			return nil, err
		}
	}
	execID, err := a.ids.ExecutionID()
	if err != nil {
		return nil, fmt.Errorf("generating execution id: %w", err)
	}
	r := engine.Request{
		ExecutionID: execID, Workflow: req.Workflow, Mode: domain.ModeManual,
		StartNode: start, StartItems: req.Items,
	}
	state, err := a.Engine.Run(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("running workflow: %w", err)
	}
	for state.Status == domain.StatusWaiting && state.Pending != nil && !state.Pending.Until.IsZero() {
		if d := state.Pending.Until.Sub(a.Clock.Now()); d > 0 {
			a.Logger.Info("waiting", "node", state.Pending.Node, "for", d.Round(time.Millisecond).String())
			if err := a.Sleeper.Sleep(ctx, d); err != nil {
				return state, fmt.Errorf("waiting for %q: %w", state.Pending.Node, err)
			}
		}
		r.Resume = state
		if state, err = a.Engine.Run(ctx, r); err != nil {
			return nil, fmt.Errorf("resuming workflow: %w", err)
		}
	}
	return state, nil
}

// FindStartNode picks the node a run starts at: the only trigger node of the
// workflow, or failing that the only node without an incoming connection.
func (a *App) FindStartNode(wf domain.Workflow) (string, error) {
	var triggers []string
	for _, name := range wf.NodesInCanvasOrder() {
		n, _ := wf.NodeByName(name)
		if n.Disabled {
			continue
		}
		d, err := a.Registry.Descriptor(n.Type, n.TypeVersion)
		if err != nil {
			return "", fmt.Errorf("node %q: %w", name, err)
		}
		if d.Group == nodesdk.GroupTrigger {
			triggers = append(triggers, name)
		}
	}
	switch len(triggers) {
	case 1:
		return triggers[0], nil
	case 0:
		return a.onlyRoot(wf)
	}
	return "", fmt.Errorf("workflow has %d trigger nodes (%v); choose one with --start", len(triggers), triggers)
}

func (a *App) onlyRoot(wf domain.Workflow) (string, error) {
	hasInput := map[string]bool{}
	for _, c := range wf.Connections {
		hasInput[c.To.Node] = true
	}
	var roots []string
	for _, name := range wf.NodesInCanvasOrder() {
		if !hasInput[name] {
			roots = append(roots, name)
		}
	}
	if len(roots) != 1 {
		return "", errors.New("cannot tell where to start: the workflow has no single trigger or root node; choose one with --start")
	}
	return roots[0], nil
}
