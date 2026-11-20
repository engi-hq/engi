package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
)

// Behaviour tests for engine edge cases (pairing, empty outputs, lineage):
// see docs/04-engine.md.

func runWF(t *testing.T, wf domain.Workflow, start ...domain.Item) *engine.State {
	t.Helper()
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	req := request(wf)
	req.StartItems = start
	st, err := e.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func nItems(n int) []domain.Item {
	items := make([]domain.Item, n)
	for i := range items {
		items[i] = domain.Item{JSON: map[string]any{"n": float64(i + 1)}}
	}
	return items
}

func outJSON(st *engine.State, node string, port int) []map[string]any {
	var out []map[string]any
	ports := st.RunData[node][0].Output[domain.PortMain]
	if port >= len(ports) {
		return nil
	}
	for _, it := range ports[port] {
		out = append(out, it.JSON)
	}
	return out
}

func TestAlwaysOutputDataLooksOnlyAtFirstOutput(t *testing.T) {
	wf := wfOf([]domain.Node{
		nd("Start", "test.trigger", 0, domain.NodeSettings{}, nil),
		nd("If", "test.if", 100, domain.NodeSettings{AlwaysOutputData: true}, map[string]any{"condition": false}),
	}, link("Start", "If"))
	st := runWF(t, wf, nItems(2)...)
	ports := st.RunData["If"][0].Output[domain.PortMain]
	if len(ports[0]) != 1 || len(ports[0][0].JSON) != 0 {
		t.Fatalf("output 0 must get one empty item, got %+v", ports[0])
	}
	if len(ports[1]) != 2 {
		t.Fatalf("output 1 must keep its items, got %+v", ports[1])
	}
	if got := ports[0][0].PairedItem; len(got) != 2 {
		t.Fatalf("the empty item must pair to every input item, got %v", got)
	}
}

func TestContinueErrorItemCarriesMessageInJSON(t *testing.T) {
	wf := simple("test.fail", domain.NodeSettings{OnError: domain.OnErrorContinue}, map[string]any{"message": "boom"})
	st := runWF(t, wf, nItems(2)...)
	got := outJSON(st, "N", 0)
	if len(got) != 2 || got[0]["error"] != "boom" || len(got[0]) != 1 {
		t.Fatalf("continue items = %v", got)
	}
}

func TestErrorOutputItemsKeepInputData(t *testing.T) {
	wf := simple("test.fail", domain.NodeSettings{OnError: domain.OnErrorErrorOutput}, map[string]any{"message": "boom"})
	st := runWF(t, wf, nItems(2)...)
	got := outJSON(st, "N", 1)
	if len(got) != 2 || got[1]["n"] != float64(2) || got[1]["error"] != "boom" {
		t.Fatalf("error output items = %v", got)
	}
}

func TestErrorOutputRoutesItemLevelErrors(t *testing.T) {
	wf := simple("test.itemError", domain.NodeSettings{OnError: domain.OnErrorErrorOutput}, nil)
	st := runWF(t, wf, nItems(3)...)
	ok, bad := outJSON(st, "N", 0), outJSON(st, "N", 1)
	if len(ok) != 2 || len(bad) != 1 || bad[0]["n"] != float64(2) || bad[0]["error"] != "bad item" {
		t.Fatalf("main=%v error=%v", ok, bad)
	}
	// Without error output the failing item stays on the main output.
	st = runWF(t, simple("test.itemError", domain.NodeSettings{OnError: domain.OnErrorContinue}, nil), nItems(3)...)
	if len(outJSON(st, "N", 0)) != 3 {
		t.Fatalf("continue must keep all items on output 0")
	}
}

func lineageWF(middle string, middleSettings domain.NodeSettings) domain.Workflow {
	return wfOf([]domain.Node{
		nd("Start", "test.trigger", 0, domain.NodeSettings{}, nil),
		nd("Drop", "test.dropFirst", 100, domain.NodeSettings{}, nil),
		nd("Mid", middle, 200, middleSettings, nil),
		nd("Join", "test.set", 300, domain.NodeSettings{}, map[string]any{"values": map[string]any{
			"origin": `={{ $("Start").item.json.n }}`,
		}}),
	}, link("Start", "Drop"), link("Drop", "Mid"), link("Mid", "Join"))
}

func TestPairedItemSurvivesPassThroughNodes(t *testing.T) {
	for name, mid := range map[string]struct {
		typ      string
		settings domain.NodeSettings
	}{
		"disabled node": {"test.noop", domain.NodeSettings{}},
		"echoing node":  {"test.noop", domain.NodeSettings{}},
	} {
		t.Run(name, func(t *testing.T) {
			wf := lineageWF(mid.typ, mid.settings)
			if name == "disabled node" {
				wf.Nodes[2].Disabled = true
			}
			st := runWF(t, wf, nItems(3)...)
			if st.Status != domain.StatusSuccess {
				t.Fatalf("status=%s err=%+v", st.Status, st.Error)
			}
			got := outJSON(st, "Join", 0)
			if len(got) != 2 || got[0]["origin"] != float64(2) || got[1]["origin"] != float64(3) {
				t.Fatalf("origins = %v", got)
			}
		})
	}
}

func joinWF(middle string) domain.Workflow {
	return wfOf([]domain.Node{
		nd("Start", "test.trigger", 0, domain.NodeSettings{}, nil),
		nd("Mid", middle, 100, domain.NodeSettings{}, nil),
		nd("Join", "test.set", 200, domain.NodeSettings{}, map[string]any{"values": map[string]any{
			"origin": `={{ $("Start").item.json.n }}`,
		}}),
	}, link("Start", "Mid"), link("Mid", "Join"))
}

func TestPairedItemManyToOneIsAmbiguous(t *testing.T) {
	st := runWF(t, joinWF("test.aggregate"), nItems(2)...)
	if st.Status != domain.StatusError || !strings.Contains(st.Error.Message, "multiple") {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
	// All paths lead to the same ancestor item, so the lookup is unambiguous.
	st = runWF(t, joinWF("test.aggregate"), nItems(1)...)
	if st.Status != domain.StatusSuccess {
		t.Fatalf("single input: status=%s err=%+v", st.Status, st.Error)
	}
}

func TestPairedItemNotAnAncestorMentionsTheNode(t *testing.T) {
	wf := joinWF("test.noop")
	wf.Nodes = append(wf.Nodes, nd("Island", "test.noop", 0, domain.NodeSettings{}, nil))
	wf.Nodes[2].Parameters = map[string]any{"values": map[string]any{"x": `={{ $("Island").item.json.n }}`}}
	st := runWF(t, wf, nItems(1)...)
	if st.Status != domain.StatusError || !strings.Contains(st.Error.Message, "Island") {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
}

func TestAutoPairingManyInputsOneOutput(t *testing.T) {
	st := runWF(t, joinWF("test.count"), nItems(3)...)
	if st.Status != domain.StatusSuccess {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
	if got := outJSON(st, "Join", 0); len(got) != 1 || got[0]["origin"] != float64(1) {
		t.Fatalf("join = %v", got)
	}
}

func TestPinnedItemsGetPairing(t *testing.T) {
	wf := joinWF("test.noop")
	wf.PinData = map[string][]domain.Item{"Mid": {{JSON: map[string]any{"p": true}}}}
	st := runWF(t, wf, nItems(1)...)
	if got := outJSON(st, "Join", 0); st.Status != domain.StatusSuccess || got[0]["origin"] != float64(1) {
		t.Fatalf("status=%s err=%+v join=%v", st.Status, st.Error, got)
	}
}

func TestFanOutRunsBranchToCompletionTopFirst(t *testing.T) {
	wf := wfOf([]domain.Node{
		nd("Start", "test.trigger", 0, domain.NodeSettings{}, nil),
		{Name: "Bottom", Type: "test.noop", TypeVersion: 1, Position: domain.Position{X: 100, Y: 200}},
		{Name: "Top", Type: "test.noop", TypeVersion: 1, Position: domain.Position{X: 300, Y: 0}},
		{Name: "TopNext", Type: "test.noop", TypeVersion: 1, Position: domain.Position{X: 400, Y: 0}},
	}, link("Start", "Bottom"), link("Start", "Top"), link("Top", "TopNext"))
	st := runWF(t, wf, nItems(1)...)
	var order []string
	for _, r := range st.Order {
		order = append(order, r.Node)
	}
	if got, want := fmt.Sprint(order), "[Start Top TopNext Bottom]"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
}

func TestStalePairedItemReferencesAreReplaced(t *testing.T) {
	st := runWF(t, joinWF("test.stale"), nItems(2)...)
	got := outJSON(st, "Join", 0)
	if st.Status != domain.StatusSuccess || len(got) != 2 || got[0]["origin"] != float64(1) || got[1]["origin"] != float64(2) {
		t.Fatalf("status=%s err=%+v join=%v", st.Status, st.Error, got)
	}
}

func TestPinnedItemsDropTheirOwnPairedItem(t *testing.T) {
	wf := joinWF("test.noop")
	wf.PinData = map[string][]domain.Item{"Mid": {{
		JSON:       map[string]any{"p": true},
		PairedItem: []domain.ItemRef{{Input: 0, Item: 7}},
	}}}
	st := runWF(t, wf, nItems(1)...)
	if got := outJSON(st, "Join", 0); st.Status != domain.StatusSuccess || got[0]["origin"] != float64(1) {
		t.Fatalf("status=%s err=%+v join=%v", st.Status, st.Error, got)
	}
}
