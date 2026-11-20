package trigger_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/noop"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/internal/node/scheduletrigger"
	"github.com/engi-hq/engi/internal/node/webhooktrigger"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/servicetest"
	"github.com/engi-hq/engi/internal/service/trigger"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// --- fakes ---

type webhookStore struct {
	mu   sync.Mutex
	regs []domain.WebhookRegistration
}

func (w *webhookStore) Register(_ context.Context, r domain.WebhookRegistration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, o := range w.regs {
		if o.Method == r.Method && o.Shape() == r.Shape() {
			return domain.ErrAlreadyExists
		}
	}
	w.regs = append(w.regs, r)
	return nil
}

func (w *webhookStore) DeleteByWorkflow(_ context.Context, id domain.WorkflowID) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var keep []domain.WebhookRegistration
	for _, o := range w.regs {
		if o.WorkflowID != id {
			keep = append(keep, o)
		}
	}
	w.regs = keep
	return nil
}

func (w *webhookStore) Find(_ context.Context, method, path string) (domain.WebhookRegistration, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, o := range w.regs {
		if o.Method == method && o.Shape() == path {
			return o, nil
		}
	}
	return domain.WebhookRegistration{}, domain.ErrNotFound
}

func (w *webhookStore) ListDynamic(_ context.Context, method string, segments int) ([]domain.WebhookRegistration, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []domain.WebhookRegistration
	for _, o := range w.regs {
		if o.Method == method && o.Dynamic() && o.Segments() == segments {
			out = append(out, o)
		}
	}
	return out, nil
}

func (w *webhookStore) ListByWorkflow(_ context.Context, id domain.WorkflowID) ([]domain.WebhookRegistration, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []domain.WebhookRegistration
	for _, o := range w.regs {
		if o.WorkflowID == id {
			out = append(out, o)
		}
	}
	return out, nil
}

type stateStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *stateStore) key(id domain.WorkflowID, node string) string { return string(id) + "/" + node }

func (s *stateStore) LoadTriggerState(_ context.Context, id domain.WorkflowID, node string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[s.key(id, node)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return b, nil
}

func (s *stateStore) SaveTriggerState(_ context.Context, id domain.WorkflowID, node string, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[s.key(id, node)] = b
	return nil
}

func (s *stateStore) DeleteTriggerState(_ context.Context, id domain.WorkflowID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if len(k) > len(id) && k[:len(id)+1] == string(id)+"/" {
			delete(s.m, k)
		}
	}
	return nil
}

type activationStore struct {
	mu sync.Mutex
	m  map[domain.WorkflowID]string
}

func (a *activationStore) SetActivationError(_ context.Context, id domain.WorkflowID, m string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.m[id] = m
	return nil
}

func (a *activationStore) ActivationError(_ context.Context, id domain.WorkflowID) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.m[id], nil
}

func (a *activationStore) ClearActivationError(_ context.Context, id domain.WorkflowID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.m, id)
	return nil
}

type creds map[domain.CredentialID]bool

func (c creds) Resolve(_ context.Context, id domain.CredentialID) (port.CredentialData, error) {
	if !c[id] {
		return port.CredentialData{}, domain.ErrNotFound
	}
	return port.CredentialData{}, nil
}

type starter struct {
	mu   sync.Mutex
	reqs []execution.StartRequest
	err  error
}

func (s *starter) Start(_ context.Context, r execution.StartRequest) (domain.ExecutionID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	for _, o := range s.reqs {
		if o.DedupKey != "" && o.DedupKey == r.DedupKey && o.WorkflowID == r.WorkflowID {
			return "exec_existing", &domain.DuplicateError{WorkflowID: r.WorkflowID, DedupKey: r.DedupKey}
		}
	}
	s.reqs = append(s.reqs, r)
	return "exec_new", nil
}

func (s *starter) Await(context.Context, domain.ExecutionID) (domain.Execution, error) {
	return domain.Execution{}, nil
}

func (s *starter) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

