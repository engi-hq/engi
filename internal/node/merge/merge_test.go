package merge

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func two(a, b []map[string]any) *nodetest.Input {
	in := nodetest.New(New(2).Describe(), nodetest.Items(a...), nil)
	in.Ports = append(in.Ports, nodetest.Items(b...))
	return in
}

func run(t *testing.T, in *nodetest.Input, params map[string]any) []nodesdk.Item {
	t.Helper()
	in.Params = params
	return nodetest.Run(t, New(2), in).Ports[0]
}

func TestDescriptor(t *testing.T) {
	d := New(3).Describe()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(d.Inputs) != 3 || d.MergePolicy != nodesdk.MergeWaitAll {
		t.Fatalf("inputs=%d policy=%q", len(d.Inputs), d.MergePolicy)
	}
	if len(New(1).Describe().Inputs) != 2 {
		t.Fatal("a Merge node has at least two inputs")
	}
}

func TestAppend(t *testing.T) {
	in := two([]map[string]any{{"a": 1.0}, {"a": 2.0}}, []map[string]any{{"b": 3.0}})
	got := run(t, in, nil)
	if want := []map[string]any{{"a": 1.0}, {"a": 2.0}, {"b": 3.0}}; !reflect.DeepEqual(nodetest.JSONs(got), want) {
		t.Fatalf("got %v", nodetest.JSONs(got))
	}
	if p := got[2].PairedItem[0]; p.Input != 1 || p.Item != 0 {
		t.Fatalf("pairing = %v", got[2].PairedItem)
	}
	if got := run(t, two(nil, nil), nil); len(got) != 0 {
		t.Fatal("append of nothing is nothing")
	}
	if got := run(t, two(nil, []map[string]any{{"x": 1.0}}), nil); len(got) != 1 {
		t.Fatal("append with one empty input")
	}
}

func TestChooseBranch(t *testing.T) {
	in := two([]map[string]any{{"a": 1.0}}, []map[string]any{{"b": 2.0}, {"b": 3.0}})
	if got := run(t, in, map[string]any{"mode": "chooseBranch", "chooseInput": 2.0}); len(got) != 2 || got[0].JSON["b"] != 2.0 {
		t.Fatalf("got %v", got)
	}
	if got := run(t, in, map[string]any{"mode": "chooseBranch"}); len(got) != 1 || got[0].JSON["a"] != 1.0 {
		t.Fatalf("default is input 1, got %v", got)
	}
}

