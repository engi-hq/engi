package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/credential/noop"
	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/internal/node/wait"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/expression"
	"github.com/engi-hq/engi/internal/service/servicetest"
	"github.com/engi-hq/engi/internal/service/waiter"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// testResumeBase is the absolute prefix of the wait resume route in these tests.
const testResumeBase = "https://engi.example.com/hook-wait"

// captureType is a test node that records $execution.resumeUrl as the node sees it.
const captureType = "test.capture"

// waitStep is one node of a chain workflow.
type waitStep struct {
	name, typ string
	params    map[string]any
}

// chainWorkflow links the steps one after the other on their main ports.
func chainWorkflow(t *testing.T, steps ...waitStep) domain.Workflow {
	t.Helper()
	doc := map[string]any{"name": "resume", "nodes": []any{}, "connections": []any{}}
	nodes := []any{}
	for i, s := range steps {
		n := map[string]any{"name": s.name, "type": s.typ, "typeVersion": 1, "position": []int{i * 200, 0}}
		if s.params != nil {
			n["parameters"] = s.params
		}
		nodes = append(nodes, n)
		if i > 0 {
			doc["connections"] = append(doc["connections"].([]any), map[string]any{
				"from": map[string]any{"node": steps[i-1].name, "port": "main", "index": 0},
				"to":   map[string]any{"node": s.name, "port": "main", "index": 0},
			})
		}
	}
	doc["nodes"] = nodes
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := jsoncodec.DecodeWorkflow(raw)
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

// newResumeEngine builds an engine with the capture node, the real Wait node and
// the IF node. Every resume URL a capture node sees is appended to seen.
func newResumeEngine(t *testing.T, clock *servicetest.Clock, seen *[]string) *engine.Engine {
	t.Helper()
	capture := funcNode{desc(captureType, one(), one(), ""), func(ctx context.Context, in nodesdk.Input) (nodesdk.Output, error) {
		v, err := in.EvalExpression(ctx, "$execution.resumeUrl", 0)
		if err != nil {
			return nodesdk.Output{}, err
		}
		*seen = append(*seen, fmt.Sprint(v))
		return passThrough(ctx, in)
	}}
	reg := registry.New()
	for _, factory := range []func() nodesdk.Node{
		func() nodesdk.Node { return capture },
		func() nodesdk.Node { return wait.New(clock) },
	} {
		if err := reg.Register(factory); err != nil {
			t.Fatal(err)
		}
	}
	ev, err := expression.New(config.Default().Expression, clock, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(config.Default().Engine, engine.Deps{
		Registry: reg, Evaluator: ev, Clock: clock, Sleeper: noSleeper{}, Credentials: noop.Resolver{},
		ResumeBase: testResumeBase,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// noSleeper satisfies port.Sleeper for tests that never sleep.
type noSleeper struct{}

func (noSleeper) Sleep(context.Context, time.Duration) error { return nil }

// runUntilDone runs the workflow, pausing at every Wait node and resuming it
// through a serialized round trip, as a real resume would. At each pause the
// last resume URL seen must resume exactly the pending wait.
func runUntilDone(t *testing.T, e *engine.Engine, wf domain.Workflow, seen *[]string, dropSecret bool) *engine.State {
	t.Helper()
	ctx := context.Background()
	req := engine.Request{ExecutionID: "ex_01J00000000000000000000000", Workflow: wf, Mode: domain.ModeManual, StartNode: wf.Nodes[0].Name}
	st, err := e.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for st.Status == domain.StatusWaiting {
		if st.Pending == nil || st.Pending.ResumeToken == "" {
			t.Fatalf("waiting without a token: %+v", st.Pending)
		}
		if got, want := (*seen)[len(*seen)-1], testResumeBase+"/"+st.Pending.ResumeToken; got != want {
			t.Fatalf("resume URL = %q, want %q", got, want)
		}
		encoded, err := jsoncodec.EncodeState(st)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := jsoncodec.DecodeState(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if dropSecret {
			restored.ResumeSecret = ""
		}
		req.Resume = restored
		if st, err = e.Run(ctx, req); err != nil {
			t.Fatalf("resume: %v", err)
		}
	}
	return st
}

func TestResumeURLIsStableAcrossWaitNodes(t *testing.T) {
	tests := []struct {
		name     string
		chain    []waitStep
		captures int // capture nodes that run: each one sees the URL
	}{
		{"one wait", []waitStep{
			{"Before", captureType, nil},
			{"Wait", wait.Type, map[string]any{"resume": "webhook"}},
			{"After", captureType, nil},
		}, 2},
		{"two waits", []waitStep{
			{"Before", captureType, nil},
			{"Wait1", wait.Type, map[string]any{"resume": "webhook"}},
			{"Mid", captureType, nil},
			{"Wait2", wait.Type, map[string]any{"resume": "webhook"}},
			{"After", captureType, nil},
		}, 3},
	}
	for _, tt := range tests {
		for _, dropSecret := range []bool{false, true} {
			label := tt.name
			if dropSecret {
				label += ", state saved before resume secrets"
			}
			t.Run(label, func(t *testing.T) {
				var seen []string
				e := newResumeEngine(t, servicetest.NewClock(), &seen)
				st := runUntilDone(t, e, chainWorkflow(t, tt.chain...), &seen, dropSecret)
				if st.Status != domain.StatusSuccess {
					t.Fatalf("status = %s", st.Status)
				}
				if len(seen) != tt.captures {
					t.Fatalf("captures = %d, want %d", len(seen), tt.captures)
				}
				for i, u := range seen {
					if u != seen[0] || !strings.HasPrefix(u, testResumeBase+"/ex_01J00000000000000000000000.") {
						t.Fatalf("capture %d sees %q, first sees %q", i, u, seen[0])
					}
				}
			})
		}
	}
}

func TestResumeURLFromScopeResumesWaitingExecution(t *testing.T) {
	ctx := context.Background()
	clock := servicetest.NewClock()
	var seen []string
	e := newResumeEngine(t, clock, &seen)
	wf := chainWorkflow(t,
		waitStep{"Before", captureType, nil},
		waitStep{"Wait", wait.Type, map[string]any{"resume": "webhook"}},
		waitStep{"After", captureType, nil},
	)
	st, err := e.Run(ctx, engine.Request{ExecutionID: "ex_01J00000000000000000000001", Workflow: wf, Mode: domain.ModeManual, StartNode: "Before"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != domain.StatusWaiting {
		t.Fatalf("status = %s, want waiting", st.Status)
	}

	store := servicetest.NewStore(clock)
	execs := store.Executions()
	queue := servicetest.NewQueue(clock, 3)
	svc, err := waiter.New(config.Default().Worker, waiter.Deps{
		Executions: execs, States: execs, Queue: queue, Tx: store, Clock: clock, IDs: servicetest.NewIDs(clock), Codec: servicetest.Codec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := execs.Create(ctx, domain.Execution{
		ID: st.ExecutionID, WorkflowID: "wf_x", WorkflowVersion: 1, Mode: domain.ModeManual, Status: domain.StatusWaiting,
		StartedAt: clock.Now(), RunData: map[string][]domain.NodeRun{"Wait": {{Status: domain.NodeRunWaiting}}},
	}); err != nil {
		t.Fatal(err)
	}
	blob, err := servicetest.Codec{}.EncodeState(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := execs.SaveState(ctx, st.ExecutionID, blob); err != nil {
		t.Fatal(err)
	}

	url := seen[0]
	token, ok := strings.CutPrefix(url, testResumeBase+"/")
	if !ok {
		t.Fatalf("resume URL %q is not under %s", url, testResumeBase)
	}
	items := []domain.Item{{JSON: map[string]any{"approved": true}}}

	// A wrong secret, for the same execution, is refused as an invalid token.
	flipped := "A"
	if token[len(token)-1:] == flipped {
		flipped = "B"
	}
	wrong := token[:len(token)-1] + flipped
	if err := svc.ResumeByToken(ctx, wrong, items); !errors.Is(err, waiter.ErrInvalidToken) {
		t.Fatalf("wrong secret: err = %v, want ErrInvalidToken", err)
	}
	if ex, _ := execs.Get(ctx, st.ExecutionID); ex.Status != domain.StatusWaiting {
		t.Fatalf("wrong secret changed the status to %s", ex.Status)
	}

	if err := svc.ResumeByToken(ctx, token, items); err != nil {
		t.Fatalf("resume with the URL from the scope: %v", err)
	}
	if ex, _ := execs.Get(ctx, st.ExecutionID); ex.Status != domain.StatusQueued {
		t.Fatalf("status after resume = %s, want queued", ex.Status)
	}
	if jobs := queue.Jobs(); len(jobs) != 1 || jobs[0].ExecutionID != st.ExecutionID {
		t.Fatalf("jobs = %+v", jobs)
	}
}
