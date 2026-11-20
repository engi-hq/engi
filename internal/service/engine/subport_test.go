package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// supplyNode is a test sub-node: a descriptor plus a Supply function. Execute
// fails, so any test that schedules a sub-node as a step breaks.
type supplyNode struct {
	desc   nodesdk.Descriptor
	supply func(ctx context.Context, in nodesdk.Input) (any, error)
}

func (n supplyNode) Describe() nodesdk.Descriptor { return n.desc }
func (n supplyNode) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, errors.New("a sub-node must never run as a step")
}
func (n supplyNode) Supply(ctx context.Context, in nodesdk.Input) (any, error) {
	return n.supply(ctx, in)
}

func subPort(kind nodesdk.PortKind, required bool, maxConns int) nodesdk.PortSpec {
	return nodesdk.PortSpec{Name: string(kind), Kind: kind, Required: required, MaxConnections: maxConns}
}

func subDesc(typ string, inputs []nodesdk.PortSpec, kind nodesdk.PortKind) nodesdk.Descriptor {
	d := desc(typ, inputs, []nodesdk.PortSpec{subPort(kind, false, 0)}, "")
	d.Properties = []nodesdk.Property{{Name: "name", Type: nodesdk.PropString}, {Name: "fail", Type: nodesdk.PropBoolean}}
	return d
}

// supplyName returns the sub-node's "name" parameter, or fails when "fail" is set.
func supplyName(ctx context.Context, in nodesdk.Input) (any, error) {
	if fail, err := nodesdk.ParamBool(ctx, in, "fail", 0); err != nil || fail {
		if err != nil {
			return nil, err
		}
		return nil, &nodesdk.NodeError{Message: "boom", Description: "supply failed", HTTPStatus: 429}
	}
	return nodesdk.ParamString(ctx, in, "name", 0)
}

// pullParent pulls both sub-ports per item and emits what it got.
func pullParent(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
	var out []nodesdk.Item
	for i, it := range in.Items(0) {
		js := map[string]any{"n": it.JSON["n"]}
		for _, k := range []nodesdk.PortKind{nodesdk.PortAIModel, nodesdk.PortAITool} {
			got, err := in.SubPort(ctx, k, i)
			if err != nil {
				return nodesdk.Output{}, err
			}
			var vals []any
			for _, s := range got {
				vals = append(vals, fmt.Sprintf("%s=%v", s.Node, s.Value))
			}
			js[string(k)] = vals
		}
		out = append(out, nodesdk.Item{JSON: js})
	}
	return nodesdk.Output{Ports: [][]nodesdk.Item{out}}, nil
}

func registerSubPortFakes(reg *registry.Registry) error {
	parentIn := []nodesdk.PortSpec{port("main"), subPort(nodesdk.PortAIModel, true, 1), subPort(nodesdk.PortAITool, false, 0)}
	optIn := []nodesdk.PortSpec{port("main"), subPort(nodesdk.PortAITool, false, 0)}
	nodes := []nodesdk.Node{
		funcNode{desc("test.parent", parentIn, one(), ""), pullParent},
		funcNode{desc("test.parentOpt", optIn, one(), ""), pullParent},
		funcNode{desc("test.badParent", optIn, one(), ""), func(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
			_, err := in.SubPort(ctx, nodesdk.PortAIMemory, 0)
			return nodesdk.Output{}, err
		}},
		supplyNode{subDesc("test.model", nil, nodesdk.PortAIModel), supplyName},
		supplyNode{subDesc("test.tool", nil, nodesdk.PortAITool), supplyName},
		// A tool that itself pulls a model: sub-nodes can have sub-ports.
		supplyNode{subDesc("test.toolWithModel", []nodesdk.PortSpec{subPort(nodesdk.PortAIModel, true, 1)}, nodesdk.PortAITool),
			func(ctx context.Context, in nodesdk.Input) (any, error) {
				name, err := nodesdk.ParamString(ctx, in, "name", 0)
				if err != nil {
					return nil, err
				}
				m, err := in.SubPort(ctx, nodesdk.PortAIModel, 0)
				if err != nil {
					return nil, err
				}
				return fmt.Sprintf("%s(%v)", name, m[0].Value), nil
			}},
		supplyNode{subDesc("test.toolWrap", []nodesdk.PortSpec{subPort(nodesdk.PortAITool, false, 0)}, nodesdk.PortAITool), supplyName},
	}
	for _, n := range nodes {
		if err := reg.Register(func() nodesdk.Node { return n }); err != nil {
			return fmt.Errorf("registering %s: %w", n.Describe().Type, err)
		}
	}
	return nil
}

