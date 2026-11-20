package domain

import (
	"errors"
	"fmt"
	"time"
)

// Storage errors. Stores return them wrapped; check with errors.Is.
var (
	// ErrDuplicate is returned when a unique deduplication key already exists.
	// The concrete error is a *DuplicateError.
	ErrDuplicate = errors.New("duplicate")
	// ErrAlreadyExists is returned when creating an entity whose ID is taken.
	ErrAlreadyExists = errors.New("already exists")
	// ErrLeaseLost is returned when a worker acts on a job it no longer owns.
	ErrLeaseLost = errors.New("job lease lost")
	// ErrInvalidCursor is returned when a list cursor is malformed or was not
	// issued by the store.
	ErrInvalidCursor = errors.New("invalid cursor")
	// ErrNoJob is returned when no job is available to claim.
	ErrNoJob = errors.New("no job available")
)

// DuplicateError describes a rejected duplicate execution.
type DuplicateError struct {
	WorkflowID WorkflowID
	DedupKey   string
	// ExistingID is the execution that already holds the key, when known.
	ExistingID ExecutionID
}

// Error implements error.
func (e *DuplicateError) Error() string {
	return fmt.Sprintf("duplicate execution for workflow %s with dedup key %q", e.WorkflowID, e.DedupKey)
}

// Is makes errors.Is(err, ErrDuplicate) true.
func (e *DuplicateError) Is(target error) bool { return target == ErrDuplicate }

// WorkflowVersion is an immutable snapshot of a workflow, created by every save.
type WorkflowVersion struct {
	WorkflowID WorkflowID
	Version    int
	CreatedAt  time.Time
	Workflow   Workflow
}

// Credential is a stored credential. Data is ciphertext produced elsewhere;
// the store never sees plaintext.
type Credential struct {
	ID        CredentialID
	Name      string
	Type      string
	KeyID     string
	Data      []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PrefixJob is the identifier prefix of queue jobs.
const PrefixJob = "jb"

// JobID identifies a queue job.
type JobID string

// JobStatus is the lifecycle state of a job.
type JobStatus string

// Job statuses. Completed jobs are removed from the queue.
const (
	JobQueued JobStatus = "queued"
	JobLeased JobStatus = "leased"
	// JobFailed parks a job that exhausted its attempts.
	JobFailed JobStatus = "failed"
)

// Job asks a worker to run one execution. Jobs carry IDs, never payloads.
type Job struct {
	ID          JobID
	ExecutionID ExecutionID
	Status      JobStatus
	// Attempts counts claims, including the current one.
	Attempts int
	// AvailableAt is the earliest time the job may be claimed.
	AvailableAt time.Time
	LeaseOwner  string
	LeaseUntil  time.Time
	LastError   string
	CreatedAt   time.Time
}

// ReclaimedJob is a job whose lease expired and was taken back by the queue.
// The embedded Job is in its new state: queued again, or failed when its
// attempts ran out.
type ReclaimedJob struct {
	Job
	// PreviousOwner is the worker that held the expired lease.
	PreviousOwner string
}
