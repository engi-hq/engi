package switchnode

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
)

func rule(combinator string, conds ...map[string]any) map[string]any {
	cs := make([]any, len(conds))
	for i, c := range conds {
		cs[i] = c
	}
	return map[string]any{"combinator": combinator, "conditions": cs}
}

func eq(l, r any) map[string]any {
	return map[string]any{"left": l, "operator": "string:equals", "right": r}
}

func ids(items []map[string]any) []float64 {
	var out []float64
	for _, m := range items {
		out = append(out, m["id"].(float64))
	}
	return out
}

func TestDescriptor(t *testing.T) {
	d := New(3).Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(d.Outputs) != 4 || d.Outputs[3].Name != "fallback" {
		t.Fatalf("outputs = %+v", d.Outputs)
	}
	if got := len(New(0).Describe().Outputs); got != 2 {
		t.Fatalf("a Switch with no rules still declares one rule output and a fallback, got %d", got)
	}
}

func TestRulesMode(t *testing.T) {
	n := New(3)
	d := n.Describe()
	items := nodetest.Items(
		map[string]any{"id": 1.0, "c": "red"}, map[string]any{"id": 2.0, "c": "green"},
		map[string]any{"id": 3.0, "c": "blue"}, map[string]any{"id": 4.0, "c": "red"})
	rules := func(i int) []any {
		c := items[i].JSON["c"]
		return []any{rule("and", eq(c, "red")), rule("and", eq(c, "green")), rule("or", eq(c, "red"), eq(c, "blue"))}
	}
	tests := []struct {
		name   string
		params map[string]any
		want   [][]float64
	}{
		{"first match wins, no fallback", map[string]any{}, [][]float64{{1, 4}, {2}, {3}, nil}},
		{"all matching", map[string]any{"allMatching": true}, [][]float64{{1, 4}, {2}, {1, 3, 4}, nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := nodetest.New(d, items, tt.params)
			in.ParamFunc = func(name string, i int) (any, bool) {
				if name == "rules" {
					return rules(i), true
				}
				return nil, false
			}
			out := nodetest.Run(t, n, in)
			if len(out.Ports) != 4 {
				t.Fatalf("ports = %d", len(out.Ports))
			}
			for p, want := range tt.want {
				if got := ids(nodetest.JSONs(out.Ports[p])); !reflect.DeepEqual(got, want) {
					t.Errorf("port %d = %v, want %v", p, got, want)
				}
			}
		})
	}
}

func TestFallbackAndOptions(t *testing.T) {
	n := New(2)
	d := n.Describe()
	items := nodetest.Items(map[string]any{"id": 1.0})
	noMatch := []any{rule("and", eq("a", "b"))}
	tests := []struct {
		name     string
		params   map[string]any
		wantPort int // -1 = dropped
	}{
		{"dropped by default", map[string]any{"rules": noMatch}, -1},
		{"fallback output", map[string]any{"rules": noMatch, "fallback": "fallback"}, 2},
		{"ignore case", map[string]any{"rules": []any{rule("and", eq("A", "a"))}, "ignoreCase": true}, 0},
		{"no rules goes to fallback", map[string]any{"fallback": "fallback"}, 2},
		{"empty conditions match", map[string]any{"rules": []any{rule("and")}}, 0},
		{"loose types", map[string]any{"looseTypes": true, "rules": []any{rule("and", map[string]any{"left": "5", "operator": "number:gt", "right": 2.0})}}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := nodetest.Run(t, n, nodetest.New(d, items, tt.params))
			for p, port := range out.Ports {
				if want := p == tt.wantPort; (len(port) == 1) != want {
					t.Errorf("port %d has %d items, want item=%v", p, len(port), want)
				}
			}
		})
	}
}

