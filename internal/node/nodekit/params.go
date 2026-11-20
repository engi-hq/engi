package nodekit

import (
	"context"
	"fmt"
	"strings"

	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Main returns a main-kind port spec.
func Main(name, displayName string) nodesdk.PortSpec {
	return nodesdk.PortSpec{Name: name, Kind: nodesdk.PortMain, DisplayName: displayName}
}

// ParamMap reads an object parameter; a missing value is an empty map.
func ParamMap(ctx context.Context, in nodesdk.Input, name string, itemIndex int) (map[string]any, error) {
	v, err := in.Param(ctx, name, itemIndex)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", name, err)
	}
	switch t := v.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return t, nil
	}
	return nil, fmt.Errorf("parameter %q must be an object, got %s", name, TypeName(v))
}

// ParamSlice reads an array parameter; a missing value is an empty slice.
func ParamSlice(ctx context.Context, in nodesdk.Input, name string, itemIndex int) ([]any, error) {
	v, err := in.Param(ctx, name, itemIndex)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", name, err)
	}
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		return t, nil
	}
	return nil, fmt.Errorf("parameter %q must be an array, got %s", name, TypeName(v))
}

// ParamFloat reads a numeric parameter, accepting numeric strings.
func ParamFloat(ctx context.Context, in nodesdk.Input, name string, itemIndex int) (float64, error) {
	v, err := in.Param(ctx, name, itemIndex)
	if err != nil {
		return 0, fmt.Errorf("reading parameter %q: %w", name, err)
	}
	f, err := ToFloat(v, true)
	if err != nil {
		return 0, fmt.Errorf("parameter %q: %w", name, err)
	}
	return f, nil
}

// ParamStringList reads a list of names given as an array or as a
// comma-separated string. Blank entries are dropped.
func ParamStringList(ctx context.Context, in nodesdk.Input, name string, itemIndex int) ([]string, error) {
	v, err := in.Param(ctx, name, itemIndex)
	if err != nil {
		return nil, fmt.Errorf("reading parameter %q: %w", name, err)
	}
	return StringList(v)
}

// StringList converts an array of strings or a comma-separated string.
func StringList(v any) ([]string, error) {
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	switch t := v.(type) {
	case nil:
	case string:
		for _, p := range strings.Split(t, ",") {
			add(p)
		}
	case []any:
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("list entries must be strings, got %s", TypeName(e))
			}
			add(s)
		}
	default:
		return nil, fmt.Errorf("expected a list of names, got %s", TypeName(v))
	}
	return out, nil
}

// Opt builds an options entry whose value equals its name's identifier.
func Opt(value, name string) nodesdk.Option { return nodesdk.Option{Name: name, Value: value} }

// Show builds a DisplayOptions that shows a property when other == any of values.
func Show(other string, values ...any) *nodesdk.DisplayOptions {
	return &nodesdk.DisplayOptions{Show: map[string][]any{other: values}}
}

// Paired marks an output item as produced from input item i of port 0.
func Paired(it nodesdk.Item, i int) nodesdk.Item {
	it.PairedItem = []nodesdk.PairedItem{{Input: 0, Item: i}}
	return it
}

// ClonePaired copies the item JSON and links it to input item i.
func ClonePaired(it nodesdk.Item, i int) nodesdk.Item {
	it.JSON = CloneMap(it.JSON)
	return Paired(it, i)
}