func sub(from, to string, kind nodesdk.PortKind) domain.Connection {
	return domain.Connection{From: domain.Endpoint{Node: from, Port: domain.PortKind(kind)}, To: domain.Endpoint{Node: to, Port: domain.PortKind(kind)}}
}

func named(name, typ string, y float64, params map[string]any) domain.Node {
	n := nd(name, typ, 0, domain.NodeSettings{}, params)
	n.Position.Y = y
	return n
}

func baseSubWF(extra []domain.Node, conns ...domain.Connection) domain.Workflow {
	nodes := append([]domain.Node{
		nd("Start", "test.trigger", 0, domain.NodeSettings{}, nil),
		nd("Parent", "test.parent", 100, domain.NodeSettings{}, nil),
	}, extra...)
	return wfOf(nodes, append([]domain.Connection{link("Start", "Parent")}, conns...)...)
}

func TestSubPortSupply(t *testing.T) {
	wf := baseSubWF([]domain.Node{
		named("M", "test.model", 200, map[string]any{"name": "gpt"}),
		named("T2", "test.tool", 210, map[string]any{"name": "second"}),
		// Resolved against the parent's item, not a fixed one.
		named("T1", "test.tool", 220, map[string]any{"name": "=item{{ $json.n }}"}),
	}, sub("M", "Parent", nodesdk.PortAIModel), sub("T2", "Parent", nodesdk.PortAITool), sub("T1", "Parent", nodesdk.PortAITool))
	st := runWF(t, wf, nItems(2)...)
	if st.Status != domain.StatusSuccess {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
	got := outJSON(st, "Parent", 0)
	if len(got) != 2 {
		t.Fatalf("items=%v", got)
	}
	for i, want := range []string{"T1=item1", "T1=item2"} {
		tools := got[i]["ai.tool"].([]any)
		if len(tools) != 2 || tools[0] != "T2=second" || tools[1] != want {
			t.Errorf("item %d tools (connection order, per-item params) = %v", i, tools)
		}
		if m := got[i]["ai.model"].([]any); len(m) != 1 || m[0] != "M=gpt" {
			t.Errorf("item %d model = %v", i, m)
		}
	}
	for _, name := range []string{"M", "T1", "T2"} {
		if runs := st.RunData[name]; len(runs) != 0 {
			t.Errorf("sub-node %s must not appear in run data, got %d runs", name, len(runs))
		}
	}
	if len(st.Order) != 2 {
		t.Errorf("only Start and Parent run as steps, order=%v", st.Order)
	}
}

func TestSubPortConnectionOrder(t *testing.T) {
	nodes := []domain.Node{
		named("M", "test.model", 200, map[string]any{"name": "m"}),
		named("A", "test.tool", 210, map[string]any{"name": "a"}),
		named("B", "test.tool", 220, map[string]any{"name": "b"}),
	}
	for _, order := range [][]string{{"A", "B"}, {"B", "A"}} {
		conns := []domain.Connection{sub("M", "Parent", nodesdk.PortAIModel)}
		for _, n := range order {
			conns = append(conns, sub(n, "Parent", nodesdk.PortAITool))
		}
		got := outJSON(runWF(t, baseSubWF(nodes, conns...)), "Parent", 0)[0]["ai.tool"].([]any)
		if got[0] != order[0]+"="+strings.ToLower(order[0]) || got[1] != order[1]+"="+strings.ToLower(order[1]) {
			t.Errorf("order %v gave %v", order, got)
		}
	}
}

func TestSubPortDisabledSkipped(t *testing.T) {
	off := named("Off", "test.tool", 210, map[string]any{"name": "off"})
	off.Disabled = true
	wf := baseSubWF([]domain.Node{named("M", "test.model", 200, map[string]any{"name": "m"}), off},
		sub("M", "Parent", nodesdk.PortAIModel), sub("Off", "Parent", nodesdk.PortAITool))
	st := runWF(t, wf)
	if tools := outJSON(st, "Parent", 0)[0]["ai.tool"].([]any); len(tools) != 0 {
		t.Fatalf("disabled sub-node supplied %v", tools)
	}
}

func TestSubPortRequiredMissing(t *testing.T) {
	t.Run("not connected", func(t *testing.T) {
		st := runWF(t, baseSubWF(nil))
		if st.Status != domain.StatusError || !strings.Contains(st.Error.Message, "ai.model input is required") || st.Error.Node != "Parent" {
			t.Fatalf("status=%s err=%+v", st.Status, st.Error)
		}
		if len(st.RunData["Parent"]) != 1 || st.RunData["Parent"][0].Status != domain.NodeRunError {
			t.Fatalf("the parent run must be recorded as failed: %+v", st.RunData["Parent"])
		}
	})
	t.Run("only disabled", func(t *testing.T) {
		m := named("M", "test.model", 200, map[string]any{"name": "m"})
		m.Disabled = true
		st := runWF(t, baseSubWF([]domain.Node{m}, sub("M", "Parent", nodesdk.PortAIModel)))
		if st.Status != domain.StatusError || !strings.Contains(st.Error.Message, "ai.model input is required") {
			t.Fatalf("status=%s err=%+v", st.Status, st.Error)
		}
	})
	t.Run("saving an unfinished workflow is allowed", func(t *testing.T) {
		e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
		if err := e.Validate(baseSubWF(nil)); err != nil {
			t.Fatalf("Validate must accept a missing required sub-port: %v", err)
		}
	})
}

func TestSubPortGraphErrors(t *testing.T) {
	m := func(name string) domain.Node { return named(name, "test.model", 200, map[string]any{"name": name}) }
	tool := func(name string) domain.Node { return named(name, "test.tool", 210, map[string]any{"name": name}) }
	tests := []struct {
		name    string
		extra   []domain.Node
		conns   []domain.Connection
		wantMsg string
	}{
		{"too many connections", []domain.Node{m("M1"), m("M2")},
			[]domain.Connection{sub("M1", "Parent", nodesdk.PortAIModel), sub("M2", "Parent", nodesdk.PortAIModel)}, "at most 1"},
		{"sub-node has no output of that kind", []domain.Node{tool("T")},
			[]domain.Connection{sub("T", "Parent", nodesdk.PortAIModel)}, "no such output"},
		{"parent has no input of that kind", []domain.Node{tool("T"), nd("Plain", "test.noop", 300, domain.NodeSettings{}, nil)},
			[]domain.Connection{sub("T", "Plain", nodesdk.PortAITool), link("Parent", "Plain")}, "does not accept ai.tool"},
		{"main connection into a sub-node", []domain.Node{tool("T")},
			[]domain.Connection{link("Start", "T")}, "connection uses input 0 but the node has 0"},
		{"main connection out of a sub-node", []domain.Node{tool("T")},
			[]domain.Connection{link("T", "Parent")}, "connection uses output 0 but the node has 0"},
		{"unknown sub-port kind", []domain.Node{tool("T")},
			[]domain.Connection{{From: domain.Endpoint{Node: "T", Port: "weird"}, To: domain.Endpoint{Node: "Parent", Port: "weird"}}}, "unknown port kind"},
		{"cycle", []domain.Node{named("A", "test.toolWrap", 200, nil), named("B", "test.toolWrap", 210, nil)},
			[]domain.Connection{sub("A", "B", nodesdk.PortAITool), sub("B", "A", nodesdk.PortAITool)}, "form a cycle"},
	}
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := e.Validate(baseSubWF(tt.extra, tt.conns...))
			var we *domain.WorkflowError
			if !errors.As(err, &we) || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("want WorkflowError containing %q, got %v", tt.wantMsg, err)
			}
		})
	}
}

