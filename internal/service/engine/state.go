package engine

import (
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

// State is the complete, serializable progress of an execution. It holds no
// closures or live connections, so it can be stored while waiting and handed
// back to Run through Request.Resume.
type State struct {
	ExecutionID domain.ExecutionID
	Status      domain.ExecutionStatus
	StartedAt   time.Time
	FinishedAt  time.Time
	// Queue is the ready stack; the last element runs next.
	Queue []Task
	// Waiting holds partially filled inputs of multi-input nodes, by node name.
	Waiting map[string]WaitingInputs
	// RunData has every run of every node, by node name.
	RunData map[string][]domain.NodeRun
	// Order lists node runs in the order they finished.
	Order []RunRef
	// ResumeSecret is the random part of every resume token of this execution.
	// It is drawn when the execution starts, so one resume URL serves each Wait
	// node the execution reaches. Old states without it get one on resume.
	ResumeSecret string
	// Pending is set while Status is waiting.
	Pending *PendingWait
	// Error is set when Status is error or canceled.
	Error *domain.ErrorInfo
}

// RunRef identifies one run of one node.
type RunRef struct {
	Node     string
	RunIndex int
}

// Task is a node ready to run, with the items of each of its inputs.
type Task struct {
	Node    string
	Inputs  [][]domain.Item
	Sources []domain.Source
}

// WaitingInputs collects the inputs that have arrived for a multi-input node.
type WaitingInputs struct {
	Inputs  [][]domain.Item
	Sources []domain.Source
	Filled  []bool
}

// PendingWait describes the node an execution is paused on.
type PendingWait struct {
	Node        string
	RunIndex    int
	Until       time.Time
	ResumeToken string
	// Output is delivered to the node's children when the execution resumes.
	Output [][]domain.Item
	// Resumed is set by whoever resumes the execution with data (a resume
	// webhook); ResumeItems then replace the first output port, like
	// Request.ResumeItems does. It lets the data travel in the saved state.
	Resumed     bool
	ResumeItems []domain.Item
}

// Result summarizes a finished (or paused) execution for observers.
type Result struct {
	ExecutionID domain.ExecutionID
	Status      domain.ExecutionStatus
	Error       *domain.ErrorInfo
	StartedAt   time.Time
	FinishedAt  time.Time
	Pending     *PendingWait
}

// ExecutionInfo is passed to Observer.ExecutionStarted.
type ExecutionInfo struct {
	ExecutionID domain.ExecutionID
	WorkflowID  domain.WorkflowID
	Mode        domain.ExecutionMode
	StartedAt   time.Time
	// Resumed is true when the run continues a waiting execution.
	Resumed bool
}

// StateCodec turns the payloads kept in the execution state store into values
// and back. The store holds one blob per execution: the input of an execution
// that has not run yet, or the State of a waiting one.
type StateCodec interface {
	EncodeState(s *State) ([]byte, error)
	DecodeState(data []byte) (*State, error)
	EncodeInput(in domain.ExecutionInput) ([]byte, error)
	// DecodeSaved reads a blob written by EncodeInput or EncodeState. Exactly
	// one of the results is non-nil.
	DecodeSaved(data []byte) (*domain.ExecutionInput, *State, error)
}