// lease is a LeaseProvider the test drives by hand.
type lease struct {
	cb chan port.LeaderCallbacks
}

func (l *lease) Run(ctx context.Context, cb port.LeaderCallbacks) error {
	l.cb <- cb
	<-ctx.Done()
	return nil
}

// blockingSleeper never ticks: tests call Tick by hand.
type blockingSleeper struct{}

func (blockingSleeper) Sleep(ctx context.Context, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err() //nolint:wrapcheck // test double
}

// --- environment ---

type env struct {
	svc      *trigger.Service
	store    *servicetest.Store
	clock    *servicetest.Clock
	hooks    *webhookStore
	tests    *servicetest.TestWebhooks // armed test webhooks, shared by the services of a test
	nodes    trigger.Nodes
	states   *stateStore
	acts     *activationStore
	starter  *starter
	lease    *lease
	creds    creds
	cfg      config.TriggerConfig
	idSource interface {
		WorkflowID() (domain.WorkflowID, error)
	}
}

func newEnv(t *testing.T, mutate ...func(*config.TriggerConfig)) *env {
	t.Helper()
	return newEnvWith(t, nil, blockingSleeper{}, nil, mutate...)
}

// newEnvWith also registers extra node factories and sets the sleeper and the
// queue depth source.
func newEnvWith(t *testing.T, extra []registry.Factory, sleeper port.Sleeper, queue port.QueueStats, mutate ...func(*config.TriggerConfig)) *env {
	t.Helper()
	clock := servicetest.NewClock()
	e := &env{
		store: servicetest.NewStore(clock), clock: clock, hooks: &webhookStore{}, tests: servicetest.NewTestWebhooks(), states: &stateStore{m: map[string][]byte{}},
		acts: &activationStore{m: map[domain.WorkflowID]string{}}, starter: &starter{}, lease: &lease{cb: make(chan port.LeaderCallbacks, 1)},
		creds: creds{}, cfg: config.Default().Trigger, idSource: servicetest.NewIDs(clock),
	}
	for _, m := range mutate {
		m(&e.cfg)
	}
	reg := registry.New()
	for _, f := range append([]registry.Factory{webhooktrigger.New, func() nodesdk.Node { return scheduletrigger.New("UTC") }, noop.New}, extra...) {
		if err := reg.Register(f); err != nil {
			t.Fatal(err)
		}
	}
	e.nodes = reg
	svc, err := trigger.New(e.cfg, trigger.Deps{
		Workflows: e.store, Webhooks: e.hooks, TestWebhooks: e.tests, States: e.states, Activations: e.acts, Credentials: e.creds, Nodes: reg,
		Executions: e.starter, Lease: e.lease, Bus: servicetest.NewBus(), Clock: clock, Sleeper: sleeper, Queue: queue,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	return e
}

// lead makes the service the leader and returns a function that ends leadership.
func (e *env) lead(t *testing.T) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cb := <-e.lease.cb
	leaderCtx, endLead := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); cb.OnStartedLeading(leaderCtx) }()
	// wait until the leader state is set
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e.svc.Leading() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	return func() {
		endLead()
		<-done
		cb.OnStoppedLeading()
		cancel()
		if err := e.svc.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}
}

