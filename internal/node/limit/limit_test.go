package limit

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
	items := nodetest.Items(map[string]any{"i": 0.0}, map[string]any{"i": 1.0}, map[string]any{"i": 2.0}, map[string]any{"i": 3.0})
	tests := []struct {
		name   string
		params map[string]any
		want   []float64
	}{
		{"default keeps first one", nil, []float64{0}},
		{"first two", map[string]any{"maxItems": 2.0}, []float64{0, 1}},
		{"last two", map[string]any{"maxItems": 2.0, "keep": "last"}, []float64{2, 3}},
		{"more than available", map[string]any{"maxItems": 10.0}, []float64{0, 1, 2, 3}},
		{"zero", map[string]any{"maxItems": 0.0}, nil},
		{"last more than available", map[string]any{"maxItems": 9.0, "keep": "last"}, []float64{0, 1, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := nodetest.Run(t, n, nodetest.New(d, items, tt.params))
			var got []float64
			for _, it := range out.Ports[0] {
				got = append(got, it.JSON["i"].(float64))
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
	t.Run("pairing for last", func(t *testing.T) {
		out := nodetest.Run(t, n, nodetest.New(d, items, map[string]any{"maxItems": 2.0, "keep": "last"}))
		if out.Ports[0][0].PairedItem[0].Item != 2 {
			t.Fatalf("pairing = %v", out.Ports[0][0].PairedItem)
		}
	})
	t.Run("empty input", func(t *testing.T) {
		out := nodetest.Run(t, n, nodetest.New(d, nil, nil))
		if len(out.Ports[0]) != 0 {
			t.Fatal("expected no output")
		}
	})
}

func TestErrors(t *testing.T) {
	n := New()
	for name, tt := range map[string]struct {
		params map[string]any
		want   string
	}{
		"negative":     {map[string]any{"maxItems": -1.0}, "negative"},
		"fractional":   {map[string]any{"maxItems": 1.5}, "whole number"},
		"unknown keep": {map[string]any{"keep": "middle"}, "unknown keep"},
		"not a number": {map[string]any{"maxItems": "many"}, "must be a number"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := n.Execute(context.Background(), nodetest.New(n.Describe(), nodetest.Items(map[string]any{}), tt.params))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
