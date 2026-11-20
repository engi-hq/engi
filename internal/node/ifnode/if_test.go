package ifnode

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func cond(l any, op string, r any) map[string]any {
	return map[string]any{"left": l, "operator": op, "right": r}
}

func TestExecute(t *testing.T) {
	n := New()
	d := n.Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(d.Outputs) != 2 {
		t.Fatalf("outputs = %d", len(d.Outputs))
	}
	items := nodetest.Items(map[string]any{"n": 1.0}, map[string]any{"n": 5.0}, map[string]any{"n": 3.0})
	// The condition reads $json.n; ParamFunc plays the expression engine.
	in := nodetest.New(d, items, nil)
	in.ParamFunc = func(name string, i int) (any, bool) {
		if name == "conditions" {
			return []any{cond(items[i].JSON["n"], "number:gt", 2.0)}, true
		}
		return nil, false
	}
	out := nodetest.Run(t, n, in)
	if len(out.Ports) != 2 {
		t.Fatalf("ports = %d", len(out.Ports))
	}
	if got := nodetest.JSONs(out.Ports[0]); !reflect.DeepEqual(got, []map[string]any{{"n": 5.0}, {"n": 3.0}}) {
		t.Fatalf("true: %v", got)
	}
	if got := nodetest.JSONs(out.Ports[1]); !reflect.DeepEqual(got, []map[string]any{{"n": 1.0}}) {
		t.Fatalf("false: %v", got)
	}
	if got := out.Ports[0][0].PairedItem; !reflect.DeepEqual(got, []nodesdk.PairedItem{{Input: 0, Item: 1}}) {
		t.Fatalf("pairing = %v", got)
	}
}

func TestCombinatorsAndEdges(t *testing.T) {
	n := New()
	d := n.Describe()
	item := nodetest.Items(map[string]any{})
	tests := []struct {
		name   string
		params map[string]any
		toTrue bool
	}{
		{"no conditions is true", map[string]any{}, true},
		{"and fails", map[string]any{"conditions": []any{cond(1.0, "number:equals", 1.0), cond(1.0, "number:equals", 2.0)}}, false},
		{"or passes", map[string]any{"combinator": "or", "conditions": []any{cond(1.0, "number:equals", 2.0), cond(1.0, "number:equals", 1.0)}}, true},
		{"ignore case", map[string]any{"ignoreCase": true, "conditions": []any{cond("A", "string:equals", "a")}}, true},
		{"loose types", map[string]any{"looseTypes": true, "conditions": []any{cond("5", "number:gt", 2.0)}}, true},
		{"exists on null", map[string]any{"conditions": []any{cond(nil, "exists", nil)}}, false},
		{"array length", map[string]any{"conditions": []any{cond([]any{1.0, 2.0}, "array:lengthEquals", 2.0)}}, true},
		{"object not empty", map[string]any{"conditions": []any{cond(map[string]any{"a": 1.0}, "object:isNotEmpty", nil)}}, true},
		{"date compare", map[string]any{"conditions": []any{cond("2026-05-01", "dateTime:after", "2026-01-01")}}, true},
		{"boolean", map[string]any{"conditions": []any{cond(true, "boolean:isTrue", nil)}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := nodetest.Run(t, n, nodetest.New(d, item, tt.params))
			if (len(out.Ports[0]) == 1) != tt.toTrue || (len(out.Ports[1]) == 1) == tt.toTrue {
				t.Fatalf("true=%d false=%d, want toTrue=%v", len(out.Ports[0]), len(out.Ports[1]), tt.toTrue)
			}
		})
	}
	t.Run("empty input", func(t *testing.T) {
		out := nodetest.Run(t, n, nodetest.New(d, nil, nil))
		if len(out.Ports[0])+len(out.Ports[1]) != 0 {
			t.Fatal("expected no output")
		}
	})
}

func TestErrors(t *testing.T) {
	n := New()
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"strict type mismatch", map[string]any{"conditions": []any{cond("x", "number:gt", 1.0)}}, "expected a number"},
		{"unknown operator", map[string]any{"conditions": []any{cond(1.0, "number:near", 1.0)}}, "unknown operator"},
		{"conditions not array", map[string]any{"conditions": "x"}, "must be an array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := n.Execute(context.Background(), nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), tt.params))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

// storedInput reports the conditions as stored, before expressions resolve,
// the way the engine does, so the node can tell typed literals apart.
type storedInput struct {
	*nodetest.Input
	stored map[string]any
}

func (s storedInput) RawParam(name string, _ int) (nodesdk.Value, bool) {
	v, ok := s.stored[name]
	return v, ok
}

func TestTypedLiterals(t *testing.T) {
	n := New()
	d := n.Describe()
	item := nodetest.Items(map[string]any{"n": 13.0})
	tests := []struct {
		name     string
		stored   []any
		resolved []any
		loose    bool
		toTrue   bool
		wantErr  string
	}{
		{"literal text is a number", []any{cond("13", "number:gt", "12")}, []any{cond(13.0, "number:gt", 12.0)}, false, true, ""},
		{"expression result stays strict", []any{cond("={{ $json.n }}", "number:gt", "12")}, []any{cond("13", "number:gt", "12")}, false, false, "expected a number"},
		{"expression result with looseTypes", []any{cond("={{ $json.n }}", "number:gt", "12")}, []any{cond("13", "number:gt", "12")}, true, true, ""},
		{"literal that cannot convert names the value", []any{cond("13", "number:gt", "abc")}, []any{cond("13", "number:gt", "abc")}, false, false, `"abc"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{"conditions": tt.resolved, "looseTypes": tt.loose}
			in := storedInput{Input: nodetest.New(d, item, params), stored: map[string]any{"conditions": tt.stored}}
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
			if (len(out.Ports[0]) == 1) != tt.toTrue {
				t.Fatalf("true=%d false=%d, want toTrue=%v", len(out.Ports[0]), len(out.Ports[1]), tt.toTrue)
			}
		})
	}
}
