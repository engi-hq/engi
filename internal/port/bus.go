package port

import (
	"context"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

// EventKind says what happened.
type EventKind string

// Event kinds.
const (
	// EventExecutionStarted: a worker began (or resumed) running an execution.
	EventExecutionStarted EventKind = "execution.started"
	// EventNodeStarted: a node run began.
	EventNodeStarted EventKind = "node.started"
	// EventNodeFinished: a node run ended; Run holds its record.
	EventNodeFinished EventKind = "node.finished"
	// EventExecutionFinished: the execution reached a status; Status says which
	// (waiting counts: the run stopped, though the execution is not over).
	EventExecutionFinished EventKind = "execution.finished"
	// EventCancelRequested: somebody asked to cancel the execution. The worker
	// running it stops it.
	EventCancelRequested EventKind = "execution.cancel"
	// EventWorkflowActivated: a workflow's triggers were registered.
	EventWorkflowActivated EventKind = "workflow.activated"
	// EventWorkflowDeactivated: a workflow's triggers were removed.
	EventWorkflowDeactivated EventKind = "workflow.deactivated"
)

// Event is one notification on the Bus. Which fields are set depends on Kind.
type Event struct {
	Kind        EventKind
	ExecutionID domain.ExecutionID
	WorkflowID  domain.WorkflowID
	At          time.Time
	// Node and RunIndex identify the node run of node events.
	Node     string
	RunIndex int
	// Run is the finished node run (EventNodeFinished).
	Run *domain.NodeRun
	// Status and Error describe the outcome (EventExecutionFinished).
	Status domain.ExecutionStatus
	Error  *domain.ErrorInfo
}

// EventFilter selects events. Zero fields match everything.
type EventFilter struct {
	ExecutionID domain.ExecutionID
	Kinds       []EventKind
}

// Matches reports whether e passes the filter.
func (f EventFilter) Matches(e Event) bool {
	if f.ExecutionID != "" && f.ExecutionID != e.ExecutionID {
		return false
	}
	if len(f.Kinds) == 0 {
		return true
	}
	for _, k := range f.Kinds {
		if k == e.Kind {
			return true
		}
	}
	return false
}

// Subscription delivers events until it is closed.
type Subscription interface {
	// Events is closed after Close or when the subscribing context is done.
	Events() <-chan Event
	// Close stops the delivery and releases resources. It is safe to call twice.
	Close()
}

// Bus is a best-effort publish/subscribe channel for execution events: the
// worker publishes progress, the API's live stream and synchronous callers
// subscribe. A subscriber that cannot keep up loses events rather than slowing
// the publisher; durable facts always live in the stores.
type Bus interface {
	Publish(ctx context.Context, e Event) error
	// Subscribe starts receiving matching events published from now on.
	Subscribe(ctx context.Context, f EventFilter) (Subscription, error)
}
