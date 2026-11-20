// Package set implements the Set (Edit Fields) node: it adds or overwrites
// fields on each item, from a field list or from a JSON object.
package set

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.transform.set"

// Modes of the node.
const (
	modeManual = "manual"
	modeJSON   = "json"
)

// Node is the Set node.
type Node struct{}

// New returns a Set node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Edit Fields (Set)", Group: nodesdk.GroupTransform, Icon: "pen",
		Description: "Add, change or remove fields of each item.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "mode", DisplayName: "Mode", Type: nodesdk.PropOptions, Default: modeManual, Options: []nodesdk.Option{
				{Name: "Manual mapping", Value: modeManual}, {Name: "JSON", Value: modeJSON}}},
			{Name: "fields", DisplayName: "Fields to set", Type: nodesdk.PropFixedCollection, DisplayOptions: nodekit.Show("mode", modeManual),
				Description: "Each field has a name (a dot path such as address.city when dot notation is on), a type and a value.",
				Children: []nodesdk.Property{
					{Name: "name", DisplayName: "Name", Type: nodesdk.PropString, Required: true},
					{Name: "type", DisplayName: "Type", Type: nodesdk.PropOptions, Default: nodekit.TypeAuto, Options: nodekit.TypeOptions()},
					{Name: "value", DisplayName: "Value", Type: nodesdk.PropString},
				}},
			{Name: "jsonOutput", DisplayName: "JSON", Type: nodesdk.PropJSON, Default: "{}", DisplayOptions: nodekit.Show("mode", modeJSON),
				Description: "An object whose top-level keys are set on each item."},
			{Name: "keepOtherFields", DisplayName: "Keep other fields", Type: nodesdk.PropBoolean, Default: true,
				Description: "Keep the fields the item already has. When off, the item holds only the fields set here."},
			{Name: "dotNotation", DisplayName: "Dot notation", Type: nodesdk.PropBoolean, Default: true,
				Description: "Treat a.b as a nested path. When off, the whole name is one key."},
		},
	}
}

// Execute implements nodesdk.Node.
func (Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	items := in.Items(0)
	out := make([]nodesdk.Item, 0, len(items))
	for i, it := range items {
		res, err := apply(ctx, in, it, i)
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		out = append(out, res)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func apply(ctx context.Context, in nodesdk.Input, it nodesdk.Item, i int) (nodesdk.Item, error) {
	mode, err := nodesdk.ParamString(ctx, in, "mode", i)
	if err != nil {
		return it, fmt.Errorf("reading mode: %w", err)
	}
	keep, err := nodesdk.ParamBool(ctx, in, "keepOtherFields", i)
	if err != nil {
		return it, fmt.Errorf("reading keepOtherFields: %w", err)
	}
	dot, err := nodesdk.ParamBool(ctx, in, "dotNotation", i)
	if err != nil {
		return it, fmt.Errorf("reading dotNotation: %w", err)
	}

	res := nodekit.ClonePaired(it, i)
	if !keep {
		res.JSON = map[string]any{}
		res.Binary = nil
	}
	if res.JSON == nil {
		res.JSON = map[string]any{}
	}

	switch mode {
	case modeJSON:
		obj, err := jsonObject(ctx, in, i)
		if err != nil {
			return res, err
		}
		for k, v := range obj {
			nodekit.SetPath(res.JSON, k, v, dot)
		}
	case modeManual, "":
		raw, err := in.Param(ctx, "fields", i)
		if err != nil {
			return res, fmt.Errorf("reading fields: %w", err)
		}
		fields, ok := raw.([]any)
		if raw != nil && !ok {
			return res, fmt.Errorf("fields must be an array, got %s", nodekit.TypeName(raw))
		}
		for n, f := range fields {
			if err := setField(res.JSON, f, dot); err != nil {
				return res, fmt.Errorf("field %d: %w", n+1, err)
			}
		}
	default:
		return res, fmt.Errorf("unknown mode %q", mode)
	}
	return res, nil
}

func setField(target map[string]any, raw any, dot bool) error {
	f, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("must be an object with name, type and value, got %s", nodekit.TypeName(raw))
	}
	name, _ := f["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	typ, _ := f["type"].(string)
	v, err := nodekit.Coerce(f["value"], typ)
	if err != nil {
		return fmt.Errorf("%q: %w", name, err)
	}
	nodekit.SetPath(target, name, nodekit.Clone(v), dot)
	return nil
}

func jsonObject(ctx context.Context, in nodesdk.Input, i int) (map[string]any, error) {
	raw, err := in.Param(ctx, "jsonOutput", i)
	if err != nil {
		return nil, fmt.Errorf("reading jsonOutput: %w", err)
	}
	switch t := raw.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return t, nil
	case string:
		if strings.TrimSpace(t) == "" {
			return map[string]any{}, nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(t), &m); err != nil {
			return nil, fmt.Errorf("jsonOutput is not a valid JSON object: %w", err)
		}
		return m, nil
	}
	return nil, fmt.Errorf("jsonOutput must be an object, got %s", nodekit.TypeName(raw))
}
