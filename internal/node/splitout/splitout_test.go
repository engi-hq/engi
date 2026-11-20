package splitout

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
)

func TestExecute(t *testing.T) {
	n := New()
	d := n.Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		items  []map[string]any
		params map[string]any
		want   []map[string]any
	}{
		{"object elements are spread", []map[string]any{{"id": 1.0, "rows": []any{map[string]any{"a": 1.0}, map[string]any{"a": 2.0}}}},
			map[string]any{"fields": "rows"},
			[]map[string]any{{"a": 1.0}, {"a": 2.0}}},
		{"scalar elements go under the field name", []map[string]any{{"tags": []any{"x", "y"}}},
			map[string]any{"fields": "tags"},
			[]map[string]any{{"tags": "x"}, {"tags": "y"}}},
		{"destination field", []map[string]any{{"tags": []any{"x", "y"}}},
			map[string]any{"fields": "tags", "destinationField": "tag"},
			[]map[string]any{{"tag": "x"}, {"tag": "y"}}},
		{"destination field keeps object elements whole", []map[string]any{{"rows": []any{map[string]any{"a": 1.0}}}},
			map[string]any{"fields": "rows", "destinationField": "row"},
			[]map[string]any{{"row": map[string]any{"a": 1.0}}}},
		{"include all other fields", []map[string]any{{"id": 7.0, "tags": []any{"x", "y"}, "n": "k"}},
			map[string]any{"fields": "tags", "include": "all"},
			[]map[string]any{{"id": 7.0, "n": "k", "tags": "x"}, {"id": 7.0, "n": "k", "tags": "y"}}},
		{"include selected fields", []map[string]any{{"id": 7.0, "tags": []any{"x"}, "n": "k"}},
			map[string]any{"fields": "tags", "include": "selected", "fieldsToInclude": "id, missing"},
			[]map[string]any{{"id": 7.0, "tags": "x"}}},
		{"nested field path stores under the last key", []map[string]any{{"data": map[string]any{"list": []any{1.0, 2.0}}}},
			map[string]any{"fields": "data.list"},
			[]map[string]any{{"list": 1.0}, {"list": 2.0}}},
		{"several fields zip by position", []map[string]any{{"a": []any{1.0, 2.0}, "b": []any{"x", "y", "z"}}},
			map[string]any{"fields": "a, b"},
			[]map[string]any{{"a": 1.0, "b": "x"}, {"a": 2.0, "b": "y"}, {"b": "z"}}},
		{"non-array counts as one element", []map[string]any{{"v": "solo"}},
			map[string]any{"fields": "v"},
			[]map[string]any{{"v": "solo"}}},
		{"empty array yields nothing", []map[string]any{{"v": []any{}, "id": 1.0}},
			map[string]any{"fields": "v", "include": "all"},
			nil},
		{"null yields nothing", []map[string]any{{"v": nil}}, map[string]any{"fields": "v"}, nil},
		{"several items", []map[string]any{{"v": []any{1.0}}, {"v": []any{2.0, 3.0}}},
			map[string]any{"fields": "v"},
			[]map[string]any{{"v": 1.0}, {"v": 2.0}, {"v": 3.0}}},
		{"fields as an array param", []map[string]any{{"v": []any{1.0}}},
			map[string]any{"fields": []any{"v"}},
			[]map[string]any{{"v": 1.0}}},
		{"empty input", nil, map[string]any{"fields": "v"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := nodetest.Run(t, n, nodetest.New(d, nodetest.Items(tt.items...), tt.params))
			got := nodetest.JSONs(out.Ports[0])
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestPairing(t *testing.T) {
	n := New()
	items := nodetest.Items(map[string]any{"v": []any{1.0}}, map[string]any{"v": []any{2.0, 3.0}})
	out := nodetest.Run(t, n, nodetest.New(n.Describe(), items, map[string]any{"fields": "v"}))
	if len(out.Ports[0]) != 3 || out.Ports[0][2].PairedItem[0].Item != 1 {
		t.Fatalf("got %+v", out.Ports[0])
	}
}

func TestErrors(t *testing.T) {
	n := New()
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"no field", map[string]any{"fields": ""}, "no field"},
		{"missing field", map[string]any{"fields": "nope"}, "does not exist"},
		{"bad include", map[string]any{"fields": "v", "include": "some"}, "unknown include"},
		{"bad list entry", map[string]any{"fields": []any{1.0}}, "strings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := n.Execute(context.Background(), nodetest.New(n.Describe(), nodetest.Items(map[string]any{"v": []any{1.0}}), tt.params))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
