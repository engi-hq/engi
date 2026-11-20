package aggregate

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
)

func f(field, name string) map[string]any { return map[string]any{"field": field, "outputName": name} }

func TestIndividualFields(t *testing.T) {
	n := New()
	d := n.Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	items := []map[string]any{{"id": 1.0, "tags": []any{"a"}}, {"id": 2.0}, {"id": 3.0, "tags": []any{"b", "c"}}}
	tests := []struct {
		name   string
		params map[string]any
		want   map[string]any
	}{
		{"collects a field", map[string]any{"fields": []any{f("id", "")}}, map[string]any{"id": []any{1.0, 2.0, 3.0}}},
		{"renames", map[string]any{"fields": []any{f("id", "ids")}}, map[string]any{"ids": []any{1.0, 2.0, 3.0}}},
		{"skips missing", map[string]any{"fields": []any{f("tags", "")}}, map[string]any{"tags": []any{[]any{"a"}, []any{"b", "c"}}}},
		{"keeps missing as null", map[string]any{"keepMissing": true, "fields": []any{f("tags", "")}}, map[string]any{"tags": []any{[]any{"a"}, nil, []any{"b", "c"}}}},
		{"merges lists", map[string]any{"mergeLists": true, "fields": []any{f("tags", "")}}, map[string]any{"tags": []any{"a", "b", "c"}}},
		{"several fields", map[string]any{"fields": []any{f("id", "ids"), f("tags", "t")}}, map[string]any{"ids": []any{1.0, 2.0, 3.0}, "t": []any{[]any{"a"}, []any{"b", "c"}}}},
		{"field absent everywhere", map[string]any{"fields": []any{f("zzz", "")}}, map[string]any{"zzz": []any{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := nodetest.Run(t, n, nodetest.New(d, nodetest.Items(items...), tt.params))
			if len(out.Ports[0]) != 1 {
				t.Fatalf("want exactly one item, got %d", len(out.Ports[0]))
			}
			if got := out.Ports[0][0].JSON; !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v\nwant %v", got, tt.want)
			}
			if got := len(out.Ports[0][0].PairedItem); got != 3 {
				t.Fatalf("paired with %d inputs, want 3", got)
			}
		})
	}
}

func TestAllItemData(t *testing.T) {
	n := New()
	d := n.Describe()
	items := []map[string]any{{"id": 1.0, "secret": "x", "u": map[string]any{"n": "a", "k": 1.0}}, {"id": 2.0}}
	tests := []struct {
		name   string
		params map[string]any
		want   map[string]any
	}{
		{"everything into data", map[string]any{"mode": "allItemData"},
			map[string]any{"data": []any{items[0], items[1]}}},
		{"custom destination", map[string]any{"mode": "allItemData", "destinationField": "rows", "include": "all"},
			map[string]any{"rows": []any{items[0], items[1]}}},
		{"selected fields", map[string]any{"mode": "allItemData", "include": "selected", "fieldList": "id, u.n"},
			map[string]any{"data": []any{map[string]any{"id": 1.0, "u": map[string]any{"n": "a"}}, map[string]any{"id": 2.0}}}},
		{"except fields", map[string]any{"mode": "allItemData", "include": "except", "fieldList": "secret, u.k"},
			map[string]any{"data": []any{map[string]any{"id": 1.0, "u": map[string]any{"n": "a"}}, map[string]any{"id": 2.0}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := nodetest.Run(t, n, nodetest.New(d, nodetest.Items(items...), tt.params))
			if got := out.Ports[0][0].JSON; !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v\nwant %v", got, tt.want)
			}
		})
	}
	t.Run("input not mutated by except", func(t *testing.T) {
		src := nodetest.Items(map[string]any{"a": 1.0, "b": 2.0})
		nodetest.Run(t, n, nodetest.New(d, src, map[string]any{"mode": "allItemData", "include": "except", "fieldList": "a"}))
		if _, ok := src[0].JSON["a"]; !ok {
			t.Fatal("aggregate mutated its input")
		}
	})
}

func TestErrors(t *testing.T) {
	n := New()
	items := nodetest.Items(map[string]any{"id": 1.0})
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"no fields", map[string]any{"fields": []any{}}, "no field"},
		{"field entry wrong type", map[string]any{"fields": []any{"id"}}, "must be an object"},
		{"field without name", map[string]any{"fields": []any{f("", "x")}}, "field is required"},
		{"unknown mode", map[string]any{"mode": "x"}, "unknown mode"},
		{"empty destination", map[string]any{"mode": "allItemData", "destinationField": ""}, "must not be empty"},
		{"unknown include", map[string]any{"mode": "allItemData", "include": "some"}, "unknown include"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := n.Execute(context.Background(), nodetest.New(n.Describe(), items, tt.params))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