func (e *env) save(ctx context.Context, t *testing.T, nodes ...domain.Node) domain.Workflow {
	t.Helper()
	id, err := e.idSource.WorkflowID()
	if err != nil {
		t.Fatal(err)
	}
	wf, err := e.store.Save(ctx, domain.Workflow{ID: id, Name: "wf", Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func hookNode(name string, params map[string]any) domain.Node {
	return domain.Node{Name: name, Type: webhooktrigger.Type, TypeVersion: 1, Parameters: params}
}

func schedNode(name string, params map[string]any) domain.Node {
	return domain.Node{Name: name, Type: scheduletrigger.Type, TypeVersion: 1, Parameters: params}
}

func (e *env) active(ctx context.Context, t *testing.T, id domain.WorkflowID) bool {
	t.Helper()
	wf, err := e.store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return wf.Active
}

// --- tests ---

func TestActivateRegistersWebhooksAndDeactivateRemovesThem(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, hookNode("In", map[string]any{"httpMethod": "POST", "path": "/orders/:id/"}), domain.Node{Name: "Out", Type: noop.Type, TypeVersion: 1})
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	if !e.active(ctx, t, wf.ID) {
		t.Fatal("workflow must be active")
	}
	r, err := e.svc.ResolveLive(ctx, "post", "orders/42")
	if err != nil {
		t.Fatal(err)
	}
	if r.Node != "In" || r.Params["id"] != "42" || r.Mode != domain.ModeWebhook || r.Spec.ResponseMode != nodesdk.ResponseImmediately {
		t.Fatalf("route = %+v", r)
	}
	if _, err := e.svc.ResolveLive(ctx, "GET", "orders/42"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other method: %v", err)
	}
	if err := e.svc.Deactivate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	if e.active(ctx, t, wf.ID) {
		t.Fatal("workflow must be inactive")
	}
	if _, err := e.svc.ResolveLive(ctx, "POST", "orders/42"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("after deactivate: %v", err)
	}
}

func TestStaticRoutesWinOverParameterRoutes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.save(ctx, t, hookNode("A", map[string]any{"path": "items/:id"}))
	b := e.save(ctx, t, hookNode("B", map[string]any{"path": "items/new"}))
	for _, wf := range []domain.Workflow{a, b} {
		if err := e.svc.Activate(ctx, wf.ID); err != nil {
			t.Fatal(err)
		}
	}
	r, err := e.svc.ResolveLive(ctx, "GET", "items/new")
	if err != nil || r.WorkflowID != b.ID {
		t.Fatalf("static: %+v %v", r, err)
	}
	r, err = e.svc.ResolveLive(ctx, "GET", "items/7")
	if err != nil || r.WorkflowID != a.ID || r.Params["id"] != "7" {
		t.Fatalf("dynamic: %+v %v", r, err)
	}
}

func TestDefaultPathIsWorkflowAndNode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, hookNode("My Hook!", nil))
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.ResolveLive(ctx, "GET", string(wf.ID)+"/my-hook")
	if err != nil || r.Node != "My Hook!" {
		t.Fatalf("route = %+v, %v", r, err)
	}
}

