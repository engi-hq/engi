package noop

import (
	"reflect"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

func TestPassThrough(t *testing.T) {
	n := New()
	if err := n.Describe().Validate(); err != nil {
		t.Fatal(err)
	}
	stale := nodesdk.Item{JSON: map[string]any{"a": 1.0}, PairedItem: []nodesdk.PairedItem{{Input: 0, Item: 9}}}
	out := nodetest.Run(t, n, nodetest.New(n.Describe(), []nodesdk.Item{stale}, nil))
	if !reflect.DeepEqual(nodetest.JSONs(out.Ports[0]), []map[string]any{{"a": 1.0}}) {
		t.Fatalf("got %v", out.Ports[0])
	}
	if got := out.Ports[0][0].PairedItem; !reflect.DeepEqual(got, []nodesdk.PairedItem{{Input: 0, Item: 0}}) {
		t.Fatalf("pairing must be reset to this node's input, got %v", got)
	}
	empty := nodetest.Run(t, n, nodetest.New(n.Describe(), nil, nil))
	if len(empty.Ports[0]) != 0 {
		t.Fatal("empty in, empty out")
	}
}
