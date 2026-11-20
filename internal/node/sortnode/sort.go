// Package sortnode implements the Sort node: a stable, multi-key, type-aware
// ordering of items.
package sortnode

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.transform.sort"

// Sort key types and orders.
const (
	typeAuto   = "auto"
	typeString = "string"
	typeNumber = "number"
	typeDate   = "dateTime"
	orderAsc   = "asc"
	orderDesc  = "desc"
)

// Node is the Sort node.
type Node struct{}

// New returns a Sort node.
func New() nodesdk.Node { return Node{} }

// Describe implements nodesdk.Node.
func (Node) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Sort", Group: nodesdk.GroupTransform, Icon: "arrow-up-down",
		Description: "Order items by one or more fields. Items missing a field sort last.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "keys", DisplayName: "Sort by", Type: nodesdk.PropFixedCollection, Required: true,
				Description: "Keys apply in order; later keys break ties.",
				Children: []nodesdk.Property{
					{Name: "field", DisplayName: "Field", Type: nodesdk.PropString, Required: true},
					{Name: "order", DisplayName: "Order", Type: nodesdk.PropOptions, Default: orderAsc, Options: []nodesdk.Option{
						{Name: "Ascending", Value: orderAsc}, {Name: "Descending", Value: orderDesc}}},
					{Name: "type", DisplayName: "Type", Type: nodesdk.PropOptions, Default: typeAuto, Options: []nodesdk.Option{
						{Name: "Auto", Value: typeAuto}, {Name: "String", Value: typeString},
						{Name: "Number", Value: typeNumber}, {Name: "Date", Value: typeDate}}},
					{Name: "ignoreCase", DisplayName: "Ignore case", Type: nodesdk.PropBoolean, Default: false},
				}},
		},
	}
}

type key struct {
	field      string
	desc       bool
	typ        string
	ignoreCase bool
}

// Execute implements nodesdk.Node.
func (Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	keys, err := readKeys(ctx, in)
	if err != nil {
		return nodesdk.Output{}, err
	}
	items := in.Items(0)
	type entry struct {
		item nodesdk.Item
		pos  int
		vals []sortValue
	}
	entries := make([]entry, len(items))
	for i, it := range items {
		entries[i] = entry{item: it, pos: i, vals: make([]sortValue, len(keys))}
		for k, ky := range keys {
			v, _ := nodekit.GetPath(it.JSON, ky.field)
			sv, err := toSortValue(v, ky)
			if err != nil {
				return nodesdk.Output{}, nodekit.ItemError(i, fmt.Errorf("field %q: %w", ky.field, err))
			}
			entries[i].vals[k] = sv
		}
	}
	slices.SortStableFunc(entries, func(a, b entry) int {
		for k, ky := range keys {
			if c := compareSort(a.vals[k], b.vals[k], ky.desc); c != 0 {
				return c
			}
		}
		return 0
	})
	out := make([]nodesdk.Item, len(entries))
	for i, e := range entries {
		out[i] = nodekit.Paired(e.item, e.pos)
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func readKeys(ctx context.Context, in nodesdk.Input) ([]key, error) {
	raw, err := nodekit.ParamSlice(ctx, in, "keys", 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // already descriptive
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("sort needs at least one key")
	}
	keys := make([]key, 0, len(raw))
	for n, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("key %d must be an object, got %s", n+1, nodekit.TypeName(r))
		}
		ky := key{typ: typeAuto}
		ky.field, _ = m["field"].(string)
		if strings.TrimSpace(ky.field) == "" {
			return nil, fmt.Errorf("key %d: field is required", n+1)
		}
		switch order, _ := m["order"].(string); order {
		case "", orderAsc:
		case orderDesc:
			ky.desc = true
		default:
			return nil, fmt.Errorf("key %d: unknown order %q", n+1, order)
		}
		if t, _ := m["type"].(string); t != "" {
			ky.typ = t
		}
		ky.ignoreCase, _ = m["ignoreCase"].(bool)
		keys = append(keys, ky)
	}
	return keys, nil
}

// sortValue is a comparable form of a field value. Missing values sort last.
type sortValue struct {
	missing bool
	num     float64
	str     string
	isNum   bool
	cmp     any // fallback for auto-typed values that are neither numbers nor strings
}

func toSortValue(v any, k key) (sortValue, error) {
	if v == nil {
		return sortValue{missing: true}, nil
	}
	switch k.typ {
	case typeNumber:
		f, err := nodekit.ToFloat(v, true)
		if err != nil {
			return sortValue{}, err //nolint:wrapcheck // already descriptive
		}
		return sortValue{num: f, isNum: true}, nil
	case typeString:
		return sortValue{str: fold(nodekit.ToString(v), k.ignoreCase)}, nil
	case typeDate:
		t, err := nodekit.ToTime(v)
		if err != nil {
			return sortValue{}, err //nolint:wrapcheck // already descriptive
		}
		return sortValue{num: float64(t.UnixNano()), isNum: true}, nil
	case typeAuto:
		switch t := v.(type) {
		case string:
			return sortValue{str: fold(t, k.ignoreCase)}, nil
		case time.Time:
			return sortValue{num: float64(t.UnixNano()), isNum: true}, nil
		}
		if f, err := nodekit.ToFloat(v, false); err == nil {
			return sortValue{num: f, isNum: true}, nil
		}
		return sortValue{cmp: v}, nil
	}
	return sortValue{}, fmt.Errorf("unknown sort type %q", k.typ)
}

// Value classes, ordered: numbers, then strings, then everything else.
const (
	rankNumber = iota
	rankString
	rankOther
)

func (v sortValue) rank() int {
	switch {
	case v.isNum:
		return rankNumber
	case v.cmp != nil:
		return rankOther
	}
	return rankString
}

func fold(s string, ignoreCase bool) string {
	if ignoreCase {
		return strings.ToLower(s)
	}
	return s
}

func compareSort(a, b sortValue, desc bool) int {
	switch {
	case a.missing && b.missing:
		return 0
	case a.missing:
		return 1
	case b.missing:
		return -1
	}
	var c int
	switch ra, rb := a.rank(), b.rank(); {
	case ra != rb:
		c = ra - rb
	case ra == rankNumber:
		switch {
		case a.num < b.num:
			c = -1
		case a.num > b.num:
			c = 1
		}
	case ra == rankString:
		c = strings.Compare(a.str, b.str)
	default:
		c = nodekit.Compare(a.cmp, b.cmp)
	}
	if desc {
		return -c
	}
	return c
}