func TestSubPortSupplyErrorSurfacesOnParent(t *testing.T) {
	wf := baseSubWF([]domain.Node{
		named("M", "test.model", 200, map[string]any{"name": "m"}),
		named("Bad", "test.tool", 210, map[string]any{"fail": true}),
	}, sub("M", "Parent", nodesdk.PortAIModel), sub("Bad", "Parent", nodesdk.PortAITool))
	st := runWF(t, wf)
	if st.Status != domain.StatusError || st.Error.Kind != domain.ErrorKindNode || st.Error.Node != "Parent" {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
	if m := st.Error.Message; !strings.Contains(m, `sub-node "Bad"`) || !strings.Contains(m, "test.tool") || !strings.Contains(m, "boom") {
		t.Errorf("message must name the sub-node and keep the cause: %q", m)
	}
	if st.Error.Description != "supply failed" || st.Error.HTTPStatus != 429 {
		t.Errorf("details of the sub-node error are lost: %+v", st.Error)
	}
	if len(st.RunData["Bad"]) != 0 {
		t.Error("a failing sub-node must not get run data")
	}
}

func TestSubPortParentErrorHandlingApplies(t *testing.T) {
	// onError=continue on the parent turns a sub-node failure into an error item.
	wf := baseSubWF([]domain.Node{
		named("M", "test.model", 200, map[string]any{"fail": true}),
	}, sub("M", "Parent", nodesdk.PortAIModel))
	wf.Nodes[1].Settings.OnError = domain.OnErrorContinue
	st := runWF(t, wf)
	if st.Status != domain.StatusSuccess || !strings.Contains(fmt.Sprint(outJSON(st, "Parent", 0)[0]["error"]), `sub-node "M"`) {
		t.Fatalf("status=%s out=%v", st.Status, outJSON(st, "Parent", 0))
	}
}

func TestSubPortUndeclaredKind(t *testing.T) {
	wf := wfOf([]domain.Node{
		nd("Start", "test.trigger", 0, domain.NodeSettings{}, nil),
		nd("P", "test.badParent", 100, domain.NodeSettings{}, nil),
	}, link("Start", "P"))
	st := runWF(t, wf)
	if st.Status != domain.StatusError || !strings.Contains(st.Error.Message, `no "ai.memory" sub-port input`) {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
}

func TestSubNodeFeedsTwoParents(t *testing.T) {
	wf := wfOf([]domain.Node{
		nd("Start", "test.trigger", 0, domain.NodeSettings{}, nil),
		nd("P1", "test.parent", 100, domain.NodeSettings{}, nil),
		nd("P2", "test.parent", 200, domain.NodeSettings{}, nil),
		named("M", "test.model", 300, map[string]any{"name": "shared"}),
	}, link("Start", "P1"), link("P1", "P2"), sub("M", "P1", nodesdk.PortAIModel), sub("M", "P2", nodesdk.PortAIModel))
	st := runWF(t, wf)
	if st.Status != domain.StatusSuccess {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
	for _, p := range []string{"P1", "P2"} {
		if m := outJSON(st, p, 0)[0]["ai.model"].([]any); len(m) != 1 || m[0] != "M=shared" {
			t.Errorf("%s model = %v", p, m)
		}
	}
}

func TestSubNodeWithSubPorts(t *testing.T) {
	wf := baseSubWF([]domain.Node{
		named("M", "test.model", 200, map[string]any{"name": "m"}),
		named("TM", "test.toolWithModel", 210, map[string]any{"name": "search"}),
		named("M2", "test.model", 220, map[string]any{"name": "inner"}),
	}, sub("M", "Parent", nodesdk.PortAIModel), sub("TM", "Parent", nodesdk.PortAITool), sub("M2", "TM", nodesdk.PortAIModel))
	st := runWF(t, wf)
	if st.Status != domain.StatusSuccess {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
	if tools := outJSON(st, "Parent", 0)[0]["ai.tool"].([]any); len(tools) != 1 || tools[0] != "TM=search(inner)" {
		t.Fatalf("tools=%v", tools)
	}
}

func TestSubNodeCannotBeStartOrDestination(t *testing.T) {
	wf := baseSubWF([]domain.Node{named("M", "test.model", 200, map[string]any{"name": "m"})}, sub("M", "Parent", nodesdk.PortAIModel))
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)

	req := request(wf)
	req.StartNode = "M"
	if _, err := e.Run(context.Background(), req); err == nil || !strings.Contains(err.Error(), "cannot be a start node") {
		t.Errorf("start: %v", err)
	}
	req = request(wf)
	req.Destination = &engine.Destination{Node: "M"}
	if _, err := e.Run(context.Background(), req); err == nil || !strings.Contains(err.Error(), "cannot be a destination") {
		t.Errorf("destination: %v", err)
	}
}

func TestPartialRunToParentStillSupplies(t *testing.T) {
	wf := baseSubWF([]domain.Node{named("M", "test.model", 200, map[string]any{"name": "m"})}, sub("M", "Parent", nodesdk.PortAIModel))
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	req := request(wf)
	req.Destination = &engine.Destination{Node: "Parent"}
	st, err := e.Run(context.Background(), req)
	if err != nil || st.Status != domain.StatusSuccess || outJSON(st, "Parent", 0)[0]["ai.model"].([]any)[0] != "M=m" {
		t.Fatalf("err=%v st=%+v", err, st)
	}
}