func TestActivationConflictAndFailureRecordTheReason(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.save(ctx, t, hookNode("In", map[string]any{"path": "same"}))
	second := e.save(ctx, t, hookNode("In", map[string]any{"path": "same"}))
	if err := e.svc.Activate(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	err := e.svc.Activate(ctx, second.ID)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate path: %v", err)
	}
	if e.active(ctx, t, second.ID) {
		t.Fatal("a failed activation must leave the workflow inactive")
	}
	if msg, _ := e.acts.ActivationError(ctx, second.ID); msg == "" {
		t.Fatal("the reason must be recorded")
	}
	// the first workflow keeps its route
	if _, err := e.svc.ResolveLive(ctx, "GET", "same"); err != nil {
		t.Fatal(err)
	}
	// success clears the recorded error
	if err := e.svc.Deactivate(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Activate(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if msg, _ := e.acts.ActivationError(ctx, second.ID); msg != "" {
		t.Fatalf("error not cleared: %q", msg)
	}
}

func TestActivateValidatesTriggerNodes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cases := map[string][]domain.Node{
		"no trigger":         {{Name: "N", Type: noop.Type, TypeVersion: 1}},
		"disabled trigger":   {{Name: "In", Type: webhooktrigger.Type, TypeVersion: 1, Disabled: true}},
		"bad method":         {hookNode("In", map[string]any{"httpMethod": "TRACE"})},
		"bad param name":     {hookNode("In", map[string]any{"path": "a/:1x"})},
		"empty segment":      {hookNode("In", map[string]any{"path": "a//b"})},
		"missing credential": {hookNode("In", map[string]any{"authentication": "basic"})},
		"bad cron":           {schedNode("S", map[string]any{"rule": "cron", "cronExpression": "nope"})},
		"unknown type":       {{Name: "X", Type: "no.such.node", TypeVersion: 1}},
		"two same routes":    {hookNode("A", map[string]any{"path": "p"}), hookNode("B", map[string]any{"path": "p"})},
	}
	for name, nodes := range cases {
		t.Run(name, func(t *testing.T) {
			wf := e.save(ctx, t, nodes...)
			err := e.svc.Activate(ctx, wf.ID)
			var ve *domain.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want a validation error", err)
			}
			if e.active(ctx, t, wf.ID) {
				t.Fatal("must stay inactive")
			}
		})
	}
	t.Run("unknown credential", func(t *testing.T) {
		n := hookNode("In", map[string]any{"authentication": "header"})
		n.Credentials = map[string]domain.CredentialID{"headerAuth": "cred_missing"}
		wf := e.save(ctx, t, n)
		if err := e.svc.Activate(ctx, wf.ID); err == nil {
			t.Fatal("want an error")
		}
	})
	t.Run("known credential", func(t *testing.T) {
		e.creds["cred_ok"] = true
		n := hookNode("In", map[string]any{"authentication": "header", "path": "secured"})
		n.Credentials = map[string]domain.CredentialID{"headerAuth": "cred_ok"}
		wf := e.save(ctx, t, n)
		if err := e.svc.Activate(ctx, wf.ID); err != nil {
			t.Fatal(err)
		}
		r, err := e.svc.ResolveLive(ctx, "GET", "secured")
		if err != nil || r.Credential != "cred_ok" || r.Spec.Auth != nodesdk.WebhookAuthHeader {
			t.Fatalf("route = %+v, %v", r, err)
		}
	})
	if _, err := e.svc.ResolveLive(ctx, "GET", "unknown"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	if err := e.svc.Activate(ctx, "wf_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing workflow: %v", err)
	}
}

