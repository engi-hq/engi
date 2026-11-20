package nodesdk

import "context"

// Supplier is implemented by sub-nodes: nodes whose descriptor has a sub-port
// output (an "ai.*" kind) and no main ports. A sub-node never runs as a step.
// Instead the parent that it is connected to pulls its value through
// Input.SubPort, and the engine calls Supply at that moment.
//
// The Input passed to Supply belongs to the sub-node: Param, Credential, HTTP,
// Blob, Logger and ExecutionInfo (NodeName is the sub-node's) work as in
// Execute. Parameters and expressions resolve against the parent's current
// item, so $json is the parent's item. Items returns the parent's main input
// items. The sub-node may itself have sub-port inputs and call SubPort.
//
// The value's type depends on the port kind; the parent type-asserts it, for
// example to port.ModelProvider for PortAIModel or port.ToolProvider for
// PortAITool. Supply must be cheap or cache its own expensive resources, and
// must not keep per-execution state in the node.
type Supplier interface {
	Supply(ctx context.Context, in Input) (any, error)
}

// Supplied is one value pulled from a sub-node.
type Supplied struct {
	// Node is the name of the sub-node that produced Value.
	Node string
	// Value is whatever the sub-node's Supply returned.
	Value any
}
