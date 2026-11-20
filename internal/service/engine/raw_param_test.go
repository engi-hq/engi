package engine_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/credential/noop"
	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/ifnode"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/expression"
	"github.com/engi-hq/engi/internal/service/servicetest"
)

// TestIFTypedLiteralRoutesByType checks that a literal stored without the "="
// prefix ("12" typed into a number field) is read as a number, so an expression
// result is compared with it. The engine's nodeInput provides the raw parameter
// (nodekit.RawParamSource) that makes this work.
func TestIFTypedLiteralRoutesByType(t *testing.T) {
	ctx := context.Background()
	clock := servicetest.NewClock()
	reg := registry.New()
	if err := reg.Register(ifnode.New); err != nil {
		t.Fatal(err)
	}
	ev, err := expression.New(config.Default().Expression, clock, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(config.Default().Engine, engine.Deps{
		Registry: reg, Evaluator: ev, Clock: clock, Sleeper: noSleeper{}, Credentials: noop.Resolver{},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"name": "if_literal",
		"nodes": []any{map[string]any{
			"name": "Check", "type": ifnode.Type, "typeVersion": 1, "position": []int{0, 0},
			"parameters": map[string]any{"conditions": []any{map[string]any{
				"left": "={{ $json.n }}", "operator": "number:gt", "right": "12",
			}}},
		}},
		"connections": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := jsoncodec.DecodeWorkflow(raw)
	if err != nil {
		t.Fatal(err)
	}
	items := []domain.Item{{JSON: map[string]any{"n": float64(20)}}, {JSON: map[string]any{"n": float64(5)}}}
	st, err := e.Run(ctx, engine.Request{ExecutionID: "ex_01J00000000000000000000002", Workflow: wf, Mode: domain.ModeManual, StartNode: "Check", StartItems: items})
	if err != nil {
		t.Fatal(err)
	}
	runs := st.RunData["Check"]
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	yes, no := runs[0].Output[domain.PortMain][0], runs[0].Output[domain.PortMain][1]
	if len(yes) != 1 || yes[0].JSON["n"] != float64(20) {
		t.Fatalf("true branch = %+v, want the item with n=20", yes)
	}
	if len(no) != 1 || no[0].JSON["n"] != float64(5) {
		t.Fatalf("false branch = %+v, want the item with n=5", no)
	}
}
