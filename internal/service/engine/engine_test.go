package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
)

func wfOf(nodes []domain.Node, conns ...domain.Connection) domain.Workflow {
	return domain.Workflow{Name: "t", Nodes: nodes, Connections: conns}
}

func nd(name, typ string, x float64, settings domain.NodeSettings, params map[string]any) domain.Node {
	return domain.Node{Name: name, Type: typ, TypeVersion: 1, Position: domain.Position{X: x}, Settings: settings, Parameters: params}
}

func link(a, b string) domain.Connection {
	return domain.Connection{From: domain.Endpoint{Node: a, Port: domain.PortMain}, To: domain.Endpoint{Node: b, Port: domain.PortMain}}
}

func request(wf domain.Workflow) engine.Request {
	return engine.Request{ExecutionID: "ex_1", Workflow: wf, Mode: domain.ModeManual, StartNode: "Start"}
}

func simple(typ string, settings domain.NodeSettings, params map[string]any) domain.Workflow {
	return wfOf([]domain.Node{nd("Start", "test.trigger", 0, domain.NodeSettings{}, nil), nd("N", typ, 100, settings, params)}, link("Start", "N"))
}

func TestPreflightErrors(t *testing.T) {
	cfg := config.Default().Engine
	cfg.MaxSubworkflowDepth = 2
	e := newEngine(t, cfg, newFakeClock(), nil)

	tests := []struct {
		name    string
		mutate  func(*engine.Request)
		wantMsg string
	}{
		{"unknown start", func(r *engine.Request) { r.StartNode = "Nope" }, "start node"},
		{"unknown node type", func(r *engine.Request) { r.Workflow.Nodes[1].Type = "nope.type" }, "unknown node type"},
		{"invalid workflow", func(r *engine.Request) { r.Workflow.Name = "" }, "invalid workflow"},
		{"depth exceeded", func(r *engine.Request) { r.Depth = 3 }, "depth"},
		{"output index out of range", func(r *engine.Request) { r.Workflow.Connections[0].From.Index = 4 }, "output 4"},
		{"input index out of range", func(r *engine.Request) { r.Workflow.Connections[0].To.Index = 2 }, "input 2"},
		{"unknown destination", func(r *engine.Request) { r.Destination = &engine.Destination{Node: "Nope"} }, "destination"},
		{"unreachable destination", func(r *engine.Request) {
			r.Workflow.Nodes = append(r.Workflow.Nodes, nd("Island", "test.noop", 0, domain.NodeSettings{}, nil))
			r.Destination = &engine.Destination{Node: "Island"}
		}, "not reachable"},
		{"resume of non-waiting", func(r *engine.Request) { r.Resume = &engine.State{Status: domain.StatusSuccess} }, "waiting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := request(simple("test.noop", domain.NodeSettings{}, nil))
			tt.mutate(&req)
			st, err := e.Run(context.Background(), req)
			var we *domain.WorkflowError
			if !errors.As(err, &we) || st != nil {
				t.Fatalf("want *WorkflowError and nil state, got %v / %v", err, st)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("error %q does not contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := engine.New(config.Default().Engine, engine.Deps{}); err == nil {
		t.Fatal("missing deps must fail")
	}
	bad := config.Default().Engine
	bad.MaxNodeRuns = 0
	if _, err := engine.New(bad, engine.Deps{}); err == nil {
		t.Fatal("invalid config must fail")
	}
}

func TestNodeTimeout(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	st, err := e.Run(context.Background(), request(simple("test.block", domain.NodeSettings{Timeout: 20 * time.Millisecond}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != domain.StatusError || st.Error.Kind != domain.ErrorKindNode || !strings.Contains(st.Error.Message, "timed out") {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
}

func TestDefaultNodeTimeoutFromConfig(t *testing.T) {
	cfg := config.Default().Engine
	cfg.DefaultNodeTimeout = 20 * time.Millisecond
	e := newEngine(t, cfg, newFakeClock(), nil)
	st, _ := e.Run(context.Background(), request(simple("test.block", domain.NodeSettings{}, nil)))
	if st.Status != domain.StatusError || !strings.Contains(st.Error.Message, "timed out") {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
}

func TestExecutionTimeout(t *testing.T) {
	cfg := config.Default().Engine
	cfg.ExecutionTimeout = 20 * time.Millisecond
	e := newEngine(t, cfg, newFakeClock(), nil)
	st, err := e.Run(context.Background(), request(simple("test.block", domain.NodeSettings{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != domain.StatusError || st.Error.Kind != domain.ErrorKindTimeout {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
}

func TestWorkflowTimeoutOverridesConfig(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	wf := simple("test.block", domain.NodeSettings{}, nil)
	wf.Settings.Timeout = 20 * time.Millisecond
	st, _ := e.Run(context.Background(), request(wf))
	if st.Status != domain.StatusError || st.Error.Kind != domain.ErrorKindTimeout {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
}

func TestCancellation(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	st, err := e.Run(ctx, request(simple("test.block", domain.NodeSettings{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != domain.StatusCanceled || st.Error.Kind != domain.ErrorKindCanceled {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
	if len(st.Queue) != 0 {
		t.Fatal("a canceled execution must not keep a queue")
	}
}

func TestCancellationDuringRetryBackoff(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st, _ := e.Run(ctx, request(simple("test.fail", domain.NodeSettings{Retry: domain.RetryPolicy{MaxAttempts: 5}}, map[string]any{"message": "x"})))
	if st.Status != domain.StatusCanceled {
		t.Fatalf("status = %s", st.Status)
	}
}

func TestNodePanicBecomesNodeError(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	st, _ := e.Run(context.Background(), request(simple("test.panic", domain.NodeSettings{}, nil)))
	if st.Status != domain.StatusError || !strings.Contains(st.Error.Message, "kaboom") {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
}

func TestNodeReturningTooManyPortsFailsTheWorkflow(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	st, _ := e.Run(context.Background(), request(simple("test.tooManyPorts", domain.NodeSettings{}, nil)))
	if st.Status != domain.StatusError || st.Error.Kind != domain.ErrorKindWorkflow {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
}

func TestRetryDelayIsCapped(t *testing.T) {
	cfg := config.Default().Engine
	cfg.MaxRetryDelay = 150 * time.Millisecond
	clock := newFakeClock()
	e := newEngine(t, cfg, clock, nil)
	policy := domain.RetryPolicy{MaxAttempts: 5, Backoff: domain.BackoffExponential, InitialDelay: 100 * time.Millisecond}
	st, _ := e.Run(context.Background(), request(simple("test.fail", domain.NodeSettings{Retry: policy}, map[string]any{"message": "x"})))
	if st.Status != domain.StatusError || st.RunData["N"][0].Attempts != 5 {
		t.Fatalf("status=%s attempts=%d", st.Status, st.RunData["N"][0].Attempts)
	}
	want := []time.Duration{100, 150, 150, 150}
	got := clock.Sleeps()
	if len(got) != len(want) {
		t.Fatalf("sleeps = %v", got)
	}
	for i := range want {
		if got[i] != want[i]*time.Millisecond {
			t.Fatalf("sleeps = %v, want %v ms", got, want)
		}
	}
}

func TestParameterDefaultsAndRequired(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)

	st, _ := e.Run(context.Background(), request(simple("test.params", domain.NodeSettings{}, map[string]any{"must": "given"})))
	if st.Status != domain.StatusSuccess {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
	got := st.RunData["N"][0].Output[domain.PortMain][0][0].JSON
	if got["greeting"] != "hi" || got["must"] != "given" {
		t.Fatalf("json = %v (descriptor default must apply)", got)
	}

	st, _ = e.Run(context.Background(), request(simple("test.params", domain.NodeSettings{}, nil)))
	if st.Status != domain.StatusError || !strings.Contains(st.Error.Message, "required parameter is missing") {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
}

type recordingObserver struct {
	mu     sync.Mutex
	events []string
}

func (o *recordingObserver) add(s string) { o.mu.Lock(); o.events = append(o.events, s); o.mu.Unlock() }
func (o *recordingObserver) ExecutionStarted(_ context.Context, ex engine.ExecutionInfo) {
	o.add("start:" + string(ex.ExecutionID))
}
func (o *recordingObserver) NodeStarted(_ context.Context, n string, _ int) { o.add("node-start:" + n) }
func (o *recordingObserver) NodeFinished(_ context.Context, n string, r domain.NodeRun) {
	o.add("node-end:" + n + ":" + string(r.Status))
}
func (o *recordingObserver) ExecutionFinished(_ context.Context, r engine.Result) {
	o.add("end:" + string(r.Status))
}

func TestObserverSeesEverything(t *testing.T) {
	obs := &recordingObserver{}
	e := newEngine(t, config.Default().Engine, newFakeClock(), obs)
	if _, err := e.Run(context.Background(), request(simple("test.noop", domain.NodeSettings{}, nil))); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(obs.events, ",")
	want := "start:ex_1,node-start:Start,node-end:Start:success,node-start:N,node-end:N:success,end:success"
	if got != want {
		t.Fatalf("events\n got %s\nwant %s", got, want)
	}
}

func TestExpressionsSeeVarsAndMetadata(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	wf := simple("test.set", domain.NodeSettings{}, map[string]any{"values": map[string]any{
		"base": "={{ $vars.base }}", "exec": "={{ $execution.id }}/{{ $execution.mode }}", "wf": "={{ $workflow.name }}", "run": "={{ $runIndex }}",
	}})
	req := request(wf)
	req.Vars = map[string]any{"base": "https://x.test"}
	st, _ := e.Run(context.Background(), req)
	if st.Status != domain.StatusSuccess {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
	got := st.RunData["N"][0].Output[domain.PortMain][0][0].JSON
	if got["base"] != "https://x.test" || got["exec"] != "ex_1/manual" || got["wf"] != "t" || got["run"] != float64(0) {
		t.Fatalf("json = %v", got)
	}
}

func TestWorkflowPinDataOnlyAppliesInManualMode(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	wf := simple("test.fail", domain.NodeSettings{}, map[string]any{"message": "ran"})
	wf.PinData = map[string][]domain.Item{"N": {{JSON: map[string]any{"pinned": true}}}}

	req := request(wf)
	if st, _ := e.Run(context.Background(), req); st.Status != domain.StatusSuccess {
		t.Fatalf("manual: status = %s", st.Status)
	}
	req.Mode = domain.ModeWebhook
	if st, _ := e.Run(context.Background(), req); st.Status != domain.StatusError {
		t.Fatalf("webhook must ignore workflow pin data, status = %s", st.Status)
	}
	req.PinData = wf.PinData // explicit request pin data always applies
	if st, _ := e.Run(context.Background(), req); st.Status != domain.StatusSuccess {
		t.Fatalf("request pin data: status = %s", st.Status)
	}
}

func TestCredentialLookupFailureIsANodeError(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	// No credential bound to "api" on the node.
	st, _ := e.Run(context.Background(), request(simple("test.cred", domain.NodeSettings{}, nil)))
	if st.Status != domain.StatusError || !strings.Contains(st.Error.Message, `no credential "api"`) {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
	// Bound, but the no-op resolver knows no credentials.
	wf := simple("test.cred", domain.NodeSettings{}, nil)
	wf.Nodes[1].Credentials = map[string]domain.CredentialID{"api": "cr_1"}
	st, _ = e.Run(context.Background(), request(wf))
	if st.Status != domain.StatusError || !strings.Contains(st.Error.Message, "not found") {
		t.Fatalf("status=%s err=%+v", st.Status, st.Error)
	}
}

func TestValidateAgainstRegistry(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	if err := e.Validate(simple("test.noop", domain.NodeSettings{}, nil)); err != nil {
		t.Fatal(err)
	}
	if err := e.Validate(simple("nope", domain.NodeSettings{}, nil)); err == nil {
		t.Fatal("unknown type must fail")
	}
}

func TestConcurrentRunsAreIndependent(t *testing.T) {
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	wf := simple("test.set", domain.NodeSettings{}, map[string]any{"values": map[string]any{"x": "={{ $json.n + 1 }}"}})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := request(wf)
			req.StartItems = []domain.Item{{JSON: map[string]any{"n": float64(i)}}}
			st, err := e.Run(context.Background(), req)
			if err != nil || st.Status != domain.StatusSuccess {
				t.Errorf("run %d: %v %v", i, err, st)
				return
			}
			if got := st.RunData["N"][0].Output[domain.PortMain][0][0].JSON["x"]; got != float64(i+1) {
				t.Errorf("run %d: x = %v", i, got)
			}
		}()
	}
	wg.Wait()
}

func TestInputItemsAreIsolatedBetweenBranches(t *testing.T) {
	// Both children receive the same items; mutating one copy must not leak.
	e := newEngine(t, config.Default().Engine, newFakeClock(), nil)
	nodes := []domain.Node{
		nd("Start", "test.trigger", 0, domain.NodeSettings{}, nil),
		nd("A", "test.set", 100, domain.NodeSettings{}, map[string]any{"values": map[string]any{"who": "a"}}),
		nd("B", "test.noop", 200, domain.NodeSettings{}, nil),
	}
	nodes[1].Position.Y, nodes[2].Position.Y = 0, 100
	st, _ := e.Run(context.Background(), request(wfOf(nodes, link("Start", "A"), link("Start", "B"))))
	if _, leaked := st.RunData["B"][0].Output[domain.PortMain][0][0].JSON["who"]; leaked {
		t.Fatal("branch A leaked into branch B")
	}
}
