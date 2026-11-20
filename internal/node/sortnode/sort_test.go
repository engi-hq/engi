package sortnode

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
)

func sk(field, order, typ string, ignoreCase bool) map[string]any {
	return map[string]any{"field": field, "order": order, "type": typ, "ignoreCase": ignoreCase}
}

func order(out []map[string]any) []string {
	var ids []string
	for _, m := range out {
		ids = append(ids, m["id"].(string))
	}
	return ids
}

func TestExecute(t *testing.T) {
	n := New()
	d := n.Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		items []map[string]any
		keys  []any
		want  []string
	}{
		{"numbers ascending", []map[string]any{{"id": "a", "v": 10.0}, {"id": "b", "v": 2.0}, {"id": "c", "v": 33.0}},
			[]any{sk("v", "asc", "auto", false)}, []string{"b", "a", "c"}},
		{"numbers descending", []map[string]any{{"id": "a", "v": 10.0}, {"id": "b", "v": 2.0}, {"id": "c", "v": 33.0}},
			[]any{sk("v", "desc", "auto", false)}, []string{"c", "a", "b"}},
		{"strings are case sensitive by default", []map[string]any{{"id": "a", "v": "b"}, {"id": "b", "v": "A"}, {"id": "c", "v": "a"}},
			[]any{sk("v", "asc", "string", false)}, []string{"b", "c", "a"}},
		{"ignore case keeps ties stable", []map[string]any{{"id": "a", "v": "b"}, {"id": "b", "v": "A"}, {"id": "c", "v": "a"}},
			[]any{sk("v", "asc", "string", true)}, []string{"b", "c", "a"}},
		{"numeric strings with number type", []map[string]any{{"id": "a", "v": "10"}, {"id": "b", "v": "9"}},
			[]any{sk("v", "asc", "number", false)}, []string{"b", "a"}},
		{"numeric strings sort lexically as strings", []map[string]any{{"id": "a", "v": "10"}, {"id": "b", "v": "9"}},
			[]any{sk("v", "asc", "string", false)}, []string{"a", "b"}},
		{"dates", []map[string]any{{"id": "a", "v": "2026-03-01"}, {"id": "b", "v": "2025-12-31T23:59:59Z"}, {"id": "c", "v": "2026-01-15"}},
			[]any{sk("v", "asc", "dateTime", false)}, []string{"b", "c", "a"}},
		{"missing sorts last ascending", []map[string]any{{"id": "a"}, {"id": "b", "v": 1.0}, {"id": "c", "v": nil}, {"id": "d", "v": 0.0}},
			[]any{sk("v", "asc", "auto", false)}, []string{"d", "b", "a", "c"}},
		{"missing sorts last descending", []map[string]any{{"id": "a"}, {"id": "b", "v": 1.0}, {"id": "d", "v": 0.0}},
			[]any{sk("v", "desc", "auto", false)}, []string{"b", "d", "a"}},
		{"multi key", []map[string]any{{"id": "a", "g": "x", "v": 1.0}, {"id": "b", "g": "y", "v": 5.0}, {"id": "c", "g": "x", "v": 9.0}},
			[]any{sk("g", "asc", "auto", false), sk("v", "desc", "auto", false)}, []string{"c", "a", "b"}},
		{"dot path", []map[string]any{{"id": "a", "u": map[string]any{"age": 5.0}}, {"id": "b", "u": map[string]any{"age": 3.0}}},
			[]any{sk("u.age", "asc", "auto", false)}, []string{"b", "a"}},
		{"auto orders numbers before strings", []map[string]any{{"id": "a", "v": "x"}, {"id": "b", "v": 2.0}, {"id": "c", "v": true}},
			[]any{sk("v", "asc", "auto", false)}, []string{"b", "a", "c"}},
		{"equal values keep input order", []map[string]any{{"id": "a", "v": 1.0}, {"id": "b", "v": 1.0}, {"id": "c", "v": 1.0}},
			[]any{sk("v", "desc", "auto", false)}, []string{"a", "b", "c"}},
		{"empty input", nil, []any{sk("v", "asc", "auto", false)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := nodetest.Run(t, n, nodetest.New(d, nodetest.Items(tt.items...), map[string]any{"keys": tt.keys}))
			if got := order(nodetest.JSONs(out.Ports[0])); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPairing(t *testing.T) {
	n := New()
	items := nodetest.Items(map[string]any{"id": "a", "v": 2.0}, map[string]any{"id": "b", "v": 1.0})
	out := nodetest.Run(t, n, nodetest.New(n.Describe(), items, map[string]any{"keys": []any{sk("v", "asc", "auto", false)}}))
	if out.Ports[0][0].PairedItem[0].Item != 1 {
		t.Fatalf("pairing = %v", out.Ports[0][0].PairedItem)
	}
}

func TestErrors(t *testing.T) {
	n := New()
	items := nodetest.Items(map[string]any{"id": "a", "v": "x"})
	tests := []struct {
		name string
		keys any
		want string
	}{
		{"no keys", []any{}, "at least one key"},
		{"missing keys", nil, "at least one key"},
		{"keys wrong type", "v", "must be an array"},
		{"key not object", []any{"v"}, "must be an object"},
		{"key without field", []any{sk("", "asc", "auto", false)}, "field is required"},
		{"bad order", []any{sk("v", "up", "auto", false)}, "unknown order"},
		{"bad type", []any{sk("v", "asc", "color", false)}, "unknown sort type"},
		{"unparsable number", []any{sk("v", "asc", "number", false)}, "convert"},
		{"unparsable date", []any{sk("v", "asc", "dateTime", false)}, "ISO-8601"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := n.Execute(context.Background(), nodetest.New(n.Describe(), items, map[string]any{"keys": tt.keys}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
