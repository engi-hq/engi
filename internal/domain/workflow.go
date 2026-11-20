package domain

import (
	"sort"
	"time"
)

// PortKind is the kind of a connection endpoint. "main" carries data between
// steps; "ai.*" kinds are sub-ports pulled by a parent node.
type PortKind string

// PortMain is the data-flow port kind.
const PortMain PortKind = "main"

// Workflow is a graph of nodes and the connections between them.
type Workflow struct {
	ID          WorkflowID
	Name        string
	Active      bool
	Version     int
	Settings    WorkflowSettings
	Nodes       []Node
	Connections []Connection
	PinData     map[string][]Item
	Tags        []string
	// Notes are sticky notes on the canvas; the engine ignores them.
	Notes []Note
}

// WorkflowSettings are workflow-wide options.
type WorkflowSettings struct {
	// Timeout overrides the configured execution timeout when positive.
	Timeout         time.Duration
	ErrorWorkflowID WorkflowID
	SaveSuccessData bool
}

// Position is a node's place on the canvas.
type Position struct {
	X, Y float64
}

// Node is one step of a workflow.
type Node struct {
	ID          NodeID
	Name        string
	Type        string
	TypeVersion int
	Position    Position
	Parameters  map[string]any
	Credentials map[string]CredentialID
	Disabled    bool
	Settings    NodeSettings
	Notes       string
}

// OnError selects what happens when a node fails.
type OnError string

// OnError values. The zero value behaves like OnErrorStop.
const (
	OnErrorStop        OnError = "stop"
	OnErrorContinue    OnError = "continue"
	OnErrorErrorOutput OnError = "errorOutput"
)

// Valid reports whether o is a known value (the empty value counts as stop).
func (o OnError) Valid() bool {
	switch o {
	case "", OnErrorStop, OnErrorContinue, OnErrorErrorOutput:
		return true
	}
	return false
}

// Backoff is the retry delay growth strategy.
type Backoff string

// Backoff strategies. The zero value behaves like BackoffFixed.
const (
	BackoffFixed       Backoff = "fixed"
	BackoffExponential Backoff = "exponential"
)

// Valid reports whether b is a known value (the empty value counts as fixed).
func (b Backoff) Valid() bool {
	switch b {
	case "", BackoffFixed, BackoffExponential:
		return true
	}
	return false
}

// RetryPolicy configures re-running a failed node.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts including the first.
	// Zero and one both mean "do not retry".
	MaxAttempts  int
	Backoff      Backoff
	InitialDelay time.Duration
}

// NodeSettings are per-node execution options.
type NodeSettings struct {
	Retry            RetryPolicy
	OnError          OnError
	AlwaysOutputData bool
	ExecuteOnce      bool
	// Timeout overrides the configured node timeout when positive.
	Timeout time.Duration
}

// Endpoint addresses one port of one node.
type Endpoint struct {
	Node string
	Port PortKind
	// Index selects among several ports of the same kind.
	Index int
}

// Connection links an output endpoint to an input endpoint.
type Connection struct {
	From Endpoint
	To   Endpoint
}

// NodeByName returns the node with the given name.
func (w *Workflow) NodeByName(name string) (*Node, bool) {
	for i := range w.Nodes {
		if w.Nodes[i].Name == name {
			return &w.Nodes[i], true
		}
	}
	return nil, false
}

// CanvasLess orders nodes top to bottom, then left to right, then by name.
// It is the deterministic order used to run sibling branches.
func CanvasLess(a, b *Node) bool {
	if a.Position.Y != b.Position.Y {
		return a.Position.Y < b.Position.Y
	}
	if a.Position.X != b.Position.X {
		return a.Position.X < b.Position.X
	}
	return a.Name < b.Name
}

// NodesInCanvasOrder returns the node names sorted by CanvasLess.
func (w *Workflow) NodesInCanvasOrder() []string {
	nodes := make([]*Node, len(w.Nodes))
	for i := range w.Nodes {
		nodes[i] = &w.Nodes[i]
	}
	sort.SliceStable(nodes, func(i, j int) bool { return CanvasLess(nodes[i], nodes[j]) })
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}
