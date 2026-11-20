// Package merge implements the Merge node, which combines the items of several
// inputs: appending them, joining by matching fields, pairing by position, or
// choosing one input.
package merge

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.flow.merge"

// Modes.
const (
	modeAppend   = "append"
	modeByFields = "combineByFields"
	modeByPos    = "combineByPosition"
	modeChoose   = "chooseBranch"
)

// Join modes of combineByFields.
const (
	joinMatches    = "keepMatches"
	joinNonMatches = "keepNonMatches"
	joinEverything = "keepEverything"
	joinEnrich1    = "enrichInput1"
	joinEnrich2    = "enrichInput2"
)

// Clash handling when two items share a field name.
const (
	clashPrefer1  = "preferInput1"
	clashPrefer2  = "preferInput2"
	clashSuffix   = "addSuffix"
	multipleAll   = "all"
	multipleFirst = "first"
)

// Node is the Merge node. Its inputs are static: it declares maxInputs of
// them, and only the connected ones are waited for (MergeWaitAll).
type Node struct {
	maxInputs int
}

// New returns a Merge node with maxInputs inputs (at least 2).
func New(maxInputs int) nodesdk.Node { return Node{maxInputs: max(2, maxInputs)} }

// Describe implements nodesdk.Node.
func (n Node) Describe() nodesdk.Descriptor {
	ins := make([]nodesdk.PortSpec, n.maxInputs)
	for i := range ins {
		ins[i] = nodekit.Main("input"+strconv.Itoa(i+1), "Input "+strconv.Itoa(i+1))
	}
	byFields := nodekit.Show("mode", modeByFields)
	clash := nodekit.Show("mode", modeByFields, modeByPos)
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Merge", Group: nodesdk.GroupFlow, Icon: "merge", MergePolicy: nodesdk.MergeWaitAll,
		Description: "Combine the items of several inputs.",
		Inputs:      ins,
		Outputs:     []nodesdk.PortSpec{nodekit.Main("main", "")},
		Properties: []nodesdk.Property{
			{Name: "mode", DisplayName: "Mode", Type: nodesdk.PropOptions, Default: modeAppend, Options: []nodesdk.Option{
				{Name: "Append", Value: modeAppend, Description: "All items of input 1, then input 2, and so on."},
				{Name: "Combine by matching fields", Value: modeByFields, Description: "Join input 1 and input 2 on field values (inputs 1 and 2 only)."},
				{Name: "Combine by position", Value: modeByPos, Description: "Merge the first items of each input, then the second items, and so on."},
				{Name: "Choose branch", Value: modeChoose, Description: "Output the items of one input once all have arrived."}}},
			{Name: "matchFields", DisplayName: "Fields to match", Type: nodesdk.PropFixedCollection, DisplayOptions: byFields,
				Description: "Field of input 1 and, when named differently, of input 2.",
				Children: []nodesdk.Property{
					{Name: "field1", DisplayName: "Input 1 field", Type: nodesdk.PropString, Required: true},
					{Name: "field2", DisplayName: "Input 2 field", Type: nodesdk.PropString, Description: "Defaults to the input 1 field."},
				}},
			{Name: "joinMode", DisplayName: "Output", Type: nodesdk.PropOptions, Default: joinMatches, DisplayOptions: byFields, Options: []nodesdk.Option{
				{Name: "Matches only (inner join)", Value: joinMatches},
				{Name: "Non-matches only", Value: joinNonMatches},
				{Name: "Everything (outer join)", Value: joinEverything},
				{Name: "All of input 1, enriched (left join)", Value: joinEnrich1},
				{Name: "All of input 2, enriched (right join)", Value: joinEnrich2}}},
			{Name: "multipleMatches", DisplayName: "Multiple matches", Type: nodesdk.PropOptions, Default: multipleAll, DisplayOptions: byFields, Options: []nodesdk.Option{
				{Name: "Include all matches", Value: multipleAll}, {Name: "Include first match only", Value: multipleFirst}}},
			{Name: "onClash", DisplayName: "When fields clash", Type: nodesdk.PropOptions, Default: clashPrefer2, DisplayOptions: clash, Options: []nodesdk.Option{
				{Name: "Prefer input 1", Value: clashPrefer1}, {Name: "Prefer input 2", Value: clashPrefer2},
				{Name: "Keep both with input number suffix", Value: clashSuffix}}},
			{Name: "includeUnpaired", DisplayName: "Include unpaired items", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: nodekit.Show("mode", modeByPos),
				Description: "When inputs have different lengths, keep the items that have no partner."},
			{Name: "chooseInput", DisplayName: "Input to output", Type: nodesdk.PropNumber, Default: float64(1), DisplayOptions: nodekit.Show("mode", modeChoose),
				Description: "Number of the input (1-based) whose items are output."},
		},
	}
}

