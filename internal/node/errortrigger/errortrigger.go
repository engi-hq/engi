// Package errortrigger implements the Error Trigger node, the start of an
// error workflow. Engi starts it when an execution of a workflow that names
// this workflow as its error workflow ends in error or crashed; the node
// forwards the failure description it was started with.
package errortrigger

import (
	"context"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.trigger.error"

// Node is the Error Trigger.
type Node struct{}

// New returns an Error Trigger node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Error Trigger", Group: nodesdk.GroupTrigger, Icon: "triangle-alert",
		Description: "Starts the workflow when an execution of another workflow fails and this workflow is its error workflow.",
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
	}
}

// Execute implements nodesdk.Node. It forwards the failure item it was
// started with.
func (Node) Execute(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}
