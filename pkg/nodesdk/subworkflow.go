package nodesdk

import "context"

// SubworkflowRunner starts executions of other workflows on behalf of a node.
// The host provides it; a node receives it at construction.
type SubworkflowRunner interface {
	// RunSubworkflow starts the call's workflow. With WaitForResult it returns
	// once the child execution has finished; otherwise it returns as soon as
	// the child is accepted.
	RunSubworkflow(ctx context.Context, call SubworkflowCall) (SubworkflowResult, error)
}

// SubworkflowCall describes one child execution.
type SubworkflowCall struct {
	// WorkflowID identifies the workflow to run.
	WorkflowID string
	// ParentExecutionID is the calling execution.
	ParentExecutionID string
	// Depth is the nesting level of the child (the caller's depth plus one).
	Depth int
	// Items feed the child's Execute Workflow Trigger.
	Items []Item
	// WaitForResult makes the call block until the child has finished.
	WaitForResult bool
}

// SubworkflowResult is what the caller learns about the child.
type SubworkflowResult struct {
	ExecutionID string
	// Status is the child's status; "queued" for a fire-and-forget call.
	Status string
	// Items are the output of the child's last node (only when waiting).
	Items []Item
	// Error describes a failed child (only when waiting).
	Error string
}
