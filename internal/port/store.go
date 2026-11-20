package port

import (
	"context"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

// Page selects one page of a listing. Lists are ordered newest first.
type Page struct {
	// Cursor is the opaque NextCursor of the previous page; empty for the first.
	Cursor string
	// Limit is the maximum number of items; the store applies its own cap when zero.
	Limit int
}

// PageResult is one page of items.
type PageResult[T any] struct {
	Items []T
	// NextCursor is empty when there are no more items.
	NextCursor string
}

// WorkflowFilter narrows a workflow listing. Zero fields match everything.
type WorkflowFilter struct {
	Active *bool
	// Tag matches workflows carrying exactly this tag.
	Tag string
	// NameContains matches names containing the text, ignoring case.
	NameContains string
}

// WorkflowStore persists workflows. Every Save also records a WorkflowVersion.
type WorkflowStore interface {
	// Save creates the workflow or updates it, assigns the next version number
	// (1 for a new workflow) and returns the stored workflow. The Version field
	// of the argument is ignored.
	Save(ctx context.Context, w domain.Workflow) (domain.Workflow, error)
	// Get returns the current workflow or an error wrapping domain.ErrNotFound.
	Get(ctx context.Context, id domain.WorkflowID) (domain.Workflow, error)
	// SetActive flips the active flag without creating a version.
	SetActive(ctx context.Context, id domain.WorkflowID, active bool) error
	// Delete removes the workflow with its versions and executions.
	Delete(ctx context.Context, id domain.WorkflowID) error
	List(ctx context.Context, f WorkflowFilter, p Page) (PageResult[domain.Workflow], error)
}

// WorkflowVersionStore reads the immutable history written by WorkflowStore.Save.
type WorkflowVersionStore interface {
	GetVersion(ctx context.Context, id domain.WorkflowID, version int) (domain.WorkflowVersion, error)
	// ListVersions returns the versions of a workflow, newest first.
	ListVersions(ctx context.Context, id domain.WorkflowID, p Page) (PageResult[domain.WorkflowVersion], error)
}

// ExecutionFilter narrows an execution listing. Zero fields match everything.
type ExecutionFilter struct {
	WorkflowID domain.WorkflowID
	Statuses   []domain.ExecutionStatus
	// StartedAfter (inclusive) and StartedBefore (exclusive) bound StartedAt.
	StartedAfter  time.Time
	StartedBefore time.Time
}

// ExecutionStore persists executions. Callers set StartedAt when they create
// an execution, even a queued one: it is the sort and retention timestamp.
type ExecutionStore interface {
	// Create inserts the execution. A repeated (WorkflowID, DedupKey) fails
	// with an error matching domain.ErrDuplicate (a *domain.DuplicateError).
	Create(ctx context.Context, e domain.Execution) error
	Get(ctx context.Context, id domain.ExecutionID) (domain.Execution, error)
	// List omits RunData to keep pages small.
	List(ctx context.Context, f ExecutionFilter, p Page) (PageResult[domain.Execution], error)
	// Delete removes the execution with its data and jobs, or returns an error
	// wrapping domain.ErrNotFound.
	Delete(ctx context.Context, id domain.ExecutionID) error
	// Update writes status, FinishedAt, WaitUntil, Error and RunData. It never
	// writes Attempts, LastRecoveredAt or the job that holds the run: those
	// change only through ClaimRun and RequeueRun, so a stale read cannot
	// undo them.
	Update(ctx context.Context, e domain.Execution) error
	// ClaimRun moves a queued execution to running for holder and counts the
	// attempt. It is one conditional write, so when two jobs race for the same
	// execution exactly one wins. A holder may also take over a run that the
	// same job already holds at an earlier attempt: a reclaimed job claimed
	// before its run was requeued. That takeover counts as a recovery at
	// claimedAt. claimed is false when another job holds the run or the
	// execution ended or waits; the returned execution is the stored one in
	// both cases.
	ClaimRun(ctx context.Context, id domain.ExecutionID, holder domain.RunHolder, claimedAt time.Time) (current domain.Execution, claimed bool, err error)
	// FinishRun writes the result of a run (status, finish time, wait, error and
	// run data) only while holder still holds the running execution. A run that
	// was requeued, taken over or ended is left alone and written is false.
	FinishRun(ctx context.Context, e domain.Execution, holder domain.RunHolder) (written bool, err error)
	// CrashRun ends an execution that its job could not finish. It is fenced like
	// FinishRun but also accepts a queued execution, which no claim holds once its
	// job ran out of attempts. A run that a newer claim holds is left alone.
	CrashRun(ctx context.Context, e domain.Execution, holder domain.RunHolder) (written bool, err error)
	// RequeueRun moves a running execution back to queued, but only while holder
	// is the stored holder of the run: a stale job or an older claim cannot
	// reset a run that a newer claim drives. recoveredAt, when set, records that
	// a dead worker lost the run. requeued is false when holder does not hold it.
	RequeueRun(ctx context.Context, id domain.ExecutionID, holder domain.RunHolder, recoveredAt *time.Time) (requeued bool, err error)
	// FindDueWaiting returns waiting executions whose WaitUntil is not after
	// now, oldest first.
	FindDueWaiting(ctx context.Context, now time.Time, limit int) ([]domain.Execution, error)
	// Prune deletes finished executions older than the cutoff of their status
	// and returns how many were removed. Statuses missing from cutoffs, and
	// non-terminal statuses, are kept.
	Prune(ctx context.Context, cutoffs map[domain.ExecutionStatus]time.Time) (int, error)
}

// ExecutionStateStore holds the serialized engine state of waiting executions.
type ExecutionStateStore interface {
	SaveState(ctx context.Context, id domain.ExecutionID, state []byte) error
	// LoadState returns an error wrapping domain.ErrNotFound when none is saved.
	LoadState(ctx context.Context, id domain.ExecutionID) ([]byte, error)
	DeleteState(ctx context.Context, id domain.ExecutionID) error
}

// CredentialStore persists encrypted credentials. It never handles plaintext.
type CredentialStore interface {
	Create(ctx context.Context, c domain.Credential) error
	Update(ctx context.Context, c domain.Credential) error
	Get(ctx context.Context, id domain.CredentialID) (domain.Credential, error)
	Delete(ctx context.Context, id domain.CredentialID) error
	// List omits the encrypted Data.
	List(ctx context.Context, p Page) (PageResult[domain.Credential], error)
}

// TagStore persists tags. Workflows reference tags by name.
type TagStore interface {
	// Create fails with domain.ErrAlreadyExists when the name is taken.
	Create(ctx context.Context, t domain.Tag) error
	Get(ctx context.Context, id domain.TagID) (domain.Tag, error)
	// Rename changes the name, here and on every workflow carrying it. It fails
	// with domain.ErrAlreadyExists when the new name is taken.
	Rename(ctx context.Context, id domain.TagID, name string) error
	// Delete removes the tag from the store and from every workflow.
	Delete(ctx context.Context, id domain.TagID) error
	// List orders by name.
	List(ctx context.Context, p Page) (PageResult[domain.Tag], error)
}

// VariableStore persists variables. Secret values arrive already encrypted.
type VariableStore interface {
	// Create fails with domain.ErrAlreadyExists when the ID or key is taken.
	Create(ctx context.Context, v domain.Variable) error
	// Update replaces key, secret flag, key ID and value; the key stays unique.
	Update(ctx context.Context, v domain.Variable) error
	Get(ctx context.Context, id domain.VariableID) (domain.Variable, error)
	Delete(ctx context.Context, id domain.VariableID) error
	// List orders by key.
	List(ctx context.Context, p Page) (PageResult[domain.Variable], error)
}

// VarsResolver supplies the $vars of an execution.
type VarsResolver interface {
	Vars(ctx context.Context) (map[string]any, error)
}

// TxManager runs several store and queue calls atomically. Calls made with the
// context passed to fn join the transaction; nested calls join the outer one.
// A non-nil error from fn rolls everything back.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
