// Package executeworkflowtrigger implements the Execute Workflow Trigger node,
// the start of a workflow that other workflows call. It emits the items the
// calling Execute Workflow node passed in.
package executeworkflowtrigger

import (
	"context"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.trigger.executeWorkflow"

// Node is the Execute Workflow Trigger.
type Node struct{}

// New returns an Execute Workflow Trigger node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Execute Workflow Trigger", Group: nodesdk.GroupTrigger, Icon: "workflow",
		Description: "Starts the workflow when another workflow calls it.",
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
	}
}

// Execute implements nodesdk.Node. It forwards the items it was started with.
func (Node) Execute(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}
