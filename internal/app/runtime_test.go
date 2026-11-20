package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/adapter/sqlitedb"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/waiter"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// runtimeFixture is a real standalone runtime over a temporary SQLite database:
// store, durable queue, bus, worker, waiter and execution service.
type runtimeFixture struct {
	app *App
	rt  *Runtime
}

func newRuntime(t *testing.T, mutate ...func(*config.Config)) *runtimeFixture {
	t.Helper()
	cfg := config.Default()
	cfg.Store.Path = filepath.Join(t.TempDir(), "engi.db")
	cfg.Queue.LeaseDuration, cfg.Queue.HeartbeatInterval = 400*time.Millisecond, 100*time.Millisecond
	cfg.Queue.PollInterval, cfg.Queue.RetryDelay = 20*time.Millisecond, 0
	cfg.Worker.ReclaimInterval, cfg.Worker.WaiterInterval = 100*time.Millisecond, 50*time.Millisecond
	cfg.Worker.SyncPollInterval, cfg.Worker.SyncRunTimeout = 20*time.Millisecond, 10*time.Second
	cfg.Worker.ShutdownGrace = 2 * time.Second
	for _, m := range mutate {
		m(&cfg)
	}
	a, err := New(t.Context(), cfg, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := a.NewRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rt.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return &runtimeFixture{app: a, rt: rt}
}

func (f *runtimeFixture) start(t *testing.T) {
	t.Helper()
	if err := f.rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// save stores a workflow document under a fresh id and returns it.
func (f *runtimeFixture) save(t *testing.T, doc string) domain.Workflow {
	t.Helper()
	wf := workflow(t, doc)
	id, err := f.app.ids.WorkflowID()
	if err != nil {
		t.Fatal(err)
	}
	wf.ID = id
	saved, err := f.rt.Workflows.Save(context.Background(), wf)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func (f *runtimeFixture) begin(t *testing.T, wf domain.Workflow, start string, items ...domain.Item) domain.ExecutionID {
	t.Helper()
	id, err := f.rt.Executions.Start(context.Background(), execution.StartRequest{
		WorkflowID: wf.ID, Mode: domain.ModeManual, StartNode: start, Items: items,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// until polls the stored execution until cond holds.
func (f *runtimeFixture) until(t *testing.T, id domain.ExecutionID, what string, cond func(domain.Execution) bool) domain.Execution {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ex, err := f.rt.Executions.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if cond(ex) {
			return ex
		}
		time.Sleep(10 * time.Millisecond)
	}
	ex, _ := f.rt.Executions.Get(context.Background(), id)
	t.Fatalf("timed out waiting for %s; execution is %s (error %+v)", what, ex.Status, ex.Error)
	return domain.Execution{}
}

func (f *runtimeFixture) status(t *testing.T, id domain.ExecutionID, want domain.ExecutionStatus) domain.Execution {
	t.Helper()
	return f.until(t, id, "status "+string(want), func(e domain.Execution) bool { return e.Status == want })
}

func items(n ...float64) []domain.Item {
	out := make([]domain.Item, len(n))
	for i, v := range n {
		out[i] = domain.Item{JSON: map[string]any{"n": v}}
	}
	return out
}

func outputOf(ex domain.Execution, node string) []map[string]any {
	var out []map[string]any
	runs := ex.RunData[node]
	if len(runs) == 0 {
		return nil
	}
	for _, it := range runs[len(runs)-1].Output[domain.PortMain][0] {
		out = append(out, it.JSON)
	}
	return out
}

func TestRuntimeRunsAWorkflowThroughQueueWorkerAndStore(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	wf := f.save(t, pipelineDoc)
	id := f.begin(t, wf, "Start", items(1, 5, 2)...)

	ex := f.status(t, id, domain.StatusSuccess)
	if ex.FinishedAt == nil || ex.WorkflowVersion != 1 || ex.Mode != domain.ModeManual {
		t.Fatalf("execution = %+v", ex)
	}
	if big := outputOf(ex, "Big"); len(big) != 1 || big[0]["size"] != "big" || big[0]["double"] != 10.0 {
		t.Fatalf("big = %v", big)
	}
	if small := outputOf(ex, "Small"); len(small) != 2 {
		t.Fatalf("small = %v", small)
	}
	if _, err := f.rt.Executions.Get(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	// The job is gone and the saved input dropped.
	var jobs int
	if err := f.rt.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("jobs left = %d (%v)", jobs, err)
	}
	if _, err := f.rt.states.LoadState(context.Background(), id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("state must be dropped, got %v", err)
	}
}

const waitDoc = `{
  "name": "wait",
  "nodes": [
    {"name": "Start", "type": "engi.trigger.manual", "position": [0, 0]},
    {"name": "Pause", "type": "engi.flow.wait", "position": [200, 0], "parameters": %s},
    {"name": "After", "type": "engi.transform.set", "position": [400, 0], "parameters": {"fields": [{"name": "after", "value": "yes"}]}}
  ],
  "connections": [
    {"from": {"node": "Start"}, "to": {"node": "Pause"}},
    {"from": {"node": "Pause"}, "to": {"node": "After"}}
  ]
}`

func TestRuntimeResumesATimedWait(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	wf := f.save(t, fmt.Sprintf(waitDoc, `{"resume": "interval", "amount": 0.4, "unit": "seconds"}`))
	started := time.Now()
	id := f.begin(t, wf, "Start", items(1)...)

	waiting := f.status(t, id, domain.StatusWaiting)
	if waiting.WaitUntil == nil || waiting.RunData["After"] != nil {
		t.Fatalf("waiting execution = %+v", waiting)
	}
	ex := f.status(t, id, domain.StatusSuccess)
	if elapsed := time.Since(started); elapsed < 300*time.Millisecond {
		t.Fatalf("resumed after %s, before the wait was over", elapsed)
	}
	if got := outputOf(ex, "After"); len(got) != 1 || got[0]["after"] != "yes" || got[0]["n"] != 1.0 {
		t.Fatalf("after = %v", got)
	}
	if run := ex.RunData["Pause"][0]; run.Status != domain.NodeRunSuccess {
		t.Fatalf("the wait node run must end as success, is %s", run.Status)
	}
}

func TestRuntimeResumesAWebhookWaitWithItems(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	wf := f.save(t, fmt.Sprintf(waitDoc, `{"resume": "webhook"}`))
	id := f.begin(t, wf, "Start", items(1)...)
	f.status(t, id, domain.StatusWaiting)

	token, err := f.rt.Waiter.PendingToken(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if guess := nodesdk.NewResumeToken(string(id), "Pause/0"); guess == token {
		t.Fatal("resume token must not be guessable")
	}
	if err := f.rt.Waiter.ResumeByToken(context.Background(), string(id)+nodesdk.ResumeTokenSeparator+"guess", nil); !errors.Is(err, waiter.ErrInvalidToken) {
		t.Fatalf("guessed token: %v", err)
	}
	if err := f.rt.Waiter.ResumeByToken(context.Background(), "wrong", nil); !errors.Is(err, waiter.ErrInvalidToken) {
		t.Fatalf("bad token: %v", err)
	}
	err = f.rt.Waiter.ResumeByToken(context.Background(), token, []domain.Item{{JSON: map[string]any{"approved": true}}})
	if err != nil {
		t.Fatal(err)
	}
	ex := f.status(t, id, domain.StatusSuccess)
	if got := outputOf(ex, "After"); len(got) != 1 || got[0]["approved"] != true || got[0]["after"] != "yes" {
		t.Fatalf("after = %v", got)
	}
}

func TestRuntimeCancelsWaitingAndRunningExecutions(t *testing.T) {
	f := newRuntime(t, func(c *config.Config) { c.Nodes.Code.Timeout = time.Minute })
	f.start(t)

	longWait := f.save(t, fmt.Sprintf(waitDoc, `{"resume": "interval", "amount": 1, "unit": "hours"}`))
	waitID := f.begin(t, longWait, "Start", items(1)...)
	f.status(t, waitID, domain.StatusWaiting)
	if err := f.rt.Executions.Cancel(context.Background(), waitID); err != nil {
		t.Fatal(err)
	}
	if ex := f.status(t, waitID, domain.StatusCanceled); ex.Error == nil || ex.Error.Kind != domain.ErrorKindCanceled {
		t.Fatalf("waiting cancel: %+v", ex)
	}

	spin := f.save(t, `{"name": "spin", "nodes": [
	  {"name": "Start", "type": "engi.trigger.manual", "position": [0, 0]},
	  {"name": "Spin", "type": "engi.code.javascript", "position": [200, 0], "parameters": {"jsCode": "while (true) {}"}}
	], "connections": [{"from": {"node": "Start"}, "to": {"node": "Spin"}}]}`)
	spinID := f.begin(t, spin, "Start", items(1)...)
	f.status(t, spinID, domain.StatusRunning)
	if err := f.rt.Executions.Cancel(context.Background(), spinID); err != nil {
		t.Fatal(err)
	}
	f.status(t, spinID, domain.StatusCanceled)
	if err := f.rt.Executions.Cancel(context.Background(), spinID); !errors.Is(err, execution.ErrNotCancelable) {
		t.Fatalf("second cancel: %v", err)
	}
}

func TestRuntimeRecoversAJobWhoseWorkerDied(t *testing.T) {
	f := newRuntime(t)
	wf := f.save(t, pipelineDoc)
	id := f.begin(t, wf, "Start", items(1, 5)...)

	// Another worker claims the job and dies: it never heartbeats or finishes.
	// The lease is an hour, so nothing expires on the wall clock during the test;
	// expiry and recovery are triggered explicitly below.
	deadJob, err := f.rt.queue.Claim(context.Background(), "wk_dead", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deadJob.ExecutionID != id {
		t.Fatalf("claimed %s", deadJob.ExecutionID)
	}
	f.start(t) // the live worker starts while the dead one still holds the lease

	// A live lease is not reclaimed, and the new worker cannot take the job.
	if err := f.rt.Worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if owner := jobLeaseOwner(t, f, deadJob.ID); owner != "wk_dead" {
		t.Fatalf("live lease was reclaimed, lease owner = %q", owner)
	}
	if ex, _ := f.rt.Executions.Get(context.Background(), id); ex.Status != domain.StatusQueued {
		t.Fatalf("the live lease must keep the job away from the new worker, status = %s", ex.Status)
	}

	// The lease runs out: the reclaimer requeues the job and the live worker finishes it.
	expireLease(t, f, deadJob.ID)
	if err := f.rt.Worker.ReclaimOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	ex := f.status(t, id, domain.StatusSuccess)
	for _, node := range []string{"Start", "Set", "Big?"} {
		if runs := ex.RunData[node]; len(runs) != 1 {
			t.Fatalf("%s ran %d times, want once", node, len(runs))
		}
	}
	// The worker deletes the job after it writes the execution's status, so the
	// row can outlive the status by a moment: wait for the deletion, not for a fixed time.
	poll(t, "the finished job to be deleted", func() bool { return jobCount(t, f) == 0 })
}

// jobLeaseOwner returns the worker that holds the job's lease, or "" if none.
func jobLeaseOwner(t *testing.T, f *runtimeFixture, id domain.JobID) string {
	t.Helper()
	var owner sql.NullString
	if err := f.rt.db.QueryRow(`SELECT lease_owner FROM jobs WHERE id = ?`, string(id)).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return owner.String
}

// expireLease moves the job's lease into the past, as if its worker had been
// silent for longer than the lease.
func expireLease(t *testing.T, f *runtimeFixture, id domain.JobID) {
	t.Helper()
	past := sqlitedb.FormatTime(time.Now().Add(-time.Second))
	if _, err := f.rt.db.Exec(`UPDATE jobs SET lease_until = ? WHERE id = ?`, past, string(id)); err != nil {
		t.Fatal(err)
	}
}

func jobCount(t *testing.T, f *runtimeFixture) int {
	t.Helper()
	var n int
	if err := f.rt.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// poll waits until cond holds, for a bounded time.
func poll(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const childDoc = `{
  "name": "child",
  "nodes": [
    {"name": "In", "type": "engi.trigger.executeWorkflow", "position": [0, 0]},
    {"name": "Double", "type": "engi.transform.set", "position": [200, 0], "parameters": {
      "fields": [{"name": "double", "type": "number", "value": "={{ $json.n * 2 }}"}]}}
  ],
  "connections": [{"from": {"node": "In"}, "to": {"node": "Double"}}]
}`

const parentDoc = `{
  "name": "parent",
  "nodes": [
    {"name": "Start", "type": "engi.trigger.manual", "position": [0, 0]},
    {"name": "Call", "type": "engi.flow.executeWorkflow", "position": [200, 0], "parameters": {"workflowId": %q, "waitForResult": %t}},
    {"name": "Done", "type": "engi.transform.set", "position": [400, 0], "parameters": {"fields": [{"name": "done", "type": "boolean", "value": true}]}}
  ],
  "connections": [
    {"from": {"node": "Start"}, "to": {"node": "Call"}},
    {"from": {"node": "Call"}, "to": {"node": "Done"}}
  ]
}`

func TestRuntimeSubworkflowWaitsForTheChildResult(t *testing.T) {
	f := newRuntime(t, func(c *config.Config) { c.Worker.Concurrency = 1 }) // one slot: the child must not need a second one
	f.start(t)
	child := f.save(t, childDoc)
	parent := f.save(t, fmt.Sprintf(parentDoc, string(child.ID), true))
	id := f.begin(t, parent, "Start", items(2, 3)...)

	ex := f.status(t, id, domain.StatusSuccess)
	got := outputOf(ex, "Call")
	if len(got) != 2 || got[0]["double"] != 4.0 || got[1]["double"] != 6.0 {
		t.Fatalf("call output = %v", got)
	}
	if done := outputOf(ex, "Done"); len(done) != 2 || done[0]["done"] != true {
		t.Fatalf("done = %v", done)
	}
	list, err := f.rt.Executions.List(context.Background(), port.ExecutionFilter{WorkflowID: child.ID}, port.Page{Limit: 10})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("child executions = %+v (%v)", list, err)
	}
	c := list.Items[0]
	if c.Mode != domain.ModeSubworkflow || c.Status != domain.StatusSuccess || c.ParentExecutionID == nil || *c.ParentExecutionID != id {
		t.Fatalf("child = %+v", c)
	}
}

func TestRuntimeSubworkflowFireAndForget(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	child := f.save(t, childDoc)
	parent := f.save(t, fmt.Sprintf(parentDoc, string(child.ID), false))
	id := f.begin(t, parent, "Start", items(2)...)

	ex := f.status(t, id, domain.StatusSuccess)
	if got := outputOf(ex, "Call"); len(got) != 1 || got[0]["n"] != 2.0 {
		t.Fatalf("the input must continue unchanged, got %v", got)
	}
	f.until(t, id, "the child to finish", func(domain.Execution) bool {
		list, err := f.rt.Executions.List(context.Background(), port.ExecutionFilter{WorkflowID: child.ID}, port.Page{Limit: 10})
		return err == nil && len(list.Items) == 1 && list.Items[0].Status == domain.StatusSuccess
	})
}

func TestRuntimeSubworkflowDepthLimit(t *testing.T) {
	f := newRuntime(t, func(c *config.Config) { c.Engine.MaxSubworkflowDepth = 2 })
	f.start(t)
	// A workflow that calls itself.
	id, err := f.app.ids.WorkflowID()
	if err != nil {
		t.Fatal(err)
	}
	doc := fmt.Sprintf(`{"name": "loop", "nodes": [
	  {"name": "In", "type": "engi.trigger.executeWorkflow", "position": [0, 0]},
	  {"name": "Again", "type": "engi.flow.executeWorkflow", "position": [200, 0], "parameters": {"workflowId": %q}}
	], "connections": [{"from": {"node": "In"}, "to": {"node": "Again"}}]}`, string(id))
	wf := workflow(t, doc)
	wf.ID = id
	if _, err := f.rt.Workflows.Save(context.Background(), wf); err != nil {
		t.Fatal(err)
	}
	exID := f.begin(t, wf, "In", items(1)...)
	ex := f.status(t, exID, domain.StatusError)
	if ex.Error == nil || ex.Error.Message == "" {
		t.Fatalf("execution = %+v", ex)
	}
	list, _ := f.rt.Executions.List(context.Background(), port.ExecutionFilter{WorkflowID: id}, port.Page{Limit: 50})
	if len(list.Items) != 3 { // depth 0, 1, 2; the call from depth 2 is refused
		t.Fatalf("executions = %d, want 3", len(list.Items))
	}
}

func TestRuntimeDuplicateDedupKey(t *testing.T) {
	f := newRuntime(t)
	wf := f.save(t, pipelineDoc)
	req := execution.StartRequest{WorkflowID: wf.ID, Mode: domain.ModeWebhook, StartNode: "Start", DedupKey: "evt-1"}
	first, err := f.rt.Executions.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.rt.Executions.Start(context.Background(), req)
	if !errors.Is(err, domain.ErrDuplicate) || second != first {
		t.Fatalf("second = %s (want %s), err = %v", second, first, err)
	}
}

func TestRuntimeRunSyncReturnsTheLastNodeOutput(t *testing.T) {
	f := newRuntime(t)
	f.start(t)
	child := f.save(t, childDoc)
	ex, err := f.rt.Executions.RunSync(context.Background(), execution.StartRequest{
		WorkflowID: child.ID, Mode: domain.ModeWebhook, StartNode: "In", Items: items(21),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out := ex.LastOutput(); ex.Status != domain.StatusSuccess || len(out) != 1 || out[0].JSON["double"] != 42.0 {
		t.Fatalf("execution = %+v last = %v", ex.Status, out)
	}
}

func TestRunPersistedGoesThroughTheQueue(t *testing.T) {
	f := newRuntime(t) // only for the config; RunPersisted opens its own runtime
	_ = f.rt.Shutdown(context.Background())
	a := f.app
	wf := workflow(t, pipelineDoc)
	ex, err := a.RunPersisted(context.Background(), RunRequest{Workflow: wf, Items: items(1, 5)})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Status != domain.StatusSuccess {
		t.Fatalf("execution = %+v", ex)
	}
	rep := ReportFromExecution(ex)
	if rep.Status != "success" || rep.Output == nil || len(rep.RunData) != 5 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestRunPersistedFollowsATimedWait(t *testing.T) {
	f := newRuntime(t)
	_ = f.rt.Shutdown(context.Background())
	wf := workflow(t, fmt.Sprintf(waitDoc, `{"resume": "interval", "amount": 0.3, "unit": "seconds"}`))
	ex, err := f.app.RunPersisted(context.Background(), RunRequest{Workflow: wf, Items: items(1)})
	if err != nil || ex.Status != domain.StatusSuccess {
		t.Fatalf("execution = %+v err = %v", ex, err)
	}
}
