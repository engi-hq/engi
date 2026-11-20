// Package manualtrigger implements the Manual Trigger node, the start of a
// workflow run by hand (CLI or editor). It emits the items it was started with.
package manualtrigger

import (
	"context"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.trigger.manual"

// Node is the Manual Trigger.
type Node struct{}

// New returns a Manual Trigger node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Manual Trigger", Group: nodesdk.GroupTrigger, Icon: "play",
		Description: "Starts the workflow when you run it by hand.",
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
	}
}

// Execute implements nodesdk.Node. It forwards the start items; a run started
// without input yields one empty item (the engine supplies it).
func (Node) Execute(_ context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}
