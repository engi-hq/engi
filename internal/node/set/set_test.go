package set

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func field(name, typ string, value any) map[string]any {
	return map[string]any{"name": name, "type": typ, "value": value}
}

func TestExecute(t *testing.T) {
	n := New()
	if err := n.Describe().Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		items  []map[string]any
		params map[string]any
		want   []map[string]any
	}{
		{"adds a field and keeps others", []map[string]any{{"a": 1.0}},
			map[string]any{"fields": []any{field("b", "string", "x")}},
			[]map[string]any{{"a": 1.0, "b": "x"}}},
		{"overwrites", []map[string]any{{"a": 1.0}},
			map[string]any{"fields": []any{field("a", "auto", 2.0)}},
			[]map[string]any{{"a": 2.0}}},
		{"drops other fields when asked", []map[string]any{{"a": 1.0, "z": 9.0}},
			map[string]any{"keepOtherFields": false, "fields": []any{field("b", "auto", true)}},
			[]map[string]any{{"b": true}}},
		{"dot path", []map[string]any{{"user": map[string]any{"id": 1.0}}},
			map[string]any{"fields": []any{field("user.name", "string", "ann")}},
			[]map[string]any{{"user": map[string]any{"id": 1.0, "name": "ann"}}}},
		{"dot notation off", []map[string]any{{}},
			map[string]any{"dotNotation": false, "fields": []any{field("a.b", "auto", 1.0)}},
			[]map[string]any{{"a.b": 1.0}}},
		{"type coercion", []map[string]any{{}},
			map[string]any{"fields": []any{field("n", "number", "42"), field("s", "string", 7.0), field("b", "boolean", "true"),
				field("arr", "array", "[1,2]"), field("obj", "object", `{"k":"v"}`)}},
			[]map[string]any{{"n": 42.0, "s": "7", "b": true, "arr": []any{1.0, 2.0}, "obj": map[string]any{"k": "v"}}}},
		{"no fields is a copy", []map[string]any{{"a": 1.0}}, map[string]any{},
			[]map[string]any{{"a": 1.0}}},
		{"empty input", nil, map[string]any{"fields": []any{field("a", "auto", 1.0)}}, []map[string]any{}},
		{"json mode from object", []map[string]any{{"a": 1.0}},
			map[string]any{"mode": "json", "jsonOutput": map[string]any{"b": 2.0}},
			[]map[string]any{{"a": 1.0, "b": 2.0}}},
		{"json mode from string", []map[string]any{{"a": 1.0}},
			map[string]any{"mode": "json", "jsonOutput": `{"b": {"c": 3}}`},
			[]map[string]any{{"a": 1.0, "b": map[string]any{"c": 3.0}}}},
		{"json mode without keeping", []map[string]any{{"a": 1.0}},
			map[string]any{"mode": "json", "keepOtherFields": false, "jsonOutput": `{"b": 2}`},
			[]map[string]any{{"b": 2.0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := nodetest.Run(t, n, nodetest.New(n.Describe(), nodetest.Items(tt.items...), tt.params))
			if got := nodetest.JSONs(out.Ports[0]); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestPerItemParams(t *testing.T) {
	n := New()
	in := nodetest.New(n.Describe(), nodetest.Items(map[string]any{"v": 1.0}, map[string]any{"v": 2.0}), nil)
	in.ParamFunc = func(name string, i int) (any, bool) {
		if name == "fields" {
			return []any{field("double", "number", float64(i+1)*2)}, true // stands in for an expression using $json
		}
		return nil, false
	}
	out := nodetest.Run(t, n, in)
	want := []map[string]any{{"v": 1.0, "double": 2.0}, {"v": 2.0, "double": 4.0}}
	if got := nodetest.JSONs(out.Ports[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestDoesNotAliasInput(t *testing.T) {
	n := New()
	nested := map[string]any{"k": 1.0}
	val := map[string]any{"x": 1.0}
	items := []nodesdk.Item{{JSON: map[string]any{"nested": nested}}}
	out := nodetest.Run(t, n, nodetest.New(n.Describe(), items, map[string]any{"fields": []any{field("copy", "auto", val)}}))
	out.Ports[0][0].JSON["nested"].(map[string]any)["k"] = 99.0
	out.Ports[0][0].JSON["copy"].(map[string]any)["x"] = 99.0
	if nested["k"] != 1.0 || val["x"] != 1.0 {
		t.Fatal("output aliases input or parameter data")
	}
}

func TestErrors(t *testing.T) {
	n := New()
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"unknown mode", map[string]any{"mode": "x"}, "unknown mode"},
		{"fields not an array", map[string]any{"fields": "x"}, "must be an array"},
		{"field not an object", map[string]any{"fields": []any{"x"}}, "must be an object"},
		{"field without name", map[string]any{"fields": []any{field("", "auto", 1.0)}}, "name is required"},
		{"bad coercion", map[string]any{"fields": []any{field("a", "number", "abc")}}, "convert"},
		{"bad json text", map[string]any{"mode": "json", "jsonOutput": "{"}, "valid JSON"},
		{"json not an object", map[string]any{"mode": "json", "jsonOutput": "[1]"}, "valid JSON"},
		{"json wrong type", map[string]any{"mode": "json", "jsonOutput": 5.0}, "must be an object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := n.Execute(context.Background(), nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), tt.params))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
			var ne *nodesdk.NodeError
			if !errors.As(err, &ne) || ne.ItemIndex == nil || *ne.ItemIndex != 0 {
				t.Fatalf("error should carry the item index: %#v", err)
			}
		})
	}
}
