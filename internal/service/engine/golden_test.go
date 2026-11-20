package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/engi-hq/engi/internal/adapter/credential/noop"
	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/expression"
)

var update = flag.Bool("update", false, "rewrite golden expected.json files")

// caseOptions is the optional testdata/<case>/case.json.
type caseOptions struct {
	StartNode   string `json:"startNode"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	MaxNodeRuns int    `json:"maxNodeRuns"`
	// Resume runs the execution a second time from its serialized waiting state.
	Resume      bool            `json:"resume"`
	ResumeItems json.RawMessage `json:"resumeItems"`
}

func newEngine(t *testing.T, cfg config.EngineConfig, clock *fakeClock, obs engine.Observer) *engine.Engine {
	t.Helper()
	reg, err := buildRegistry()
	if err != nil {
		t.Fatal(err)
	}
	ev, err := expression.New(config.Default().Expression, clock, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg, engine.Deps{
		Registry: reg, Evaluator: ev, Clock: clock, Sleeper: clock, Credentials: noop.Resolver{}, Observer: obs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestGolden(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no golden cases found: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) { runGolden(t, dir) })
	}
}

func runGolden(t *testing.T, dir string) {
	opts := caseOptions{StartNode: "Start", Mode: string(domain.ModeManual)}
	if raw, err := os.ReadFile(filepath.Join(dir, "case.json")); err == nil {
		if err := json.Unmarshal(raw, &opts); err != nil {
			t.Fatalf("case.json: %v", err)
		}
	}
	if opts.StartNode == "" {
		opts.StartNode = "Start"
	}
	wfRaw := mustRead(t, filepath.Join(dir, "workflow.json"))
	wf, err := jsoncodec.DecodeWorkflow(wfRaw)
	if err != nil {
		t.Fatal(err)
	}
	var input []domain.Item
	if raw, err := os.ReadFile(filepath.Join(dir, "input.json")); err == nil {
		if input, err = jsoncodec.DecodeItems(raw); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default().Engine
	if opts.MaxNodeRuns > 0 {
		cfg.MaxNodeRuns = opts.MaxNodeRuns
	}
	clock := newFakeClock()
	e := newEngine(t, cfg, clock, nil)

	req := engine.Request{
		ExecutionID: "ex_01J00000000000000000000000", Workflow: wf, Mode: domain.ExecutionMode(opts.Mode),
		StartNode: opts.StartNode, StartItems: input,
	}
	if opts.Destination != "" {
		req.Destination = &engine.Destination{Node: opts.Destination}
	}

	var phases []any
	state, err := e.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	phases = append(phases, project(state, clock))

	if opts.Resume {
		if state.Status != domain.StatusWaiting {
			t.Fatalf("status = %s, want waiting before resume", state.Status)
		}
		// Round-trip through the serialized form, as a real resume would.
		encoded, err := jsoncodec.EncodeState(state)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := jsoncodec.DecodeState(encoded)
		if err != nil {
			t.Fatal(err)
		}
		req.Resume = restored
		if opts.ResumeItems != nil {
			if req.ResumeItems, err = jsoncodec.DecodeItems(opts.ResumeItems); err != nil {
				t.Fatal(err)
			}
		}
		state, err = e.Run(context.Background(), req)
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		phases = append(phases, project(state, clock))
	}

	got, err := json.MarshalIndent(map[string]any{"phases": phases}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	expectedPath := filepath.Join(dir, "expected.json")
	if *update {
		if err := os.WriteFile(expectedPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if want := mustRead(t, expectedPath); !bytes.Equal(want, got) {
		t.Fatalf("result differs from %s (run with -update to regenerate and review the diff)\n--- got ---\n%s\n--- want ---\n%s", expectedPath, got, want)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// project reduces a State to the facts worth asserting: order, per-run status,
// attempts, sources and output items. Timestamps and durations are left out.
func project(s *engine.State, clock *fakeClock) map[string]any {
	order := make([]string, len(s.Order))
	for i, o := range s.Order {
		order[i] = o.Node + "#" + itoa(o.RunIndex)
	}
	runData := map[string]any{}
	for node, runs := range s.RunData {
		var rs []any
		for _, r := range runs {
			run := map[string]any{"runIndex": r.RunIndex, "status": r.Status, "output": projectPorts(r.Output[domain.PortMain])}
			if r.Attempts > 1 {
				run["attempts"] = r.Attempts
			}
			if r.Error != nil {
				run["error"] = projectError(r.Error)
			}
			var srcs []string
			for _, src := range r.Sources {
				if src.Node != "" {
					srcs = append(srcs, src.Node+"#"+itoa(src.RunIndex)+"."+itoa(src.OutputIndex))
				}
			}
			if srcs != nil {
				run["from"] = srcs
			}
			rs = append(rs, run)
		}
		runData[node] = rs
	}
	out := map[string]any{"status": s.Status, "order": order, "runData": runData}
	if s.Error != nil {
		out["error"] = projectError(s.Error)
	}
	if s.Pending != nil {
		out["pending"] = map[string]any{"node": s.Pending.Node, "resumeToken": s.Pending.ResumeToken}
	}
	if sl := clock.Sleeps(); len(sl) > 0 {
		ms := make([]int64, len(sl))
		for i, d := range sl {
			ms[i] = d.Milliseconds()
		}
		out["sleepsMs"] = ms
	}
	return out
}

func projectError(e *domain.ErrorInfo) map[string]any {
	m := map[string]any{"kind": e.Kind, "message": e.Message}
	if e.Node != "" {
		m["node"] = e.Node
	}
	if e.HTTPStatus != 0 {
		m["httpStatus"] = e.HTTPStatus
	}
	return m
}

func projectPorts(ports [][]domain.Item) []any {
	out := make([]any, len(ports))
	for i, items := range ports {
		list := make([]any, len(items))
		for j, it := range items {
			m := map[string]any{"json": it.JSON}
			if len(it.PairedItem) > 0 {
				refs := make([]string, len(it.PairedItem))
				for k, p := range it.PairedItem {
					refs[k] = itoa(p.Input) + ":" + itoa(p.Item)
				}
				m["pairedItem"] = refs
			}
			if it.Error != nil {
				m["error"] = projectError(it.Error)
			}
			list[j] = m
		}
		out[i] = list
	}
	return out
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