func TestCombineByPosition(t *testing.T) {
	a := []map[string]any{{"id": 1.0, "n": "a1"}, {"id": 2.0, "n": "a2"}, {"id": 3.0, "n": "a3"}}
	b := []map[string]any{{"x": "b1", "n": "b1n"}, {"x": "b2", "n": "b2n"}}
	tests := []struct {
		name   string
		params map[string]any
		want   []map[string]any
	}{
		{"shortest wins, input 2 preferred", map[string]any{"mode": "combineByPosition"},
			[]map[string]any{{"id": 1.0, "n": "b1n", "x": "b1"}, {"id": 2.0, "n": "b2n", "x": "b2"}}},
		{"prefer input 1", map[string]any{"mode": "combineByPosition", "onClash": "preferInput1"},
			[]map[string]any{{"id": 1.0, "n": "a1", "x": "b1"}, {"id": 2.0, "n": "a2", "x": "b2"}}},
		{"suffix on clash", map[string]any{"mode": "combineByPosition", "onClash": "addSuffix"},
			[]map[string]any{{"id": 1.0, "n_1": "a1", "n_2": "b1n", "x": "b1"}, {"id": 2.0, "n_1": "a2", "n_2": "b2n", "x": "b2"}}},
		{"include unpaired", map[string]any{"mode": "combineByPosition", "includeUnpaired": true},
			[]map[string]any{{"id": 1.0, "n": "b1n", "x": "b1"}, {"id": 2.0, "n": "b2n", "x": "b2"}, {"id": 3.0, "n": "a3"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := run(t, two(a, b), tt.params)
			if !reflect.DeepEqual(nodetest.JSONs(got), tt.want) {
				t.Fatalf("got %v\nwant %v", nodetest.JSONs(got), tt.want)
			}
		})
	}
	t.Run("pairs both sources", func(t *testing.T) {
		got := run(t, two(a, b), map[string]any{"mode": "combineByPosition"})
		if !reflect.DeepEqual(got[1].PairedItem, []nodesdk.PairedItem{{Input: 0, Item: 1}, {Input: 1, Item: 1}}) {
			t.Fatalf("pairing = %v", got[1].PairedItem)
		}
	})
	t.Run("three inputs", func(t *testing.T) {
		in := nodetest.New(New(3).Describe(), nodetest.Items(map[string]any{"a": 1.0}), map[string]any{"mode": "combineByPosition"})
		in.Ports = append(in.Ports, nodetest.Items(map[string]any{"b": 2.0}), nodetest.Items(map[string]any{"c": 3.0}))
		out := nodetest.Run(t, New(3), in).Ports[0]
		if want := []map[string]any{{"a": 1.0, "b": 2.0, "c": 3.0}}; !reflect.DeepEqual(nodetest.JSONs(out), want) {
			t.Fatalf("got %v", nodetest.JSONs(out))
		}
	})
	t.Run("one input only", func(t *testing.T) {
		got := run(t, two(a, nil), map[string]any{"mode": "combineByPosition"})
		if len(got) != 3 {
			t.Fatalf("single connected input passes through, got %d", len(got))
		}
	})
}

func TestCombineByFields(t *testing.T) {
	users := []map[string]any{{"id": 1.0, "name": "ann"}, {"id": 2.0, "name": "bob"}, {"id": 3.0, "name": "cy"}, {"name": "noid"}}
	orders := []map[string]any{{"uid": 2.0, "item": "pen"}, {"uid": 2.0, "item": "ink"}, {"uid": 4.0, "item": "cap"}, {"uid": 1.0, "item": "map"}}
	match := []any{map[string]any{"field1": "id", "field2": "uid"}}
	names := func(items []nodesdk.Item) []string {
		var out []string
		for _, it := range items {
			out = append(out, nodekitString(it.JSON["name"])+"/"+nodekitString(it.JSON["item"]))
		}
		return out
	}
	tests := []struct {
		name   string
		params map[string]any
		want   []string
	}{
		{"inner join, all matches", map[string]any{"matchFields": match},
			[]string{"ann/map", "bob/pen", "bob/ink"}},
		{"first match only", map[string]any{"matchFields": match, "multipleMatches": "first"},
			[]string{"ann/map", "bob/pen"}},
		{"non-matches", map[string]any{"matchFields": match, "joinMode": "keepNonMatches"},
			[]string{"cy/", "noid/", "/cap"}},
		{"outer join", map[string]any{"matchFields": match, "joinMode": "keepEverything"},
			[]string{"ann/map", "bob/pen", "bob/ink", "cy/", "noid/", "/cap"}},
		{"enrich input 1", map[string]any{"matchFields": match, "joinMode": "enrichInput1"},
			[]string{"ann/map", "bob/pen", "bob/ink", "cy/", "noid/"}},
		{"enrich input 2", map[string]any{"matchFields": match, "joinMode": "enrichInput2"},
			[]string{"bob/pen", "bob/ink", "/cap", "ann/map"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.params["mode"] = "combineByFields"
			got := names(run(t, two(users, orders), tt.params))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v\nwant %v", got, tt.want)
			}
		})
	}

	t.Run("same field name as string and multi key", func(t *testing.T) {
		a := []map[string]any{{"k": "x", "j": 1.0, "l": "A"}, {"k": "x", "j": 2.0, "l": "B"}}
		b := []map[string]any{{"k": "x", "j": 2.0, "r": "match"}}
		got := run(t, two(a, b), map[string]any{"mode": "combineByFields", "matchFields": []any{"k", "j"}})
		if want := []map[string]any{{"k": "x", "j": 2.0, "l": "B", "r": "match"}}; !reflect.DeepEqual(nodetest.JSONs(got), want) {
			t.Fatalf("got %v", nodetest.JSONs(got))
		}
	})
	t.Run("nested field path and type-sensitive match", func(t *testing.T) {
		a := []map[string]any{{"u": map[string]any{"id": "1"}}}
		b := []map[string]any{{"id": 1.0}}
		got := run(t, two(a, b), map[string]any{"mode": "combineByFields", "matchFields": []any{map[string]any{"field1": "u.id", "field2": "id"}}})
		if len(got) != 0 {
			t.Fatal(`"1" must not match 1`)
		}
	})
	t.Run("suffix on clash", func(t *testing.T) {
		a := []map[string]any{{"id": 1.0, "v": "a"}}
		b := []map[string]any{{"id": 1.0, "v": "b"}}
		got := run(t, two(a, b), map[string]any{"mode": "combineByFields", "matchFields": []any{"id"}, "onClash": "addSuffix"})
		if want := []map[string]any{{"id_1": 1.0, "id_2": 1.0, "v_1": "a", "v_2": "b"}}; !reflect.DeepEqual(nodetest.JSONs(got), want) {
			t.Fatalf("got %v", nodetest.JSONs(got))
		}
	})
	t.Run("empty inputs", func(t *testing.T) {
		if got := run(t, two(nil, nil), map[string]any{"mode": "combineByFields", "matchFields": []any{"id"}}); len(got) != 0 {
			t.Fatal("expected nothing")
		}
	})
}

func nodekitString(v any) string {
	s, _ := v.(string)
	return s
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"unknown mode", map[string]any{"mode": "zip"}, "unknown mode"},
		{"no match fields", map[string]any{"mode": "combineByFields"}, "at least one field"},
		{"match field without name", map[string]any{"mode": "combineByFields", "matchFields": []any{map[string]any{"field1": ""}}}, "field1 is required"},
		{"match field wrong type", map[string]any{"mode": "combineByFields", "matchFields": []any{1.0}}, "must be an object or a string"},
		{"unknown join", map[string]any{"mode": "combineByFields", "matchFields": []any{"id"}, "joinMode": "cross"}, "unknown joinMode"},
		{"choose out of range", map[string]any{"mode": "chooseBranch", "chooseInput": 3.0}, "out of range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := two([]map[string]any{{"id": 1.0}}, []map[string]any{{"id": 1.0}})
			in.Params = tt.params
			_, err := New(2).Execute(context.Background(), in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
