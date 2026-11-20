package port

import (
	"context"

	"github.com/engi-hq/engi/internal/domain"
)

// ExpressionEvaluator resolves node parameter values.
type ExpressionEvaluator interface {
	// Resolve evaluates value. Strings starting with "=" are expression
	// templates; maps and slices are resolved recursively; every other value
	// is returned unchanged. Failures are *domain.ExpressionError.
	Resolve(ctx context.Context, value any, scope ExpressionScope) (any, error)
}

// ExpressionScope is the data visible to an expression.
type ExpressionScope struct {
	// Item is the current input item ($json, $binary).
	Item domain.Item
	// Input are all items of the current node's first input ($input).
	Input     []domain.Item
	ItemIndex int
	RunIndex  int
	// Nodes resolves $("Node") and $node. May be nil.
	Nodes     NodeData
	Vars      map[string]any
	Execution ExecutionMeta
	Workflow  WorkflowMeta
}

// ExecutionMeta backs $execution.
type ExecutionMeta struct {
	ID   domain.ExecutionID
	Mode domain.ExecutionMode
	// ResumeURL is the absolute URL that resumes the execution's waiting Wait
	// node ($execution.resumeUrl). Empty when no resume link is available.
	ResumeURL string
}

// WorkflowMeta backs $workflow.
type WorkflowMeta struct {
	ID   domain.WorkflowID
	Name string
}

// NodeData gives expressions access to other nodes' results.
type NodeData interface {
	// PairedItem returns the item of the named node's output that is paired
	// with the current item. It returns an error wrapping domain.ErrNotFound
	// when the node has not run or no pairing exists.
	PairedItem(node string) (domain.Item, error)
	// Output returns the items on the first output port of the named node's
	// latest run, or an error wrapping domain.ErrNotFound.
	Output(node string) ([]domain.Item, error)
}
