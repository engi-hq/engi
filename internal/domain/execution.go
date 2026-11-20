package domain

import "time"

// ExecutionMode says what started an execution.
type ExecutionMode string

// Execution modes.
const (
	ModeWebhook     ExecutionMode = "webhook"
	ModeTrigger     ExecutionMode = "trigger"
	ModeManual      ExecutionMode = "manual"
	ModeRetry       ExecutionMode = "retry"
	ModeSubworkflow ExecutionMode = "subworkflow"
	ModeError       ExecutionMode = "error"
)

// Valid reports whether m is a known mode.
func (m ExecutionMode) Valid() bool {
	switch m {
	case ModeWebhook, ModeTrigger, ModeManual, ModeRetry, ModeSubworkflow, ModeError:
		return true
	}
	return false
}

// ExecutionStatus is the lifecycle state of an execution.
type ExecutionStatus string

// Execution statuses.
const (
	StatusQueued   ExecutionStatus = "queued"
	StatusRunning  ExecutionStatus = "running"
	StatusWaiting  ExecutionStatus = "waiting"
	StatusSuccess  ExecutionStatus = "success"
	StatusError    ExecutionStatus = "error"
	StatusCanceled ExecutionStatus = "canceled"
	StatusCrashed  ExecutionStatus = "crashed"
)

// Terminal reports whether no further progress is possible.
func (s ExecutionStatus) Terminal() bool {
	switch s {
	case StatusSuccess, StatusError, StatusCanceled, StatusCrashed:
		return true
	}
	return false
}

// RunHolder identifies the queue job that holds a running execution: the job
// and the attempt of that job (its claim count). A later claim of the same job
// has a higher attempt, which fences off the claim it replaced. The zero value
// is an inline run with no job.
type RunHolder struct {
	JobID   JobID
	Attempt int
}

// Execution is one run of a workflow version.
type Execution struct {
	ID              ExecutionID
	WorkflowID      WorkflowID
	WorkflowVersion int
	Mode            ExecutionMode
	Status          ExecutionStatus
	StartedAt       time.Time
	FinishedAt      *time.Time
	WaitUntil       *time.Time
	// DedupKey, when set, is unique per workflow: a second execution with the
	// same key is rejected with ErrDuplicate. Empty means no deduplication.
	DedupKey          string
	ParentExecutionID *ExecutionID
	// Attempts counts how many times a worker started the run: a claim that
	// won the queued-to-running transition. Retries after a lost worker, after
	// a failed infrastructure attempt and after a shutdown release all count.
	Attempts int
	// LastRecoveredAt is when a reclaim last took the run back from a worker
	// that died. Nil when no worker has died under this execution.
	LastRecoveredAt *time.Time
	RunData         map[string][]NodeRun
	Error           *ErrorInfo
}

// NodeRunStatus is the outcome of one node run.
type NodeRunStatus string

// Node run statuses.
const (
	NodeRunSuccess  NodeRunStatus = "success"
	NodeRunError    NodeRunStatus = "error"
	NodeRunDisabled NodeRunStatus = "disabled"
	NodeRunPinned   NodeRunStatus = "pinned"
	NodeRunWaiting  NodeRunStatus = "waiting"
)

// PortData holds items per port kind, then per port index: PortData["main"][1]
// are the items of the second main port.
type PortData map[PortKind][][]Item

// Source says where one input of a node run came from.
type Source struct {
	Node string
	// OutputIndex is the output port of Node that produced the data.
	OutputIndex int
	// RunIndex is the run of Node that produced the data.
	RunIndex int
}

// NodeRun records one run of a node.
type NodeRun struct {
	RunIndex  int
	StartedAt time.Time
	Duration  time.Duration
	Status    NodeRunStatus
	// Attempts counts executions including retries (0 for disabled/pinned).
	Attempts int
	// Sources has one entry per input port; a zero Node means no source.
	Sources []Source
	Input   PortData
	Output  PortData
	Error   *ErrorInfo
	// Response is the webhook answer this run produced (Respond to Webhook).
	// It travels on the event bus to the waiting HTTP call and is not stored.
	Response *WebhookResponse
}

// WebhookResponse is an HTTP reply for the webhook call that started an execution.
type WebhookResponse struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// ExecutionInput is what an execution was started with: the node to begin at
// and the items it receives. It is kept until the execution first runs.
type ExecutionInput struct {
	StartNode string
	Items     []Item
	// Destination, when set, makes the run partial: only the nodes needed to
	// compute it run. The destination itself runs unless DestinationExclusive.
	Destination          string
	DestinationExclusive bool
	// PinData replaces the output of the named nodes for this run only.
	PinData map[string][]Item
	// Workflow, when set, is the unsaved document to run instead of the stored
	// version the execution points at.
	Workflow *Workflow
	// TraceParent is the W3C trace context of the request that started the
	// execution, so its trace continues across the queue. Empty when untraced.
	TraceParent string
}

// LastOutput returns the items on the first output port of the node run that
// finished last, which is what a caller waiting for "the last node" wants.
// Runs are ordered by their end time; ties go to the higher run index, then
// the node name. Disabled and failed runs are skipped.
func (e Execution) LastOutput() []Item {
	var best *NodeRun
	var bestNode string
	var bestEnd time.Time
	for node, runs := range e.RunData {
		for i := range runs {
			r := &runs[i]
			if r.Status == NodeRunError || r.Status == NodeRunDisabled {
				continue
			}
			end := r.StartedAt.Add(r.Duration)
			if best == nil || end.After(bestEnd) ||
				(end.Equal(bestEnd) && (r.RunIndex > best.RunIndex || (r.RunIndex == best.RunIndex && node > bestNode))) {
				best, bestNode, bestEnd = r, node, end
			}
		}
	}
	if best == nil || len(best.Output[PortMain]) == 0 {
		return nil
	}
	return best.Output[PortMain][0]
}
