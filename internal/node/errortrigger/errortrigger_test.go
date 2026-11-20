package errortrigger

import (
	"reflect"
	"testing"

	"github.com/engi-hq/engi/internal/node/nodetest"
)

func TestForwardsTheFailureItem(t *testing.T) {
	n := New()
	if err := n.Describe().Validate(); err != nil {
		t.Fatal(err)
	}
	failure := map[string]any{
		"execution": map[string]any{"id": "ex_1", "mode": "webhook"},
		"workflow":  map[string]any{"id": "wf_1", "name": "Orders"},
	}
	out := nodetest.Run(t, n, nodetest.New(n.Describe(), nodetest.Items(failure), nil))
	if got := nodetest.JSONs(out.Ports[0]); !reflect.DeepEqual(got, []map[string]any{failure}) {
		t.Fatalf("got %v", got)
	}
	if out.Ports[0][0].PairedItem[0].Item != 0 {
		t.Fatalf("pairing = %+v", out.Ports[0][0].PairedItem)
	}
}