// Execute implements nodesdk.Node.
func (n Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	mode, err := nodesdk.ParamString(ctx, in, "mode", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading mode: %w", err)
	}
	var out []nodesdk.Item
	switch mode {
	case modeAppend, "":
		out = n.appendAll(in)
	case modeByPos:
		out, err = n.byPosition(ctx, in)
	case modeByFields:
		out, err = byFields(ctx, in)
	case modeChoose:
		out, err = n.choose(ctx, in)
	default:
		err = fmt.Errorf("unknown mode %q", mode)
	}
	if err != nil {
		return nodesdk.Output{}, err
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

// fromInput copies item i of a port and links it to that port's item.
func fromInput(it nodesdk.Item, port, i int) nodesdk.Item {
	it.JSON = nodekit.CloneMap(it.JSON)
	if it.JSON == nil {
		it.JSON = map[string]any{}
	}
	it.PairedItem = []nodesdk.PairedItem{{Input: port, Item: i}}
	return it
}

func (n Node) appendAll(in nodesdk.Input) []nodesdk.Item {
	var out []nodesdk.Item
	for p := 0; p < n.maxInputs; p++ {
		for i, it := range in.Items(p) {
			out = append(out, fromInput(it, p, i))
		}
	}
	return out
}

func (n Node) choose(ctx context.Context, in nodesdk.Input) ([]nodesdk.Item, error) {
	num, err := nodesdk.ParamInt(ctx, in, "chooseInput", 0)
	if err != nil {
		return nil, fmt.Errorf("reading chooseInput: %w", err)
	}
	if num < 1 || num > n.maxInputs {
		return nil, fmt.Errorf("chooseInput %d is out of range 1..%d", num, n.maxInputs)
	}
	items := in.Items(num - 1)
	out := make([]nodesdk.Item, len(items))
	for i, it := range items {
		out[i] = fromInput(it, num-1, i)
	}
	return out, nil
}

// part is one item taking part in a merge, with the port it came from.
type part struct {
	item nodesdk.Item
	port int
	idx  int
}

func (n Node) byPosition(ctx context.Context, in nodesdk.Input) ([]nodesdk.Item, error) {
	clash, err := nodesdk.ParamString(ctx, in, "onClash", 0)
	if err != nil {
		return nil, fmt.Errorf("reading onClash: %w", err)
	}
	unpaired, err := nodesdk.ParamBool(ctx, in, "includeUnpaired", 0)
	if err != nil {
		return nil, fmt.Errorf("reading includeUnpaired: %w", err)
	}
	var ports []int
	longest, shortest := 0, -1
	for p := 0; p < n.maxInputs; p++ {
		l := len(in.Items(p))
		if l == 0 {
			continue
		}
		ports = append(ports, p)
		longest = max(longest, l)
		if shortest < 0 || l < shortest {
			shortest = l
		}
	}
	count := shortest
	if unpaired {
		count = longest
	}
	var out []nodesdk.Item
	for i := 0; i < count; i++ {
		var parts []part
		for _, p := range ports {
			if items := in.Items(p); i < len(items) {
				parts = append(parts, part{items[i], p, i})
			}
		}
		out = append(out, mergeParts(parts, clash))
	}
	return out, nil
}

// mergeParts combines items into one. Parts are ordered by port; with
// preferInputN the part from port N-1 is applied last so it wins clashes.
func mergeParts(parts []part, clash string) nodesdk.Item {
	if len(parts) == 1 {
		return fromInput(parts[0].item, parts[0].port, parts[0].idx)
	}
	ordered := make([]part, 0, len(parts))
	var preferred *part
	for i := range parts {
		if clash == clashPrefer1 && parts[i].port == 0 || clash == clashPrefer2 && parts[i].port == 1 {
			preferred = &parts[i]
			continue
		}
		ordered = append(ordered, parts[i])
	}
	if preferred != nil {
		ordered = append(ordered, *preferred)
	}

	counts := map[string]int{}
	for _, p := range parts {
		for k := range p.item.JSON {
			counts[k]++
		}
	}
	res := nodesdk.Item{JSON: map[string]any{}}
	for _, p := range ordered {
		for k, v := range p.item.JSON {
			if clash == clashSuffix && counts[k] > 1 {
				k += "_" + strconv.Itoa(p.port+1)
			}
			res.JSON[k] = nodekit.Clone(v)
		}
		for k, b := range p.item.Binary {
			if res.Binary == nil {
				res.Binary = map[string]nodesdk.BinaryRef{}
			}
			res.Binary[k] = b
		}
	}
	for _, p := range parts {
		res.PairedItem = append(res.PairedItem, nodesdk.PairedItem{Input: p.port, Item: p.idx})
	}
	return res
}

func byFields(ctx context.Context, in nodesdk.Input) ([]nodesdk.Item, error) {
	fields1, fields2, err := readMatchFields(ctx, in)
	if err != nil {
		return nil, err
	}
	joinMode, err := nodesdk.ParamString(ctx, in, "joinMode", 0)
	if err != nil {
		return nil, fmt.Errorf("reading joinMode: %w", err)
	}
	multiple, err := nodesdk.ParamString(ctx, in, "multipleMatches", 0)
	if err != nil {
		return nil, fmt.Errorf("reading multipleMatches: %w", err)
	}
	clash, err := nodesdk.ParamString(ctx, in, "onClash", 0)
	if err != nil {
		return nil, fmt.Errorf("reading onClash: %w", err)
	}
	left, right := in.Items(0), in.Items(1)

	// matches[a] lists the input 2 items matching input 1 item a.
	matches := make([][]int, len(left))
	usedRight := make([]bool, len(right))
	index := map[string][]int{}
	for b, it := range right {
		if k, ok := matchKey(it, fields2); ok {
			index[k] = append(index[k], b)
		}
	}
	for a, it := range left {
		k, ok := matchKey(it, fields1)
		if !ok {
			continue
		}
		m := index[k]
		if multiple == multipleFirst && len(m) > 1 {
			m = m[:1]
		}
		matches[a] = m
		for _, b := range m {
			usedRight[b] = true
		}
	}
	joined := func(a, b int) nodesdk.Item {
		return mergeParts([]part{{left[a], 0, a}, {right[b], 1, b}}, clash)
	}

	var out []nodesdk.Item
	switch joinMode {
	case joinMatches, "":
		for a := range left {
			for _, b := range matches[a] {
				out = append(out, joined(a, b))
			}
		}
	case joinNonMatches:
		for a, it := range left {
			if len(matches[a]) == 0 {
				out = append(out, fromInput(it, 0, a))
			}
		}
		for b, it := range right {
			if !usedRight[b] {
				out = append(out, fromInput(it, 1, b))
			}
		}
	case joinEverything, joinEnrich1:
		for a, it := range left {
			if len(matches[a]) == 0 {
				out = append(out, fromInput(it, 0, a))
			}
			for _, b := range matches[a] {
				out = append(out, joined(a, b))
			}
		}
		if joinMode == joinEverything {
			for b, it := range right {
				if !usedRight[b] {
					out = append(out, fromInput(it, 1, b))
				}
			}
		}
	case joinEnrich2:
		byRight := make([][]int, len(right))
		for a, ms := range matches {
			for _, b := range ms {
				byRight[b] = append(byRight[b], a)
			}
		}
		for b, it := range right {
			if len(byRight[b]) == 0 {
				out = append(out, fromInput(it, 1, b))
			}
			for _, a := range byRight[b] {
				out = append(out, joined(a, b))
			}
		}
	default:
		return nil, fmt.Errorf("unknown joinMode %q", joinMode)
	}
	return out, nil
}

// readMatchFields returns the match field paths for input 1 and input 2.
func readMatchFields(ctx context.Context, in nodesdk.Input) (f1, f2 []string, err error) {
	raw, err := nodekit.ParamSlice(ctx, in, "matchFields", 0)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // already descriptive
	}
	for n, r := range raw {
		var a, b string
		switch t := r.(type) {
		case string:
			a, b = strings.TrimSpace(t), strings.TrimSpace(t)
		case map[string]any:
			a, _ = t["field1"].(string)
			b, _ = t["field2"].(string)
			if a = strings.TrimSpace(a); a == "" {
				return nil, nil, fmt.Errorf("matchFields[%d]: field1 is required", n)
			}
			if b = strings.TrimSpace(b); b == "" {
				b = a
			}
		default:
			return nil, nil, fmt.Errorf("matchFields[%d] must be an object or a string, got %s", n, nodekit.TypeName(r))
		}
		f1, f2 = append(f1, a), append(f2, b)
	}
	if len(f1) == 0 {
		return nil, nil, fmt.Errorf("combine by fields needs at least one field to match")
	}
	return f1, f2, nil
}

// matchKey builds the join key of an item. Items lacking any field, or with a
// null value, never match.
func matchKey(it nodesdk.Item, fields []string) (string, bool) {
	var b strings.Builder
	for _, f := range fields {
		v, ok := nodekit.GetPath(it.JSON, f)
		if !ok || v == nil {
			return "", false
		}
		b.WriteString(nodekit.Key(v))
		b.WriteByte(0)
	}
	return b.String(), true
}
