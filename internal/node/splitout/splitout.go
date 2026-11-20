// Package splitout implements the Split Out node, which turns an array field
// into one item per element.
package splitout

import (
	"context"
	"fmt"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.transform.splitOut"

// Which other fields stay on the produced items.
const (
	includeNone     = "none"
	includeAll      = "all"
	includeSelected = "selected"
)

// Node is the Split Out node.
type Node struct{}

// New returns a Split Out node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Split Out", Group: nodesdk.GroupTransform, Icon: "unfold-vertical",
		Description: "Turn the elements of an array field into separate items.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "fields", DisplayName: "Fields to split out", Type: nodesdk.PropString, Required: true,
				Placeholder: "e.g. items, tags", Description: "Comma-separated field paths. With several fields, their elements are paired by position. A field that is not an array counts as a one-element array."},
			{Name: "destinationField", DisplayName: "Output field", Type: nodesdk.PropString,
				Description: "Name for the elements (single field only). Object elements are spread into the item when empty."},
			{Name: "include", DisplayName: "Other fields", Type: nodesdk.PropOptions, Default: includeNone, Options: []nodesdk.Option{
				{Name: "None", Value: includeNone}, {Name: "All other fields", Value: includeAll}, {Name: "Selected fields", Value: includeSelected}}},
			{Name: "fieldsToInclude", DisplayName: "Fields to include", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("include", includeSelected),
				Placeholder: "e.g. id, name", Description: "Comma-separated field paths copied onto every produced item."},
		},
	}
}

// Execute implements nodesdk.Node.
func (Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	var out []nodesdk.Item
	for i, it := range in.Items(0) {
		produced, err := split(ctx, in, it, i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		out = append(out, produced...)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func split(ctx context.Context, in nodesdk.Input, it nodesdk.Item, i int) ([]nodesdk.Item, error) {
	fields, err := nodekit.ParamStringList(ctx, in, "fields", i)
	if err != nil {
		return nil, err //nolint:wrapcheck // already descriptive
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("no field to split out")
	}
	dest, err := nodesdk.ParamString(ctx, in, "destinationField", i)
	if err != nil {
		return nil, fmt.Errorf("reading destinationField: %w", err)
	}
	include, err := nodesdk.ParamString(ctx, in, "include", i)
	if err != nil {
		return nil, fmt.Errorf("reading include: %w", err)
	}

	lists := make([][]any, len(fields))
	longest := 0
	for n, f := range fields {
		v, ok := nodekit.GetPath(it.JSON, f)
		if !ok {
			return nil, fmt.Errorf("field %q does not exist", f)
		}
		switch t := v.(type) {
		case []any:
			lists[n] = t
		case nil:
			lists[n] = nil
		default:
			lists[n] = []any{t}
		}
		longest = max(longest, len(lists[n]))
	}

	base := map[string]any{}
	switch include {
	case includeAll:
		base = nodekit.CloneMap(it.JSON)
		for _, f := range fields {
			nodekit.DeletePath(base, f)
		}
	case includeSelected:
		keep, err := nodekit.ParamStringList(ctx, in, "fieldsToInclude", i)
		if err != nil {
			return nil, err //nolint:wrapcheck // already descriptive
		}
		for _, f := range keep {
			if v, ok := nodekit.GetPath(it.JSON, f); ok {
				nodekit.SetPath(base, f, nodekit.Clone(v), true)
			}
		}
	case includeNone, "":
	default:
		return nil, fmt.Errorf("unknown include value %q", include)
	}

	spread := len(fields) == 1 && dest == ""
	out := make([]nodesdk.Item, 0, longest)
	for e := 0; e < longest; e++ {
		res := nodekit.Paired(nodesdk.Item{JSON: nodekit.CloneMap(base)}, i)
		for n, f := range fields {
			if e >= len(lists[n]) {
				continue
			}
			el := nodekit.Clone(lists[n][e])
			name := f
			if len(fields) == 1 && dest != "" {
				name = dest
			}
			if obj, ok := el.(map[string]any); ok && spread {
				for k, v := range obj {
					res.JSON[k] = v
				}
				continue
			}
			nodekit.SetPath(res.JSON, lastSegment(name, len(fields) == 1 && dest != ""), el, false)
		}
		out = append(out, res)
	}
	return out, nil
}

// lastSegment returns the name an element is stored under. A destination is
// used verbatim; a source path stores under its final key (a.b.c -> c).
func lastSegment(name string, isDestination bool) string {
	if isDestination {
		return name
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}
