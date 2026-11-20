package port

import (
	"context"

	"github.com/engi-hq/engi/internal/domain"
)

// WebhookStore persists the webhook routes of active workflows.
type WebhookStore interface {
	// Register adds a route. A route with the same method and shape that is
	// already registered fails with an error matching domain.ErrAlreadyExists.
	Register(ctx context.Context, r domain.WebhookRegistration) error
	// DeleteByWorkflow removes every route of the workflow.
	DeleteByWorkflow(ctx context.Context, id domain.WorkflowID) error
	// Find returns the route whose shape equals the (normalized) path, which
	// is how static routes are found. It wraps domain.ErrNotFound when none.
	Find(ctx context.Context, method, path string) (domain.WebhookRegistration, error)
	// ListDynamic returns the routes with parameter segments for the method
	// and segment count.
	ListDynamic(ctx context.Context, method string, segments int) ([]domain.WebhookRegistration, error)
	// ListByWorkflow returns the routes of one workflow.
	ListByWorkflow(ctx context.Context, id domain.WorkflowID) ([]domain.WebhookRegistration, error)
}

// TriggerStateStore keeps opaque per-trigger state, for example the next fire
// time of a schedule, so a restart or leader change continues where it left off.
type TriggerStateStore interface {
	// LoadTriggerState wraps domain.ErrNotFound when nothing is saved.
	LoadTriggerState(ctx context.Context, id domain.WorkflowID, node string) ([]byte, error)
	SaveTriggerState(ctx context.Context, id domain.WorkflowID, node string, state []byte) error
	// DeleteTriggerState removes the state of every trigger of the workflow.
	DeleteTriggerState(ctx context.Context, id domain.WorkflowID) error
}

// ActivationStore records why activating a workflow failed.
type ActivationStore interface {
	SetActivationError(ctx context.Context, id domain.WorkflowID, message string) error
	// ActivationError returns the recorded message, or "" when there is none.
	ActivationError(ctx context.Context, id domain.WorkflowID) (string, error)
	ClearActivationError(ctx context.Context, id domain.WorkflowID) error
}

// LeaderCallbacks are called by a LeaseProvider as leadership changes.
type LeaderCallbacks struct {
	// OnStartedLeading is called in its own goroutine when this process becomes
	// the leader. Its context is canceled when leadership is lost or the
	// provider stops; the callback must return promptly after that.
	OnStartedLeading func(ctx context.Context)
	// OnStoppedLeading is called after leadership ended. May be nil.
	OnStoppedLeading func()
}

// LeaseProvider elects one leader among the replicas that run it. Schedules,
// and pollers run only while the process leads (group-based listeners do not).
type LeaseProvider interface {
	// Run campaigns for leadership and blocks until ctx is done. A provider
	// must cancel the leader context before another process can become leader.
	Run(ctx context.Context, cb LeaderCallbacks) error
}