func TestActivateAllRestoresActiveWorkflowsAndDeactivatesBrokenOnes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	good := e.save(ctx, t, hookNode("In", map[string]any{"path": "good"}))
	broken := e.save(ctx, t, hookNode("In", map[string]any{"httpMethod": "TRACE"}))
	idle := e.save(ctx, t, hookNode("In", map[string]any{"path": "idle"}))
	for _, id := range []domain.WorkflowID{good.ID, broken.ID} {
		if err := e.store.SetActive(ctx, id, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.ActivateAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveLive(ctx, "GET", "good"); err != nil {
		t.Fatalf("good: %v", err)
	}
	if _, err := e.svc.ResolveLive(ctx, "GET", "idle"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("idle workflow must not be registered: %v", err)
	}
	if e.active(ctx, t, broken.ID) {
		t.Fatal("broken workflow must be made inactive")
	}
	if msg, _ := e.acts.ActivationError(ctx, broken.ID); msg == "" {
		t.Fatal("reason missing")
	}
	_ = idle
}

func TestTestWebhookIsOneShotAndExpires(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, hookNode("In", map[string]any{"httpMethod": "POST", "path": "try/:x"}))
	tw, err := e.svc.ArmTest(ctx, wf.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if tw.URLPath != "/hook-test/try/:x" || !tw.ExpiresAt.Equal(e.clock.Now().Add(e.cfg.Webhook.TestTimeout)) {
		t.Fatalf("test webhook = %+v", tw)
	}
	r, err := e.svc.ResolveTest(ctx, "POST", "try/9")
	if err != nil || !r.Test || r.Mode != domain.ModeManual || r.Params["x"] != "9" {
		t.Fatalf("route = %+v, %v", r, err)
	}
	if _, err := e.svc.ResolveTest(ctx, "POST", "try/9"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second call: %v", err)
	}
	if _, err := e.svc.ArmTest(ctx, wf.ID, ""); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(e.cfg.Webhook.TestTimeout)
	if _, err := e.svc.ResolveTest(ctx, "POST", "try/9"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := e.svc.ArmTest(ctx, wf.ID, "Nope"); err == nil {
		t.Fatal("unknown node must fail")
	}
}

// peer returns a second service over the same stores, as another pod sees them.
func (e *env) peer(t *testing.T) *trigger.Service {
	t.Helper()
	svc, err := trigger.New(e.cfg, trigger.Deps{
		Workflows: e.store, Webhooks: e.hooks, TestWebhooks: e.tests, States: e.states, Activations: e.acts, Credentials: e.creds,
		Nodes: e.nodes, Executions: e.starter, Lease: e.lease, Clock: e.clock, Sleeper: blockingSleeper{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// A test armed by the api pod is served by a webhook pod sharing the store.
func TestTestWebhookArmedOnOnePodIsServedByAnother(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, hookNode("In", map[string]any{"httpMethod": "POST", "path": "try/:x"}))
	apiPod, webhookPod := e.svc, e.peer(t)
	if _, err := apiPod.ArmTest(ctx, wf.ID, ""); err != nil {
		t.Fatal(err)
	}
	r, err := webhookPod.ResolveTest(ctx, "POST", "try/9")
	if err != nil || !r.Test || r.Mode != domain.ModeManual || r.Params["x"] != "9" || r.WorkflowID != wf.ID {
		t.Fatalf("route on the webhook pod = %+v, %v", r, err)
	}
	if _, err := apiPod.ResolveTest(ctx, "POST", "try/9"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the test is one-shot across pods: %v", err)
	}
}

// Concurrent calls on several pods: exactly one gets the test.
func TestTestWebhookIsClaimedOnceAcrossPods(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, hookNode("In", map[string]any{"httpMethod": "POST", "path": "race"}))
	if _, err := e.svc.ArmTest(ctx, wf.ID, ""); err != nil {
		t.Fatal(err)
	}
	pods := []*trigger.Service{e.svc, e.peer(t), e.peer(t)}
	const callsPerPod = 4
	var wins, misses int32
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range pods {
		for range callsPerPod {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := p.ResolveTest(ctx, "POST", "race")
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					wins++
				case errors.Is(err, domain.ErrNotFound):
					misses++
				default:
					t.Errorf("ResolveTest: %v", err)
				}
			}()
		}
	}
	wg.Wait()
	if wins != 1 || misses != int32(len(pods)*callsPerPod-1) {
		t.Fatalf("wins = %d, misses = %d", wins, misses)
	}
}

// Deactivating on one pod disarms the test everywhere.
func TestDeactivateDisarmsTestsOfEveryPod(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, hookNode("In", map[string]any{"httpMethod": "POST", "path": "gone"}))
	if _, err := e.svc.ArmTest(ctx, wf.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.peer(t).Deactivate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveTest(ctx, "POST", "gone"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("after deactivation on another pod: %v", err)
	}
}

func TestSchedulesRunOnlyOnTheLeader(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, schedNode("Every", map[string]any{"rule": "interval", "interval": 1.0, "unit": "minutes"}))
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute)
	e.svc.Tick(ctx)
	if n := e.starter.count(); n != 0 {
		t.Fatalf("a follower fired %d runs", n)
	}
	stop := e.lead(t)
	// the new leader loads the active workflow from the store
	e.clock.Advance(time.Minute)
	e.svc.Tick(ctx)
	if n := e.starter.count(); n != 1 {
		t.Fatalf("leader fired %d runs, want 1", n)
	}
	stop()
	e.clock.Advance(time.Hour)
	e.svc.Tick(ctx)
	if n := e.starter.count(); n != 1 {
		t.Fatalf("a former leader fired: %d", n)
	}
}

