// Package ifnode implements the IF node, which routes items to a true or a
// false output by typed conditions.
package ifnode

import (
	"context"
	"fmt"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.flow.if"

// Output ports.
const (
	portTrue  = 0
	portFalse = 1
)

// Node is the IF node.
type Node struct{}

// New returns an IF node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "IF", Group: nodesdk.GroupFlow, Icon: "split",
		Description: "Route items to the true or false output depending on conditions.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("true", "True"), nodekit.Main("false", "False")},
		Properties:  nodekit.ConditionProperties(nil),
	}
}

// Execute implements nodesdk.Node. Items with no conditions configured go to
// the true output.
func (Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	ports := make([][]nodesdk.Item, 2)
	for i, it := range in.Items(0) {
		g, opts, err := nodekit.ReadGroup(ctx, in, i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		ok, err := g.Eval(opts)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, fmt.Errorf("evaluating conditions: %w", err))
		}
		target := portFalse
		if ok {
			target = portTrue
		}
		ports[target] = append(ports[target], nodekit.Paired(it, i))
	}
	return nodesdk.Output{Ports: ports}, nil
}
