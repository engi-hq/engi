package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned (wrapped) when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// Issue is one validation problem at a path, for example "nodes[2].name".
type Issue struct {
	Path    string
	Message string
}

// ValidationError aggregates every problem found while validating an entity.
type ValidationError struct {
	Issues []Issue
}

// Error implements error.
func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Issues))
	for i, is := range e.Issues {
		parts[i] = is.Path + ": " + is.Message
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// ErrorKind classifies an execution or item error.
type ErrorKind string

// Error kinds.
const (
	ErrorKindNode     ErrorKind = "node"
	ErrorKindWorkflow ErrorKind = "workflow"
	ErrorKindTimeout  ErrorKind = "timeout"
	ErrorKindCanceled ErrorKind = "canceled"
)

// ErrorInfo is the serializable description of a failure. It is stored in
// executions, node runs and failed items.
type ErrorInfo struct {
	Kind        ErrorKind
	Node        string
	Message     string
	Description string
	HTTPStatus  int
	// ItemIndex is the failing input item, when known.
	ItemIndex *int
}

// NodeError is a failure of one node.
type NodeError struct {
	Node        string
	Message     string
	Description string
	HTTPStatus  int
	ItemIndex   *int
	Err         error
}

// Error implements error.
func (e *NodeError) Error() string {
	if e.Err != nil && e.Message == "" {
		return fmt.Sprintf("node %q: %v", e.Node, e.Err)
	}
	return fmt.Sprintf("node %q: %s", e.Node, e.Message)
}

// Unwrap returns the underlying cause.
func (e *NodeError) Unwrap() error { return e.Err }

// Info returns the serializable form.
func (e *NodeError) Info() ErrorInfo {
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return ErrorInfo{
		Kind: ErrorKindNode, Node: e.Node, Message: msg,
		Description: e.Description, HTTPStatus: e.HTTPStatus, ItemIndex: e.ItemIndex,
	}
}

// WorkflowError is a failure of the workflow as a whole: a bad graph, a
// missing node, a loop guard, and so on. Node is empty when not node-specific.
type WorkflowError struct {
	Node    string
	Message string
	Err     error
}

// Error implements error.
func (e *WorkflowError) Error() string {
	var b strings.Builder
	b.WriteString("workflow")
	if e.Node != "" {
		fmt.Fprintf(&b, " (node %q)", e.Node)
	}
	b.WriteString(": ")
	b.WriteString(e.Message)
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	return b.String()
}

// Unwrap returns the underlying cause.
func (e *WorkflowError) Unwrap() error { return e.Err }

// Info returns the serializable form.
func (e *WorkflowError) Info() ErrorInfo {
	msg := e.Message
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return ErrorInfo{Kind: ErrorKindWorkflow, Node: e.Node, Message: msg}
}

// ExpressionError is a failed expression evaluation.
type ExpressionError struct {
	Node      string
	Parameter string
	ItemIndex int
	// Expression is the template that failed.
	Expression string
	Message    string
	// Stack is an excerpt of the JavaScript stack, when available.
	Stack string
	Err   error
}

// Error implements error.
func (e *ExpressionError) Error() string {
	var b strings.Builder
	b.WriteString("expression")
	if e.Node != "" {
		fmt.Fprintf(&b, " in node %q", e.Node)
	}
	if e.Parameter != "" {
		fmt.Fprintf(&b, " parameter %q", e.Parameter)
	}
	fmt.Fprintf(&b, " (item %d): %s", e.ItemIndex, e.Message)
	return b.String()
}

// Unwrap returns the underlying cause.
func (e *ExpressionError) Unwrap() error { return e.Err }
