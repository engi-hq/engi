// Package aggregate implements the Aggregate node, which folds many items into
// one: selected fields collected into arrays, or every item's data in one list.
package aggregate

import (
	"context"
	"fmt"
	"slices"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.transform.aggregate"

// Modes and field selections.
const (
	modeIndividual = "individualFields"
	modeAll        = "allItemData"
	includeAll     = "all"
	includeOnly    = "selected"
	includeExcept  = "except"
)

// Node is the Aggregate node.
type Node struct{}

// New returns an Aggregate node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	individual := nodekit.Show("mode", modeIndividual)
	all := nodekit.Show("mode", modeAll)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Aggregate", Group: nodesdk.GroupTransform, Icon: "layers",
		Description: "Combine many items into one.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "mode", DisplayName: "Aggregate", Type: nodesdk.PropOptions, Default: modeIndividual, Options: []nodesdk.Option{
				{Name: "Individual fields", Value: modeIndividual}, {Name: "All item data (into one list)", Value: modeAll}}},
			{Name: "fields", DisplayName: "Fields to aggregate", Type: nodesdk.PropFixedCollection, DisplayOptions: individual,
				Children: []nodesdk.Property{
					{Name: "field", DisplayName: "Input field", Type: nodesdk.PropString, Required: true},
					{Name: "outputName", DisplayName: "Output name", Type: nodesdk.PropString, Description: "Defaults to the input field name."},
				}},
			{Name: "mergeLists", DisplayName: "Merge array values", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: individual,
				Description: "When the field holds arrays, concatenate them instead of nesting."},
			{Name: "keepMissing", DisplayName: "Keep missing values", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: individual,
				Description: "Add null for items without the field instead of skipping them."},
			{Name: "destinationField", DisplayName: "Output field", Type: nodesdk.PropString, Default: "data", DisplayOptions: all},
			{Name: "include", DisplayName: "Include", Type: nodesdk.PropOptions, Default: includeAll, DisplayOptions: all, Options: []nodesdk.Option{
				{Name: "All fields", Value: includeAll}, {Name: "Selected fields", Value: includeOnly}, {Name: "All fields except", Value: includeExcept}}},
			{Name: "fieldList", DisplayName: "Fields", Type: nodesdk.PropString, DisplayOptions: nodekit.Show("include", includeOnly, includeExcept),
				Placeholder: "e.g. id, name", Description: "Comma-separated field paths."},
		},
	}
}

// Execute implements nodesdk.Node. It always produces exactly one item, paired
// with every input item.
func (Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	mode, err := nodesdk.ParamString(ctx, in, "mode", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading mode: %w", err)
	}
	items := in.Items(0)
	var res map[string]any
	switch mode {
	case modeIndividual, "":
		res, err = individualFields(ctx, in, items)
	case modeAll:
		res, err = allData(ctx, in, items)
	default:
		err = fmt.Errorf("unknown mode %q", mode)
	}
	if err != nil {
		return nodesdk.Output{}, err
	}
	out := nodesdk.Item{JSON: res}
	for i := range items {
		out.PairedItem = append(out.PairedItem, nodesdk.PairedItem{Input: 0, Item: i})
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{{out}}}, nil
}

func individualFields(ctx context.Context, in nodesdk.Input, items []nodesdk.Item) (map[string]any, error) {
	raw, err := nodekit.ParamSlice(ctx, in, "fields", 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // already descriptive
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("no field to aggregate")
	}
	merge, err := nodesdk.ParamBool(ctx, in, "mergeLists", 0)
	if err != nil {
		return nil, fmt.Errorf("reading mergeLists: %w", err)
	}
	keepMissing, err := nodesdk.ParamBool(ctx, in, "keepMissing", 0)
	if err != nil {
		return nil, fmt.Errorf("reading keepMissing: %w", err)
	}
	res := map[string]any{}
	for n, r := range raw {
		spec, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("fields[%d] must be an object, got %s", n, nodekit.TypeName(r))
		}
		field, _ := spec["field"].(string)
		if field == "" {
			return nil, fmt.Errorf("fields[%d]: field is required", n)
		}
		name, _ := spec["outputName"].(string)
		if name == "" {
			name = field
		}
		values := []any{}
		for _, it := range items {
			v, found := nodekit.GetPath(it.JSON, field)
			if !found {
				if keepMissing {
					values = append(values, nil)
				}
				continue
			}
			if arr, isArr := v.([]any); isArr && merge {
				for _, e := range arr {
					values = append(values, nodekit.Clone(e))
				}
				continue
			}
			values = append(values, nodekit.Clone(v))
		}
		nodekit.SetPath(res, name, values, false)
	}
	return res, nil
}

func allData(ctx context.Context, in nodesdk.Input, items []nodesdk.Item) (map[string]any, error) {
	dest, err := nodesdk.ParamString(ctx, in, "destinationField", 0)
	if err != nil {
		return nil, fmt.Errorf("reading destinationField: %w", err)
	}
	if dest == "" {
		return nil, fmt.Errorf("destinationField must not be empty")
	}
	include, err := nodesdk.ParamString(ctx, in, "include", 0)
	if err != nil {
		return nil, fmt.Errorf("reading include: %w", err)
	}
	var fields []string
	if include == includeOnly || include == includeExcept {
		if fields, err = nodekit.ParamStringList(ctx, in, "fieldList", 0); err != nil {
			return nil, err //nolint:wrapcheck // already descriptive
		}
	}
	list := make([]any, 0, len(items))
	for _, it := range items {
		var obj map[string]any
		switch include {
		case includeAll, "":
			obj = nodekit.CloneMap(it.JSON)
		case includeOnly:
			obj = map[string]any{}
			for _, f := range fields {
				if v, ok := nodekit.GetPath(it.JSON, f); ok {
					nodekit.SetPath(obj, f, nodekit.Clone(v), true)
				}
			}
		case includeExcept:
			obj = nodekit.CloneMap(it.JSON)
			for _, f := range slices.Clone(fields) {
				nodekit.DeletePath(obj, f)
			}
		default:
			return nil, fmt.Errorf("unknown include value %q", include)
		}
		if obj == nil {
			obj = map[string]any{}
		}
		list = append(list, obj)
	}
	return map[string]any{dest: list}, nil
}