func TestIntervalFiresOnTheGridWithDedupKeyAndItem(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, schedNode("Every", map[string]any{"rule": "interval", "interval": 30.0, "unit": "seconds"}))
	stop := e.lead(t)
	defer stop()
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	start := e.clock.Now()
	e.clock.Advance(29 * time.Second)
	e.svc.Tick(ctx)
	if e.starter.count() != 0 {
		t.Fatal("fired early")
	}
	e.clock.Advance(2 * time.Second)
	e.svc.Tick(ctx)
	e.svc.Tick(ctx) // nothing more is due
	if e.starter.count() != 1 {
		t.Fatalf("runs = %d", e.starter.count())
	}
	r := e.starter.reqs[0]
	wantAt := start.Add(30 * time.Second)
	if r.Mode != domain.ModeTrigger || r.StartNode != "Every" || r.WorkflowID != wf.ID ||
		r.DedupKey != "Every:"+wantAt.Format(time.RFC3339Nano) || r.Items[0].JSON["timestamp"] != wantAt.Format(time.RFC3339) {
		t.Fatalf("request = %+v", r)
	}
	// next run keeps the grid: start+60s
	e.clock.Advance(29 * time.Second) // start+60s
	e.svc.Tick(ctx)
	if e.starter.count() != 2 || e.starter.reqs[1].DedupKey != "Every:"+start.Add(60*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("second run: %+v", e.starter.reqs)
	}
}

func TestCronInATimezone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// The test clock is 2026-05-06 07:08:09 UTC = 09:08:09 in Berlin (CEST).
	wf := e.save(ctx, t, schedNode("Daily", map[string]any{"rule": "cron", "cronExpression": "30 9 * * *", "timezone": "Europe/Berlin"}))
	stop := e.lead(t)
	defer stop()
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(21 * time.Minute) // 07:29:09 UTC = 09:29 Berlin
	e.svc.Tick(ctx)
	if e.starter.count() != 0 {
		t.Fatal("fired before 09:30 Berlin")
	}
	e.clock.Advance(time.Minute) // 09:30:09 Berlin
	e.svc.Tick(ctx)
	if e.starter.count() != 1 {
		t.Fatalf("runs = %d", e.starter.count())
	}
	if got := e.starter.reqs[0].Items[0].JSON; got["timestamp"] != "2026-05-06T09:30:00+02:00" || got["timezone"] != "Europe/Berlin" {
		t.Fatalf("item = %v", got)
	}
}

func (e *env) setClock(t time.Time) { e.clock.Advance(t.Sub(e.clock.Now())) }

func TestCronAcrossDaylightSaving(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, schedNode("Daily", map[string]any{"rule": "cron", "cronExpression": "30 2 * * *", "timezone": "Europe/Berlin"}))
	stop := e.lead(t)
	defer stop()
	stamps := func() []string {
		var out []string
		for _, r := range e.starter.reqs {
			out = append(out, r.Items[0].JSON["timestamp"].(string))
		}
		return out
	}
	step := func(at time.Time, want ...string) {
		t.Helper()
		e.setClock(at)
		e.svc.Tick(ctx)
		if got := stamps(); len(got) != len(want) || (len(got) > 0 && got[len(got)-1] != want[len(want)-1]) {
			t.Fatalf("at %s: runs = %v, want %v", at, got, want)
		}
	}

	// Spring forward 2026-03-29: 02:30 does not exist in Berlin, the run fires at the jump (03:00 CEST).
	e.setClock(time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC))
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	step(time.Date(2026, 3, 29, 0, 59, 0, 0, time.UTC))
	step(time.Date(2026, 3, 29, 1, 0, 10, 0, time.UTC), "2026-03-29T03:00:00+02:00")
	step(time.Date(2026, 3, 30, 0, 30, 10, 0, time.UTC), "2026-03-29T03:00:00+02:00", "2026-03-30T02:30:00+02:00")

	// Fall back 2026-10-25: 02:30 happens twice, the run fires once.
	if err := e.svc.Deactivate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	e.setClock(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	n := len(e.starter.reqs)
	e.setClock(time.Date(2026, 10, 25, 0, 30, 10, 0, time.UTC)) // 02:30 CEST
	e.svc.Tick(ctx)
	e.setClock(time.Date(2026, 10, 25, 1, 30, 10, 0, time.UTC)) // 02:30 CET, the repeat
	e.svc.Tick(ctx)
	if got := len(e.starter.reqs) - n; got != 1 {
		t.Fatalf("runs on the repeated hour = %d, want 1 (%v)", got, stamps())
	}
}

