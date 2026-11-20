// Package switchnode implements the Switch node, which routes each item to one
// of several outputs by rules or by an index expression.
package switchnode

import (
	"context"
	"fmt"
	"strconv"

	"github.com/engi-hq/engi/internal/node/nodekit"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Type is the node type id.
const Type = "engi.flow.switch"

// Modes and fallback behaviours.
const (
	modeRules      = "rules"
	modeExpression = "expression"
	fallbackNone   = "none"
	fallbackOutput = "fallback"
)

// Node is the Switch node. A descriptor is static, so the node declares
// maxRules rule outputs plus one fallback output at index maxRules.
type Node struct {
	maxRules int
}

// New returns a Switch node with maxRules rule outputs (at least 1).
func New(maxRules int) nodesdk.Node { return Node{maxRules: max(1, maxRules)} }

// Describe implements nodesdk.Node.
func (n Node) Describe() nodesdk.Descriptor {
	outs := make([]nodesdk.PortSpec, 0, n.maxRules+1)
	for i := 0; i < n.maxRules; i++ {
		outs = append(outs, nodekit.Main(strconv.Itoa(i), "Output "+strconv.Itoa(i)))
	}
	outs = append(outs, nodekit.Main("fallback", "Fallback"))

	rules := nodesdk.Property{
		Name: "rules", DisplayName: "Rules", Type: nodesdk.PropFixedCollection, DisplayOptions: nodekit.Show("mode", modeRules),
		Description: "Rule N sends matching items to output N. Each rule holds conditions and how to combine them.",
		Children: []nodesdk.Property{
			{Name: "conditions", DisplayName: "Conditions", Type: nodesdk.PropFixedCollection, Children: []nodesdk.Property{
				{Name: "left", DisplayName: "Value 1", Type: nodesdk.PropString},
				{Name: "operator", DisplayName: "Operator", Type: nodesdk.PropOptions, Default: "string:equals", Options: nodekit.OperatorOptions()},
				{Name: "right", DisplayName: "Value 2", Type: nodesdk.PropString},
			}},
			{Name: "combinator", DisplayName: "Combine", Type: nodesdk.PropOptions, Default: nodekit.CombinatorAnd, Options: []nodesdk.Option{
				{Name: "All conditions (AND)", Value: nodekit.CombinatorAnd}, {Name: "Any condition (OR)", Value: nodekit.CombinatorOr}}},
		},
	}
	return nodesdk.Descriptor{
		Type: Type, Version: 1, DisplayName: "Switch", Group: nodesdk.GroupFlow, Icon: "git-fork",
		Description: "Route items to different outputs.",
		Inputs:      []nodesdk.PortSpec{nodekit.Main("main", "")},
		Outputs:     outs,
		Properties: []nodesdk.Property{
			{Name: "mode", DisplayName: "Mode", Type: nodesdk.PropOptions, Default: modeRules, Options: []nodesdk.Option{
				{Name: "Rules", Value: modeRules}, {Name: "Expression (output index)", Value: modeExpression}}},
			rules,
			{Name: "outputIndex", DisplayName: "Output index", Type: nodesdk.PropNumber, Default: float64(0), DisplayOptions: nodekit.Show("mode", modeExpression),
				Description: "Zero-based output for the item, usually an expression."},
			{Name: "fallback", DisplayName: "No match", Type: nodesdk.PropOptions, Default: fallbackNone, DisplayOptions: nodekit.Show("mode", modeRules), Options: []nodesdk.Option{
				{Name: "Drop the item", Value: fallbackNone}, {Name: "Send to the fallback output", Value: fallbackOutput}}},
			{Name: "allMatching", DisplayName: "Send to all matching outputs", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: nodekit.Show("mode", modeRules),
				Description: "By default an item goes to the first matching rule only."},
			{Name: "ignoreCase", DisplayName: "Ignore case", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: nodekit.Show("mode", modeRules)},
			{Name: "looseTypes", DisplayName: "Loose type validation", Type: nodesdk.PropBoolean, Default: false, DisplayOptions: nodekit.Show("mode", modeRules)},
		},
	}
}

// Execute implements nodesdk.Node.
func (n Node) Execute(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	mode, err := nodesdk.ParamString(ctx, in, "mode", 0)
	if err != nil {
		return nodesdk.Output{}, fmt.Errorf("reading mode: %w", err)
	}
	ports := make([][]nodesdk.Item, n.maxRules+1)
	for i, it := range in.Items(0) {
		var targets []int
		switch mode {
		case modeExpression:
			targets, err = n.byIndex(ctx, in, i)
		case modeRules, "":
			targets, err = n.byRules(ctx, in, i)
		default:
			err = fmt.Errorf("unknown mode %q", mode)
		}
		if err != nil {
			return nodesdk.Output{}, nodekit.ItemError(i, err)
		}
		for _, t := range targets {
			ports[t] = append(ports[t], nodekit.Paired(it, i))
		}
	}
	return nodesdk.Output{Ports: ports}, nil
}

func (n Node) byIndex(ctx context.Context, in nodesdk.Input, i int) ([]int, error) {
	idx, err := nodesdk.ParamInt(ctx, in, "outputIndex", i)
	if err != nil {
		return nil, fmt.Errorf("reading outputIndex: %w", err)
	}
	if idx < 0 || idx >= n.maxRules {
		return nil, fmt.Errorf("output index %d is out of range 0..%d", idx, n.maxRules-1)
	}
	return []int{idx}, nil
}

func (n Node) byRules(ctx context.Context, in nodesdk.Input, i int) ([]int, error) {
	rules, err := nodekit.ParamSlice(ctx, in, "rules", i)
	if err != nil {
		return nil, err //nolint:wrapcheck // already descriptive
	}
	if len(rules) > n.maxRules {
		return nil, fmt.Errorf("%d rules configured but the node has %d rule outputs", len(rules), n.maxRules)
	}
	all, err := nodesdk.ParamBool(ctx, in, "allMatching", i)
	if err != nil {
		return nil, fmt.Errorf("reading allMatching: %w", err)
	}
	var opts nodekit.EvalOptions
	if opts.IgnoreCase, err = nodesdk.ParamBool(ctx, in, "ignoreCase", i); err != nil {
		return nil, fmt.Errorf("reading ignoreCase: %w", err)
	}
	if opts.Loose, err = nodesdk.ParamBool(ctx, in, "looseTypes", i); err != nil {
		return nil, fmt.Errorf("reading looseTypes: %w", err)
	}

	// The stored rules tell typed literals from expression results; rules
	// and their conditions line up with the resolved ones by index.
	stored, _ := nodekit.StoredParam(in, "rules", i)
	storedRules, _ := stored.([]any)

	var targets []int
	for r, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("rule %d must be an object, got %s", r+1, nodekit.TypeName(raw))
		}
		conds, err := nodekit.ParseConditions(rule["conditions"])
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", r+1, err)
		}
		if err := nodekit.CoerceLiterals(storedConditions(storedRules, r), conds); err != nil {
			return nil, fmt.Errorf("rule %d: %w", r+1, err)
		}
		comb, _ := rule["combinator"].(string)
		match, err := nodekit.Group{Combinator: comb, Conditions: conds}.Eval(opts)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", r+1, err)
		}
		if match {
			targets = append(targets, r)
			if !all {
				return targets, nil
			}
		}
	}
	if len(targets) > 0 {
		return targets, nil
	}
	fb, err := nodesdk.ParamString(ctx, in, "fallback", i)
	if err != nil {
		return nil, fmt.Errorf("reading fallback: %w", err)
	}
	if fb == fallbackOutput {
		return []int{n.maxRules}, nil
	}
	return nil, nil
}

// storedConditions returns the stored conditions of rule r, or nil when the
// rule has none stored.
func storedConditions(storedRules []any, r int) any {
	if r >= len(storedRules) {
		return nil
	}
	rule, ok := storedRules[r].(map[string]any)
	if !ok {
		return nil
	}
	return rule["conditions"]
}
