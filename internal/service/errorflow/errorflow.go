// Package errorflow starts error workflows. When an execution ends in error
// or crashed and its workflow names an error workflow, Notifier queues an
// execution of that workflow, in mode error, that begins at its Error Trigger
// node with an item describing the failure.
package errorflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
)

// DedupPrefix starts the dedup key of an error-workflow execution. The key is
// the prefix plus the failed execution's id, so a failure is announced once
// even if the hook runs twice (for example after a worker restart).
const DedupPrefix = "error:"

// Starter queues executions; *execution.Service implements it.
type Starter interface {
	Start(ctx context.Context, req execution.StartRequest) (domain.ExecutionID, error)
}

// Deps are the collaborators of the Notifier. All but Logger are required.
type Deps struct {
	Workflows  port.WorkflowStore
	Versions   port.WorkflowVersionStore
	Executions port.ExecutionStore
	Starter    Starter
	// TriggerType is the node type of the Error Trigger.
	TriggerType string
	Logger      *slog.Logger
}

// Notifier starts error workflows. It is safe for concurrent use.
type Notifier struct{ d Deps }

// New builds a Notifier.
func New(d Deps) (*Notifier, error) {
	if d.Workflows == nil || d.Versions == nil || d.Executions == nil || d.Starter == nil || d.TriggerType == "" {
		return nil, errors.New("error workflow notifier requires workflow, version and execution stores, a starter and the error trigger node type")
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Notifier{d: d}, nil
}

// notifies reports whether failures of mode m start an error workflow. It is
// false for manual runs, because the person running them already sees the
// error, and for error runs, because an error workflow must not trigger another.
func notifies(m domain.ExecutionMode) bool {
	switch m {
	case domain.ModeWebhook, domain.ModeTrigger, domain.ModeRetry, domain.ModeSubworkflow:
		return true
	}
	return false
}

// ExecutionFailed starts the error workflow of the failed execution's
// workflow, if there is one. It ignores executions that did not fail, modes
// that do not notify, and executions that descend from an error execution.
// Problems are logged, never returned: they must not change the outcome of the
// execution that failed.
func (n *Notifier) ExecutionFailed(ctx context.Context, ex domain.Execution) {
	if ex.Status != domain.StatusError && ex.Status != domain.StatusCrashed {
		return
	}
	if !notifies(ex.Mode) {
		return
	}
	log := n.d.Logger.With("execution", ex.ID, "workflow", ex.WorkflowID)
	v, err := n.d.Versions.GetVersion(ctx, ex.WorkflowID, ex.WorkflowVersion)
	if err != nil {
		log.Error("error workflow skipped: loading the failed workflow", "error", err)
		return
	}
	target := v.Workflow.Settings.ErrorWorkflowID
	if target == "" {
		return
	}
	log = log.With("errorWorkflow", target)
	if inErrorChain, err := n.descendsFromError(ctx, ex); err != nil {
		log.Error("error workflow skipped: walking the parent executions", "error", err)
		return
	} else if inErrorChain {
		log.Warn("error workflow skipped: the execution was started by an error workflow")
		return
	}
	ew, err := n.d.Workflows.Get(ctx, target)
	if err != nil {
		log.Warn("error workflow skipped: it cannot be loaded", "error", err)
		return
	}
	start, ok := n.triggerNode(ew)
	if !ok {
		log.Warn("error workflow skipped: it has no enabled Error Trigger node")
		return
	}
	id, err := n.d.Starter.Start(ctx, execution.StartRequest{
		WorkflowID: target, Mode: domain.ModeError, StartNode: start,
		Items: []domain.Item{failureItem(ex, v.Workflow)}, DedupKey: DedupPrefix + string(ex.ID),
	})
	switch {
	case errors.Is(err, domain.ErrDuplicate):
		log.Debug("error workflow already started for this failure", "errorExecution", id)
	case err != nil:
		log.Error("starting the error workflow failed", "error", err)
	default:
		log.Info("error workflow started", "errorExecution", id)
	}
}

// descendsFromError reports whether an ancestor of ex ran in mode error, so a
// sub-workflow of an error workflow cannot start the chain over.
func (n *Notifier) descendsFromError(ctx context.Context, ex domain.Execution) (bool, error) {
	seen := map[domain.ExecutionID]bool{ex.ID: true}
	for p := ex.ParentExecutionID; p != nil && !seen[*p]; {
		seen[*p] = true
		parent, err := n.d.Executions.Get(ctx, *p)
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("loading parent %s of execution %s: %w", *p, ex.ID, err)
		}
		if parent.Mode == domain.ModeError {
			return true, nil
		}
		p = parent.ParentExecutionID
	}
	return false, nil
}

func (n *Notifier) triggerNode(wf domain.Workflow) (string, bool) {
	for _, node := range wf.Nodes {
		if node.Type == n.d.TriggerType && !node.Disabled {
			return node.Name, true
		}
	}
	return "", false
}

// failureItem describes the failed execution for the Error Trigger.
func failureItem(ex domain.Execution, wf domain.Workflow) domain.Item {
	errInfo := map[string]any{"message": "", "description": "", "node": "", "kind": ""}
	last := ""
	if e := ex.Error; e != nil {
		errInfo["message"], errInfo["description"], errInfo["node"], errInfo["kind"] = e.Message, e.Description, e.Node, string(e.Kind)
		last = e.Node
	}
	return domain.Item{JSON: map[string]any{
		"execution": map[string]any{
			"id": string(ex.ID), "mode": string(ex.Mode), "status": string(ex.Status),
			"error": errInfo, "lastNodeExecuted": last,
		},
		"workflow": map[string]any{"id": string(wf.ID), "name": wf.Name},
	}}
}