func TestExpressionMode(t *testing.T) {
	n := New(3)
	d := n.Describe()
	items := nodetest.Items(map[string]any{"id": 1.0, "to": 2.0}, map[string]any{"id": 2.0, "to": 0.0}, map[string]any{"id": 3.0, "to": 2.0})
	in := nodetest.New(d, items, map[string]any{"mode": "expression"})
	in.ParamFunc = func(name string, i int) (any, bool) {
		if name == "outputIndex" {
			return items[i].JSON["to"], true
		}
		return nil, false
	}
	out := nodetest.Run(t, n, in)
	if got := ids(nodetest.JSONs(out.Ports[2])); !reflect.DeepEqual(got, []float64{1, 3}) {
		t.Fatalf("port 2 = %v", got)
	}
	if got := ids(nodetest.JSONs(out.Ports[0])); !reflect.DeepEqual(got, []float64{2}) {
		t.Fatalf("port 0 = %v", got)
	}
}

func TestErrors(t *testing.T) {
	n := New(2)
	d := n.Describe()
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"index out of range", map[string]any{"mode": "expression", "outputIndex": 2.0}, "out of range"},
		{"negative index", map[string]any{"mode": "expression", "outputIndex": -1.0}, "out of range"},
		{"fractional index", map[string]any{"mode": "expression", "outputIndex": 0.5}, "whole number"},
		{"too many rules", map[string]any{"rules": []any{rule("and"), rule("and"), rule("and")}}, "3 rules"},
		{"rule not object", map[string]any{"rules": []any{"x"}}, "must be an object"},
		{"bad condition", map[string]any{"rules": []any{rule("and", map[string]any{"operator": "nope"})}}, "unknown operator"},
		{"strict type error", map[string]any{"rules": []any{rule("and", map[string]any{"left": "x", "operator": "number:gt", "right": 1.0})}}, "expected a number"},
		{"unknown mode", map[string]any{"mode": "magic"}, "unknown mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := n.Execute(context.Background(), nodetest.New(d, nodetest.Items(map[string]any{}), tt.params))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
	t.Run("empty input", func(t *testing.T) {
		out := nodetest.Run(t, n, nodetest.New(d, nil, nil))
		if len(out.Ports) != 3 {
			t.Fatalf("ports = %d", len(out.Ports))
		}
	})
}

// storedInput reports the rules as stored, before expressions resolve, so the
// node can tell typed literals from expression results.
type storedInput struct {
	*nodetest.Input
	stored map[string]any
}

func (s storedInput) RawParam(name string, _ int) (any, bool) {
	v, ok := s.stored[name]
	return v, ok
}

func TestTypedLiteralRules(t *testing.T) {
	n := New(2)
	d := n.Describe()
	items := nodetest.Items(map[string]any{"n": 13.0})
	numRule := func(left, right any) map[string]any {
		return map[string]any{"combinator": "and", "conditions": []any{
			map[string]any{"left": left, "operator": "number:gt", "right": right}}}
	}
	tests := []struct {
		name     string
		stored   []any
		resolved []any
		loose    bool
		wantOut0 int
		wantErr  string
	}{
		{"literal right text is a number", []any{numRule("={{ $json.n }}", "12")}, []any{numRule(13.0, "12")}, false, 1, ""},
		{"literal left text is a number", []any{numRule("13", "12")}, []any{numRule("13", "12")}, false, 1, ""},
		{"expression result stays strict", []any{numRule("={{ $json.n }}", "={{ $json.min }}")}, []any{numRule("13", "12")}, false, 0, "expected a number"},
		{"expression result with looseTypes", []any{numRule("={{ $json.n }}", "={{ $json.min }}")}, []any{numRule("13", "12")}, true, 1, ""},
		{"literal that cannot convert names the rule and value", []any{numRule("={{ $json.n }}", "abc")}, []any{numRule(13.0, "abc")}, false, 0, `rule 1: conditions[0] (Number: is greater than): value 2: cannot convert "abc"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{"mode": modeRules, "rules": tt.resolved, "looseTypes": tt.loose}
			in := storedInput{Input: nodetest.New(d, items, params), stored: map[string]any{"rules": tt.stored}}
			out, err := n.Execute(context.Background(), in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := len(out.Ports[0]); got != tt.wantOut0 {
				t.Fatalf("output 0 has %d items, want %d", got, tt.wantOut0)
			}
		})
	}
}
