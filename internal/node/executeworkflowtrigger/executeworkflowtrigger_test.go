package executeworkflowtrigger

import (
	"reflect"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
)

func TestForwardsStartItems(t *testing.T) {
	n := New()
	if err := n.Describe().Validate(); err != nil {
		t.Fatal(err)
	}
	items := nodetest.Items(map[string]any{"a": 1.0}, map[string]any{"b": 2.0})
	out := nodetest.Run(t, n, nodetest.New(n.Describe(), items, nil))
	if got := nodetest.JSONs(out.Ports[0]); !reflect.DeepEqual(got, []map[string]any{{"a": 1.0}, {"b": 2.0}}) {
		t.Fatalf("got %v", got)
	}
	if out.Ports[0][1].PairedItem[0].Item != 1 {
		t.Fatalf("pairing = %+v", out.Ports[0][1].PairedItem)
	}
}
