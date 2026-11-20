package filter

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestExecute(t *testing.T) {
	n := New()
	d := n.Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	items := nodetest.Items(map[string]any{"age": 17.0}, map[string]any{"age": 30.0}, map[string]any{"age": 45.0})
	tests := []struct {
		name          string
		keepDiscarded bool
		wantKept      []float64
		wantDiscarded []float64
	}{
		{"keeps matches", false, []float64{30, 45}, nil},
		{"routes discarded", true, []float64{30, 45}, []float64{17}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := nodetest.New(d, items, map[string]any{"keepDiscarded": tt.keepDiscarded})
			in.ParamFunc = func(name string, i int) (any, bool) {
				if name == "conditions" {
					return []any{map[string]any{"left": items[i].JSON["age"], "operator": "number:gte", "right": 18.0}}, true
				}
				return nil, false
			}
			out := nodetest.Run(t, n, in)
			ages := func(p int) []float64 {
				var a []float64
				for _, it := range out.Ports[p] {
					a = append(a, it.JSON["age"].(float64))
				}
				return a
			}
			if !reflect.DeepEqual(ages(0), tt.wantKept) || !reflect.DeepEqual(ages(1), tt.wantDiscarded) {
				t.Fatalf("kept=%v discarded=%v", ages(0), ages(1))
			}
		})
	}
	t.Run("empty input", func(t *testing.T) {
		out := nodetest.Run(t, n, nodetest.New(d, nil, nil))
		if len(out.Ports[0]) != 0 {
			t.Fatal("expected no output")
		}
	})
	t.Run("pairing follows the original index", func(t *testing.T) {
		in := nodetest.New(d, items, nil)
		in.ParamFunc = func(name string, i int) (any, bool) {
			if name == "conditions" {
				return []any{map[string]any{"left": float64(i), "operator": "number:equals", "right": 2.0}}, true
			}
			return nil, false
		}
		out := nodetest.Run(t, n, in)
		if len(out.Ports[0]) != 1 || out.Ports[0][0].PairedItem[0].Item != 2 {
			t.Fatalf("got %+v", out.Ports[0])
		}
	})
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
	items := nodetest.Items(map[string]any{"age": 17.0}, map[string]any{"age": 30.0})
	tests := []struct {
		name     string
		right    any // stored right value: a literal or an expression
		resolved any // what the expression engine resolved it to
		wantKept []float64
		wantErr  string
	}{
		{name: "literal right value is converted", right: "18", resolved: "18", wantKept: []float64{30}},
		{name: "expression right value stays strict", right: "={{ $json.min }}", resolved: "18", wantErr: "expected a number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := storedInput{Input: nodetest.New(d, items, map[string]any{"keepDiscarded": true}), stored: map[string]any{}}
			in.ParamFunc = func(name string, i int) (any, bool) {
				if name != "conditions" {
					return nil, false
				}
				return []any{map[string]any{"left": items[i].JSON["age"], "operator": "number:gte", "right": tt.resolved}}, true
			}
			in.stored["conditions"] = []any{map[string]any{"left": "={{ $json.age }}", "operator": "number:gte", "right": tt.right}}
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
			var kept []float64
			for _, it := range out.Ports[0] {
				kept = append(kept, it.JSON["age"].(float64))
			}
			if !reflect.DeepEqual(kept, tt.wantKept) {
				t.Fatalf("kept = %v, want %v", kept, tt.wantKept)
			}
		})
	}
}
