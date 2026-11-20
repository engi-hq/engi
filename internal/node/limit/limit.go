// Package limit implements the Limit node, which keeps the first or last N items.
package limit

import (
	"context"
	"fmt"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.transform.limit"

// Which end of the list to keep.
const (
	keepFirst = "first"
	keepLast  = "last"
)

// Node is the Limit node.
type Node struct{}

// New returns a Limit node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Limit", Group: nodesdk.GroupTransform, Icon: "scissors",
		Description: "Keep only the first or last items.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "maxItems", DisplayName: "Max items", Type: nodesdk.PropNumber, Default: float64(1), Description: "How many items to keep."},
			{Name: "keep", DisplayName: "Keep", Type: nodesdk.PropOptions, Default: keepFirst, Options: []nodesdk.Option{
				{Name: "First items", Value: keepFirst}, {Name: "Last items", Value: keepLast}}},
		},
	}
}

// Execute implements nodesdk.Node.
func (Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	n, err := nodesdk.ParamInt(ctx, in, "maxItems", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading maxItems: %w", err)
	}
	if n < 0 {
		return nodesdk.Output{}, fmt.Errorf("maxItems must not be negative, got %d", n)
	}
	keep, err := nodesdk.ParamString(ctx, in, "keep", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading keep: %w", err)
	}
	items := in.Items(0)
	offset := 0
	switch keep {
	case keepFirst, "":
		items = items[:min(n, len(items))]
	case keepLast:
		offset = max(0, len(items)-n)
		items = items[offset:]
	default:
		return nodesdk.Output{}, fmt.Errorf("unknown keep value %q", keep)
	}
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = nodekit.Paired(it, offset+i)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}
