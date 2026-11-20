package manualtrigger

import (
	"reflect"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
)

func TestExecute(t *testing.T) {
	n := New()
	if err := n.Describe().Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		items []map[string]any
	}{
		{"forwards start items", []map[string]any{{"a": 1.0}, {"b": 2.0}}},
		{"empty input", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := nodetest.Run(t, n, nodetest.New(n.Describe(), nodetest.Items(tt.items...), nil))
			if len(out.Ports) != 1 || len(out.Ports[0]) != len(tt.items) {
				t.Fatalf("ports = %v", out.Ports)
			}
			if len(tt.items) > 0 && !reflect.DeepEqual(nodetest.JSONs(out.Ports[0]), tt.items) {
				t.Fatalf("got %v", nodetest.JSONs(out.Ports[0]))
			}
		})
	}
}
