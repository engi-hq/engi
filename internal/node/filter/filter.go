// Package filter implements the Filter node, which keeps only the items that
// satisfy typed conditions.
package filter

import (
	"context"
	"fmt"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.transform.filter"

// Node is the Filter node.
type Node struct{}

// New returns a Filter node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	props := nodekit.ConditionProperties(nil)
	props = append(props, nodesdk.Property{
		Name: "keepDiscarded", DisplayName: "Send discarded items to the second output", Type: nodesdk.PropBoolean, Default: false,
	})
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Filter", Group: nodesdk.GroupTransform, Icon: "filter",
		Description: "Keep only the items that match the conditions.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("kept", "Kept"), nodekit.Main("discarded", "Discarded")},
		Properties:  props,
	}
}

// Execute implements nodesdk.Node.
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
		if ok {
			ports[0] = append(ports[0], nodekit.Paired(it, i))
			continue
		}
		keep, err := nodesdk.ParamBool(ctx, in, "keepDiscarded", i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		if keep {
			ports[1] = append(ports[1], nodekit.Paired(it, i))
		}
	}
	return nodesdk.Output{Ports: ports}, nil
}