func TestMissedRunPolicy(t *testing.T) {
	for _, tc := range []struct {
		policy string
		want   int
	}{{config.MissedSkip, 0}, {config.MissedRunOnce, 1}} {
		t.Run(tc.policy, func(t *testing.T) {
			e := newEnv(t, func(c *config.TriggerConfig) { c.Schedule.MissedPolicy = tc.policy })
			ctx := context.Background()
			wf := e.save(ctx, t, schedNode("Every", map[string]any{"rule": "interval", "interval": 1.0, "unit": "minutes"}))
			stop := e.lead(t)
			defer stop()
			if err := e.svc.Activate(ctx, wf.ID); err != nil {
				t.Fatal(err)
			}
			// Downtime of ten minutes: ten runs were due, the scheduler is only back now.
			e.clock.Advance(10*time.Minute + 5*time.Second)
			e.svc.Tick(ctx)
			if got := e.starter.count(); got != tc.want {
				t.Fatalf("runs after downtime = %d, want %d", got, tc.want)
			}
			e.svc.Tick(ctx)
			if got := e.starter.count(); got != tc.want {
				t.Fatalf("a second tick must not catch up again: %d", got)
			}
			// the schedule continues on its grid
			e.clock.Advance(time.Minute)
			e.svc.Tick(ctx)
			if got := e.starter.count(); got != tc.want+1 {
				t.Fatalf("runs after the next due time = %d, want %d", got, tc.want+1)
			}
		})
	}
}

func TestRestartResumesFromSavedState(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, schedNode("Every", map[string]any{"rule": "interval", "interval": 1.0, "unit": "minutes"}))
	stop := e.lead(t)
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	stop()
	// A second service on the same stores: the saved next time survives.
	e.lease = &lease{cb: make(chan port.LeaderCallbacks, 1)}
	reg := registry.New()
	for _, f := range []registry.Factory{func() nodesdk.Node { return scheduletrigger.New("UTC") }} {
		if err := reg.Register(f); err != nil {
			t.Fatal(err)
		}
	}
	svc2, err := trigger.New(e.cfg, trigger.Deps{
		Workflows: e.store, Webhooks: e.hooks, TestWebhooks: e.tests, States: e.states, Activations: e.acts, Credentials: e.creds, Nodes: reg,
		Executions: e.starter, Lease: e.lease, Clock: e.clock, Sleeper: blockingSleeper{},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc2
	e.clock.Advance(30 * time.Second)
	stop2 := e.lead(t)
	defer stop2()
	e.clock.Advance(31 * time.Second) // 61 s after activation: the saved next (60 s) is due, not a fresh 60 s from restart
	e.svc.Tick(ctx)
	if e.starter.count() != 1 {
		t.Fatalf("runs = %d, want 1", e.starter.count())
	}
}

func TestDeactivateStopsSchedules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, schedNode("Every", map[string]any{"rule": "interval", "interval": 1.0, "unit": "minutes"}))
	stop := e.lead(t)
	defer stop()
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Deactivate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Hour)
	e.svc.Tick(ctx)
	if e.starter.count() != 0 {
		t.Fatal("a deactivated schedule fired")
	}
}

func TestStartFailureRetriesOnNextTick(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	wf := e.save(ctx, t, schedNode("Every", map[string]any{"rule": "interval", "interval": 1.0, "unit": "minutes"}))
	stop := e.lead(t)
	defer stop()
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(time.Minute)
	e.starter.err = errors.New("database busy")
	e.svc.Tick(ctx)
	e.starter.err = nil
	e.svc.Tick(ctx)
	if e.starter.count() != 1 {
		t.Fatalf("runs = %d, want the failed one retried", e.starter.count())
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	if _, err := trigger.New(config.Default().Trigger, trigger.Deps{}); err == nil {
		t.Fatal("want an error")
	}
}
