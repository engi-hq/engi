package porttest

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Stores is the set of ports a persistence adapter provides.
type Stores struct {
	Workflows   port.WorkflowStore
	Versions    port.WorkflowVersionStore
	Executions  port.ExecutionStore
	States      port.ExecutionStateStore
	Credentials port.CredentialStore
	Tags        port.TagStore
	Variables   port.VariableStore
	Tx          port.TxManager
}

// StoreFactory builds fresh, empty, migrated stores that read time from clk.
// It registers its own cleanup with t.
type StoreFactory func(t *testing.T, clk port.Clock) Stores

type storeEnv struct {
	Stores
	fx  *fixtures
	clk *FakeClock
}

// RunStoreSuite runs the store contract tests against stores from newStores.
func RunStoreSuite(t *testing.T, newStores StoreFactory) {
	t.Helper()
	cases := map[string]func(*testing.T, *storeEnv){
		"WorkflowRoundTrip":         testWorkflowRoundTrip,
		"WorkflowNotFound":          testWorkflowNotFound,
		"VersionsAreImmutable":      testVersionsImmutable,
		"VersionPagination":         testVersionPagination,
		"SetActiveKeepsVersion":     testSetActive,
		"WorkflowListFilters":       testWorkflowListFilters,
		"WorkflowPagination":        testWorkflowPagination,
		"WorkflowDeleteCascades":    testWorkflowDelete,
		"ExecutionRoundTrip":        testExecutionRoundTrip,
		"ExecutionNeedsVersion":     testExecutionNeedsVersion,
		"ExecutionDuplicateID":      testExecutionDuplicateID,
		"ExecutionUpdate":           testExecutionUpdate,
		"ExecutionDelete":           testExecutionDelete,
		"ExecutionListFilters":      testExecutionListFilters,
		"ExecutionPagination":       testExecutionPagination,
		"DedupRejectsDuplicates":    testDedup,
		"FindDueWaiting":            testFindDueWaiting,
		"ClaimRunIsExclusive":       testClaimRun,
		"ClaimRunRace":              testClaimRunRace,
		"RequeueRunNeedsHolder":     testRequeueRun,
		"FinishRunIsFenced":         testFinishRunFenced,
		"CrashRunIsFenced":          testCrashRunFenced,
		"ExecutionState":            testExecutionState,
		"PruneByRetention":          testPrune,
		"CredentialLifecycle":       testCredentials,
		"TagLifecycle":              testTags,
		"TagsFollowWorkflows":       testTagsOnWorkflows,
		"VariableLifecycle":         testVariables,
		"TransactionCommit":         testTxCommit,
		"TransactionRollback":       testTxRollback,
		"ConcurrentSavesGetVersion": testConcurrentSaves,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			clk := NewFakeClock()
			fn(t, &storeEnv{Stores: newStores(t, clk), fx: newFixtures(t, clk), clk: clk})
		})
	}
}

func mustSave(t *testing.T, e *storeEnv, w domain.Workflow) domain.Workflow {
	t.Helper()
	saved, err := e.Workflows.Save(context.Background(), w)
	if err != nil {
		t.Fatalf("Save(%s): %v", w.Name, err)
	}
	return saved
}

func mustCreate(t *testing.T, e *storeEnv, x domain.Execution) {
	t.Helper()
	if err := e.Executions.Create(context.Background(), x); err != nil {
		t.Fatalf("Create(%s): %v", x.ID, err)
	}
}

func testWorkflowRoundTrip(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("Order sync", "sales", "prod")
	w.Active = true
	saved := mustSave(t, e, w)
	if saved.Version != 1 {
		t.Fatalf("first save version = %d, want 1", saved.Version)
	}
	got, err := e.Workflows.Get(ctx, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	w.Version = 1
	w.Tags = []string{"prod", "sales"} // stores return tags sorted
	if !reflect.DeepEqual(got.Nodes, w.Nodes) || !reflect.DeepEqual(got.Connections, w.Connections) {
		t.Errorf("graph changed in round trip:\n got %+v\nwant %+v", got, w)
	}
	if got.Name != w.Name || !got.Active || got.Version != 1 || !reflect.DeepEqual(got.Tags, w.Tags) {
		t.Errorf("scalar fields changed: got %+v", got)
	}
}

func testWorkflowNotFound(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	id := e.fx.workflowID()
	if _, err := e.Workflows.Get(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get unknown = %v, want ErrNotFound", err)
	}
	if err := e.Workflows.SetActive(ctx, id, true); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetActive unknown = %v, want ErrNotFound", err)
	}
	if err := e.Workflows.Delete(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete unknown = %v, want ErrNotFound", err)
	}
	if _, err := e.Versions.GetVersion(ctx, id, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetVersion unknown = %v, want ErrNotFound", err)
	}
}

func testVersionsImmutable(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("first")
	mustSave(t, e, w)
	e.clk.Advance(time.Hour)
	w.Name = "second"
	w.Nodes[1].Parameters = map[string]any{"value": "changed"}
	saved := mustSave(t, e, w)
	if saved.Version != 2 {
		t.Fatalf("second save version = %d, want 2", saved.Version)
	}
	v1, err := e.Versions.GetVersion(ctx, w.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Workflow.Name != "first" || v1.Workflow.Version != 1 {
		t.Errorf("version 1 was altered: %+v", v1.Workflow)
	}
	if _, ok := v1.Workflow.Nodes[1].Parameters["n"]; !ok {
		t.Errorf("version 1 parameters were altered: %v", v1.Workflow.Nodes[1].Parameters)
	}
	v2, err := e.Versions.GetVersion(ctx, w.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Workflow.Name != "second" || !v2.CreatedAt.After(v1.CreatedAt) {
		t.Errorf("version 2 wrong: %+v (v1 created %v)", v2, v1.CreatedAt)
	}
	cur, err := e.Workflows.Get(ctx, w.ID)
	if err != nil || cur.Name != "second" || cur.Version != 2 {
		t.Errorf("current = %+v, %v; want second@2", cur, err)
	}
}

func testVersionPagination(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	const n = 5
	for i := range n {
		w.Name = string(rune('a' + i))
		mustSave(t, e, w)
	}
	var seen []int
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > n {
			t.Fatal("pagination did not terminate")
		}
		res, err := e.Versions.ListVersions(ctx, w.ID, port.Page{Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range res.Items {
			seen = append(seen, v.Version)
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if want := []int{5, 4, 3, 2, 1}; !reflect.DeepEqual(seen, want) {
		t.Errorf("versions = %v, want %v", seen, want)
	}
}

func testSetActive(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	if err := e.Workflows.SetActive(ctx, w.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := e.Workflows.Get(ctx, w.ID)
	if err != nil || !got.Active || got.Version != 1 {
		t.Errorf("after SetActive: %+v, %v; want active, version 1", got, err)
	}
	vs, err := e.Versions.ListVersions(ctx, w.ID, port.Page{})
	if err != nil || len(vs.Items) != 1 {
		t.Errorf("versions after SetActive = %d, %v; want 1", len(vs.Items), err)
	}
}

func names(ws []domain.Workflow) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Name
	}
	return out
}

func testWorkflowListFilters(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	on, off := true, false
	a := e.fx.workflow("Order sync", "sales")
	a.Active = true
	b := e.fx.workflow("Invoice export", "finance", "sales")
	c := e.fx.workflow("100%_done", "misc")
	for _, w := range []domain.Workflow{a, b, c} {
		mustSave(t, e, w)
	}
	tests := []struct {
		name   string
		filter port.WorkflowFilter
		want   []string
	}{
		{"all, newest first", port.WorkflowFilter{}, []string{"100%_done", "Invoice export", "Order sync"}},
		{"active", port.WorkflowFilter{Active: &on}, []string{"Order sync"}},
		{"inactive", port.WorkflowFilter{Active: &off}, []string{"100%_done", "Invoice export"}},
		{"tag", port.WorkflowFilter{Tag: "sales"}, []string{"Invoice export", "Order sync"}},
		{"tag miss", port.WorkflowFilter{Tag: "nope"}, []string{}},
		{"name ignores case", port.WorkflowFilter{NameContains: "ORDER"}, []string{"Order sync"}},
		{"name treats wildcards literally", port.WorkflowFilter{NameContains: "%_"}, []string{"100%_done"}},
		{"combined", port.WorkflowFilter{Tag: "sales", Active: &off, NameContains: "export"}, []string{"Invoice export"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := e.Workflows.List(ctx, tt.filter, port.Page{})
			if err != nil {
				t.Fatal(err)
			}
			if got := names(res.Items); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if res.NextCursor != "" {
				t.Errorf("unexpected next cursor %q", res.NextCursor)
			}
		})
	}
}

func testWorkflowPagination(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	const total = 7
	want := map[string]bool{}
	for i := range total {
		e.clk.Advance(time.Millisecond * 2) // distinct ULID timestamps
		w := e.fx.workflow(string(rune('a' + i)))
		mustSave(t, e, w)
		want[w.Name] = true
	}
	var seen []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > total {
			t.Fatal("pagination did not terminate")
		}
		res, err := e.Workflows.List(ctx, port.WorkflowFilter{}, port.Page{Cursor: cursor, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) > 3 {
			t.Fatalf("page has %d items, limit is 3", len(res.Items))
		}
		seen = append(seen, names(res.Items)...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if want := []string{"g", "f", "e", "d", "c", "b", "a"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("paged order = %v, want %v", seen, want)
	}
	if _, err := e.Workflows.List(ctx, port.WorkflowFilter{}, port.Page{Cursor: "!!not-a-cursor"}); err == nil {
		t.Error("garbage cursor was accepted")
	}
}

func testWorkflowDelete(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	x := e.fx.execution(w.ID, 1)
	mustCreate(t, e, x)
	if err := e.Workflows.Delete(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Executions.Get(ctx, x.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("execution survived workflow delete: %v", err)
	}
	if _, err := e.Versions.GetVersion(ctx, w.ID, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("version survived workflow delete: %v", err)
	}
}

func sampleRunData() map[string][]domain.NodeRun {
	return map[string][]domain.NodeRun{
		"Start": {{
			RunIndex: 0, Status: domain.NodeRunSuccess, Duration: 120 * time.Millisecond, Attempts: 1,
			Output: domain.PortData{domain.PortMain: {{{JSON: map[string]any{"id": float64(1)}}}}},
		}},
	}
}

func testExecutionRoundTrip(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	parent := e.fx.executionID()
	x := e.fx.execution(w.ID, 1)
	x.Mode = domain.ModeWebhook
	x.DedupKey = "k1"
	x.ParentExecutionID = &parent
	x.RunData = sampleRunData()
	x.Error = &domain.ErrorInfo{Kind: domain.ErrorKindNode, Node: "Set", Message: "boom"}
	mustCreate(t, e, x)
	got, err := e.Executions.Get(ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StartedAt.Equal(x.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, x.StartedAt)
	}
	if got.ID != x.ID || got.WorkflowID != x.WorkflowID || got.WorkflowVersion != 1 || got.Mode != x.Mode ||
		got.Status != x.Status || got.DedupKey != "k1" || got.FinishedAt != nil || got.WaitUntil != nil {
		t.Errorf("scalar fields changed: %+v", got)
	}
	if got.ParentExecutionID == nil || *got.ParentExecutionID != parent {
		t.Errorf("ParentExecutionID = %v, want %s", got.ParentExecutionID, parent)
	}
	if got.Error == nil || *got.Error != *x.Error {
		t.Errorf("Error = %+v, want %+v", got.Error, x.Error)
	}
	run := got.RunData["Start"]
	wantItem := x.RunData["Start"][0].Output[domain.PortMain][0][0]
	if len(run) != 1 || run[0].Duration != 120*time.Millisecond || run[0].Status != domain.NodeRunSuccess ||
		!reflect.DeepEqual(run[0].Output[domain.PortMain][0][0].JSON, wantItem.JSON) {
		t.Errorf("RunData changed: %+v", got.RunData)
	}
	if _, err := e.Executions.Get(ctx, e.fx.executionID()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get unknown = %v, want ErrNotFound", err)
	}
}

func testExecutionNeedsVersion(t *testing.T, e *storeEnv) {
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	err := e.Executions.Create(context.Background(), e.fx.execution(w.ID, 99))
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Create with unknown version = %v, want ErrNotFound", err)
	}
}

func testExecutionDuplicateID(t *testing.T, e *storeEnv) {
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	x := e.fx.execution(w.ID, 1)
	mustCreate(t, e, x)
	if err := e.Executions.Create(context.Background(), x); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("second Create = %v, want ErrAlreadyExists", err)
	}
}

func testExecutionUpdate(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	x := e.fx.execution(w.ID, 1)
	mustCreate(t, e, x)
	fin := e.clk.Now().Add(time.Minute)
	x.Status = domain.StatusError
	x.FinishedAt = &fin
	x.RunData = sampleRunData()
	x.Error = &domain.ErrorInfo{Kind: domain.ErrorKindWorkflow, Message: "bad"}
	if err := e.Executions.Update(ctx, x); err != nil {
		t.Fatal(err)
	}
	got, err := e.Executions.Get(ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusError || got.FinishedAt == nil || !got.FinishedAt.Equal(fin) ||
		got.Error == nil || got.Error.Message != "bad" || len(got.RunData["Start"]) != 1 {
		t.Errorf("update not persisted: %+v", got)
	}
	x.ID = e.fx.executionID()
	if err := e.Executions.Update(ctx, x); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update unknown = %v, want ErrNotFound", err)
	}
}

func ids(xs []domain.Execution) []domain.ExecutionID {
	out := make([]domain.ExecutionID, len(xs))
	for i, x := range xs {
		out[i] = x.ID
	}
	return out
}

func testExecutionListFilters(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w1, w2 := e.fx.workflow("one"), e.fx.workflow("two")
	mustSave(t, e, w1)
	mustSave(t, e, w2)
	t0 := e.clk.Now()
	mk := func(w domain.Workflow, st domain.ExecutionStatus, at time.Duration) domain.Execution {
		e.clk.Advance(2 * time.Millisecond)
		x := e.fx.execution(w.ID, 1)
		x.Status, x.StartedAt = st, t0.Add(at)
		mustCreate(t, e, x)
		return x
	}
	a := mk(w1, domain.StatusSuccess, 0)
	b := mk(w1, domain.StatusError, time.Hour)
	c := mk(w2, domain.StatusSuccess, 2*time.Hour)
	d := mk(w1, domain.StatusRunning, 3*time.Hour)
	tests := []struct {
		name   string
		filter port.ExecutionFilter
		want   []domain.ExecutionID
	}{
		{"all newest first", port.ExecutionFilter{}, []domain.ExecutionID{d.ID, c.ID, b.ID, a.ID}},
		{"workflow", port.ExecutionFilter{WorkflowID: w1.ID}, []domain.ExecutionID{d.ID, b.ID, a.ID}},
		{"status", port.ExecutionFilter{Statuses: []domain.ExecutionStatus{domain.StatusSuccess}}, []domain.ExecutionID{c.ID, a.ID}},
		{"several statuses", port.ExecutionFilter{Statuses: []domain.ExecutionStatus{domain.StatusError, domain.StatusRunning}}, []domain.ExecutionID{d.ID, b.ID}},
		{"started after is inclusive", port.ExecutionFilter{StartedAfter: t0.Add(time.Hour)}, []domain.ExecutionID{d.ID, c.ID, b.ID}},
		{"started before is exclusive", port.ExecutionFilter{StartedBefore: t0.Add(time.Hour)}, []domain.ExecutionID{a.ID}},
		{"combined", port.ExecutionFilter{WorkflowID: w1.ID, Statuses: []domain.ExecutionStatus{domain.StatusSuccess}, StartedBefore: t0.Add(time.Hour)}, []domain.ExecutionID{a.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := e.Executions.List(ctx, tt.filter, port.Page{})
			if err != nil {
				t.Fatal(err)
			}
			got := ids(res.Items)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			for _, x := range res.Items {
				if x.RunData != nil {
					t.Errorf("List returned RunData for %s", x.ID)
				}
			}
		})
	}
}

func testExecutionPagination(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	var want []domain.ExecutionID
	for range 7 {
		e.clk.Advance(2 * time.Millisecond)
		x := e.fx.execution(w.ID, 1)
		mustCreate(t, e, x)
		want = append([]domain.ExecutionID{x.ID}, want...)
	}
	var got []domain.ExecutionID
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 7 {
			t.Fatal("pagination did not terminate")
		}
		res, err := e.Executions.List(ctx, port.ExecutionFilter{WorkflowID: w.ID}, port.Page{Cursor: cursor, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ids(res.Items)...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paged ids = %v, want %v", got, want)
	}
}

func testDedup(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w1, w2 := e.fx.workflow("one"), e.fx.workflow("two")
	mustSave(t, e, w1)
	mustSave(t, e, w2)
	first := e.fx.execution(w1.ID, 1)
	first.DedupKey = "evt-1"
	mustCreate(t, e, first)

	dup := e.fx.execution(w1.ID, 1)
	dup.DedupKey = "evt-1"
	err := e.Executions.Create(ctx, dup)
	if !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate Create = %v, want ErrDuplicate", err)
	}
	var derr *domain.DuplicateError
	if !errors.As(err, &derr) || derr.ExistingID != first.ID || derr.DedupKey != "evt-1" || derr.WorkflowID != w1.ID {
		t.Errorf("DuplicateError = %+v, want existing %s", derr, first.ID)
	}
	if _, err := e.Executions.Get(ctx, dup.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("rejected duplicate was stored: %v", err)
	}

	other := e.fx.execution(w2.ID, 1)
	other.DedupKey = "evt-1"
	if err := e.Executions.Create(ctx, other); err != nil {
		t.Errorf("same key in another workflow rejected: %v", err)
	}
	for range 2 { // executions without a key never collide
		mustCreate(t, e, e.fx.execution(w1.ID, 1))
	}
}

func testFindDueWaiting(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	now := e.clk.Now()
	mk := func(st domain.ExecutionStatus, until *time.Time) domain.Execution {
		e.clk.Advance(2 * time.Millisecond)
		x := e.fx.execution(w.ID, 1)
		x.Status, x.WaitUntil = st, until
		mustCreate(t, e, x)
		return x
	}
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	late := mk(domain.StatusWaiting, at(-time.Minute))
	early := mk(domain.StatusWaiting, at(-time.Hour))
	mk(domain.StatusWaiting, at(time.Hour))  // not due yet
	mk(domain.StatusWaiting, nil)            // waiting for a webhook, no deadline
	mk(domain.StatusRunning, at(-time.Hour)) // wrong status
	due, err := e.Executions.FindDueWaiting(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []domain.ExecutionID{early.ID, late.ID}; !reflect.DeepEqual(ids(due), want) {
		t.Errorf("due = %v, want %v", ids(due), want)
	}
	limited, err := e.Executions.FindDueWaiting(ctx, now, 1)
	if err != nil || len(limited) != 1 || limited[0].ID != early.ID {
		t.Errorf("limit 1 = %v, %v; want [%s]", ids(limited), err, early.ID)
	}
}

func testExecutionState(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	x := e.fx.execution(w.ID, 1)
	mustCreate(t, e, x)
	if _, err := e.States.LoadState(ctx, x.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("LoadState before save = %v, want ErrNotFound", err)
	}
	if err := e.States.SaveState(ctx, x.ID, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := e.States.SaveState(ctx, x.ID, []byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	got, err := e.States.LoadState(ctx, x.ID)
	if err != nil || string(got) != `{"a":2}` {
		t.Errorf("LoadState = %q, %v; want latest", got, err)
	}
	if err := e.States.DeleteState(ctx, x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.States.LoadState(ctx, x.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("LoadState after delete = %v, want ErrNotFound", err)
	}
	if err := e.States.SaveState(ctx, e.fx.executionID(), []byte("{}")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SaveState unknown execution = %v, want ErrNotFound", err)
	}
}

func testPrune(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	now := e.clk.Now()
	mk := func(st domain.ExecutionStatus, age time.Duration) domain.Execution {
		e.clk.Advance(2 * time.Millisecond)
		x := e.fx.execution(w.ID, 1)
		x.Status, x.StartedAt = st, now.Add(-age)
		if st.Terminal() {
			fin := now.Add(-age)
			x.FinishedAt = &fin
		}
		mustCreate(t, e, x)
		return x
	}
	oldOK := mk(domain.StatusSuccess, 48*time.Hour)
	newOK := mk(domain.StatusSuccess, time.Hour)
	oldErr := mk(domain.StatusError, 48*time.Hour)
	oldRunning := mk(domain.StatusRunning, 48*time.Hour)
	oldWaiting := mk(domain.StatusWaiting, 48*time.Hour)
	cutoffs := map[domain.ExecutionStatus]time.Time{
		domain.StatusSuccess: now.Add(-24 * time.Hour),
		domain.StatusRunning: now.Add(-24 * time.Hour), // non-terminal: must be ignored
		domain.StatusWaiting: now.Add(-24 * time.Hour),
	} // StatusError has no cutoff: kept
	n, err := e.Executions.Prune(ctx, cutoffs)
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v; want 1", n, err)
	}
	if _, err := e.Executions.Get(ctx, oldOK.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("old success survived: %v", err)
	}
	for _, x := range []domain.Execution{newOK, oldErr, oldRunning, oldWaiting} {
		if _, err := e.Executions.Get(ctx, x.ID); err != nil {
			t.Errorf("execution %s (%s) wrongly pruned: %v", x.ID, x.Status, err)
		}
	}
	cutoffs[domain.StatusError] = now.Add(-24 * time.Hour)
	if n, err := e.Executions.Prune(ctx, cutoffs); err != nil || n != 1 {
		t.Errorf("second Prune = %d, %v; want 1", n, err)
	}
	if n, err := e.Executions.Prune(ctx, cutoffs); err != nil || n != 0 {
		t.Errorf("third Prune = %d, %v; want 0 (idempotent)", n, err)
	}
}

func testCredentials(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	c := domain.Credential{ID: e.fx.credentialID(), Name: "Prod PG", Type: "engi.postgres", KeyID: "k1", Data: []byte{0, 1, 2, 255}}
	if err := e.Credentials.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := e.Credentials.Create(ctx, c); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("duplicate Create = %v, want ErrAlreadyExists", err)
	}
	got, err := e.Credentials.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != c.Name || got.Type != c.Type || got.KeyID != "k1" || !reflect.DeepEqual(got.Data, c.Data) || got.CreatedAt.IsZero() {
		t.Errorf("Get = %+v", got)
	}
	e.clk.Advance(time.Hour)
	c.Name, c.KeyID, c.Data = "Renamed", "k2", []byte("new")
	if err := e.Credentials.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	got2, _ := e.Credentials.Get(ctx, c.ID)
	if got2.Name != "Renamed" || got2.KeyID != "k2" || string(got2.Data) != "new" ||
		!got2.CreatedAt.Equal(got.CreatedAt) || !got2.UpdatedAt.After(got.UpdatedAt) {
		t.Errorf("after Update = %+v (before %+v)", got2, got)
	}
	res, err := e.Credentials.List(ctx, port.Page{})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("List = %v, %v", res.Items, err)
	}
	if len(res.Items[0].Data) != 0 {
		t.Error("List returned ciphertext")
	}
	if err := e.Credentials.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Credentials.Get(ctx, c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	if err := e.Credentials.Update(ctx, c); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update after delete = %v, want ErrNotFound", err)
	}
}

func testTxCommit(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	x := e.fx.execution(w.ID, 1)
	err := e.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := e.Workflows.Save(ctx, w); err != nil {
			return err
		}
		// A nested transaction joins the outer one.
		return e.Tx.WithinTx(ctx, func(ctx context.Context) error { return e.Executions.Create(ctx, x) })
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Executions.Get(ctx, x.ID); err != nil {
		t.Errorf("committed execution missing: %v", err)
	}
}

func testTxRollback(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	x := e.fx.execution(w.ID, 1)
	boom := errors.New("boom")
	err := e.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := e.Workflows.Save(ctx, w); err != nil {
			return err
		}
		if err := e.Executions.Create(ctx, x); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithinTx = %v, want boom", err)
	}
	if _, err := e.Workflows.Get(ctx, w.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("workflow survived rollback: %v", err)
	}
	if _, err := e.Executions.Get(ctx, x.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("execution survived rollback: %v", err)
	}
}

func testConcurrentSaves(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	const n = 8
	errs := make(chan error, n)
	for range n {
		go func() {
			_, err := e.Workflows.Save(ctx, w)
			errs <- err
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Save: %v", err)
		}
	}
	got, err := e.Workflows.Get(ctx, w.ID)
	if err != nil || got.Version != n+1 {
		t.Errorf("version after %d concurrent saves = %d, %v; want %d", n, got.Version, err, n+1)
	}
	vs, err := e.Versions.ListVersions(ctx, w.ID, port.Page{Limit: 100})
	if err != nil || len(vs.Items) != n+1 {
		t.Errorf("versions = %d, %v; want %d", len(vs.Items), err, n+1)
	}
}

func testExecutionDelete(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("w")
	mustSave(t, e, w)
	x := e.fx.execution(w.ID, 1)
	mustCreate(t, e, x)
	if err := e.States.SaveState(ctx, x.ID, []byte(`{"s":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := e.Executions.Delete(ctx, x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Executions.Get(ctx, x.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	if _, err := e.States.LoadState(ctx, x.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("LoadState after delete = %v, want ErrNotFound", err)
	}
	if err := e.Executions.Delete(ctx, x.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}
}

func testTags(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	a := domain.Tag{ID: e.fx.tagID(), Name: "prod"}
	b := domain.Tag{ID: e.fx.tagID(), Name: "billing"}
	for _, tag := range []domain.Tag{a, b} {
		if err := e.Tags.Create(ctx, tag); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Tags.Create(ctx, domain.Tag{ID: e.fx.tagID(), Name: "prod"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("duplicate name = %v, want ErrAlreadyExists", err)
	}
	res, err := e.Tags.List(ctx, port.Page{Limit: 1})
	if err != nil || len(res.Items) != 1 || res.Items[0].Name != "billing" || res.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", res, err)
	}
	res, err = e.Tags.List(ctx, port.Page{Cursor: res.NextCursor, Limit: 1})
	if err != nil || len(res.Items) != 1 || res.Items[0].Name != "prod" || res.NextCursor != "" {
		t.Fatalf("second page = %+v, %v", res, err)
	}
	if err := e.Tags.Rename(ctx, a.ID, "billing"); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("rename onto existing = %v, want ErrAlreadyExists", err)
	}
	if err := e.Tags.Rename(ctx, a.ID, "live"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Tags.Get(ctx, a.ID); got.Name != "live" || got.CreatedAt.IsZero() {
		t.Errorf("after rename = %+v", got)
	}
	if err := e.Tags.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"Get":    func() error { _, err := e.Tags.Get(ctx, a.ID); return err }(),
		"Rename": e.Tags.Rename(ctx, a.ID, "x"),
		"Delete": e.Tags.Delete(ctx, a.ID),
	} {
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s after delete = %v, want ErrNotFound", name, err)
		}
	}
}

// testTagsOnWorkflows checks that tags saved with a workflow are listed, and
// that renaming or deleting a tag changes the workflows that carry it.
func testTagsOnWorkflows(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w1 := mustSave(t, e, e.fx.workflow("one", "prod", "billing"))
	w2 := mustSave(t, e, e.fx.workflow("two", "billing"))
	res, err := e.Tags.List(ctx, port.Page{})
	if err != nil || len(res.Items) != 2 {
		t.Fatalf("tags = %+v, %v", res.Items, err)
	}
	id := map[string]domain.TagID{}
	for _, tag := range res.Items {
		id[tag.Name] = tag.ID
	}
	// Renaming onto a name a workflow already carries must not duplicate it.
	if err := e.Tags.Rename(ctx, id["billing"], "finance"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Workflows.Get(ctx, w1.ID); !reflect.DeepEqual(got.Tags, []string{"finance", "prod"}) {
		t.Errorf("tags after rename = %v", got.Tags)
	}
	list, err := e.Workflows.List(ctx, port.WorkflowFilter{Tag: "finance"}, port.Page{})
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("filter by renamed tag = %v, %v", names(list.Items), err)
	}
	if err := e.Tags.Delete(ctx, id["billing"]); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Workflows.Get(ctx, w2.ID); len(got.Tags) != 0 {
		t.Errorf("tags after delete = %v", got.Tags)
	}
	if list, _ = e.Workflows.List(ctx, port.WorkflowFilter{Tag: "finance"}, port.Page{}); len(list.Items) != 0 {
		t.Errorf("deleted tag still filters %v", names(list.Items))
	}
}

func testVariables(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	v := domain.Variable{ID: e.fx.variableID(), Key: "REGION", Value: []byte("eu")}
	s := domain.Variable{ID: e.fx.variableID(), Key: "API_TOKEN", Secret: true, KeyID: "k1", Value: []byte{0, 1, 255}}
	for _, x := range []domain.Variable{v, s} {
		if err := e.Variables.Create(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Variables.Create(ctx, domain.Variable{ID: e.fx.variableID(), Key: "REGION", Value: []byte("x")}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("duplicate key = %v, want ErrAlreadyExists", err)
	}
	if err := e.Variables.Create(ctx, v); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("duplicate id = %v, want ErrAlreadyExists", err)
	}
	got, err := e.Variables.Get(ctx, s.ID)
	if err != nil || !got.Secret || got.KeyID != "k1" || !reflect.DeepEqual(got.Value, s.Value) || got.CreatedAt.IsZero() {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	res, err := e.Variables.List(ctx, port.Page{})
	if err != nil || len(res.Items) != 2 || res.Items[0].Key != "API_TOKEN" || res.Items[1].Key != "REGION" {
		t.Fatalf("List = %+v, %v", res.Items, err)
	}
	e.clk.Advance(time.Hour)
	v.Key, v.Value = "ZONE", []byte("b")
	if err := e.Variables.Update(ctx, v); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Variables.Get(ctx, v.ID); got.Key != "ZONE" || string(got.Value) != "b" || !got.UpdatedAt.After(got.CreatedAt) {
		t.Errorf("after Update = %+v", got)
	}
	v.Key = "API_TOKEN"
	if err := e.Variables.Update(ctx, v); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Errorf("Update onto existing key = %v, want ErrAlreadyExists", err)
	}
	if err := e.Variables.Delete(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Variables.Get(ctx, v.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get after delete = %v, want ErrNotFound", err)
	}
	if err := e.Variables.Update(ctx, v); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update after delete = %v, want ErrNotFound", err)
	}
}

func testClaimRun(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("claims")
	mustSave(t, e, w)
	x := e.fx.execution(w.ID, 1)
	mustCreate(t, e, x)
	jobA, jobB := e.fx.jobID(), e.fx.jobID()
	holderA := domain.RunHolder{JobID: jobA, Attempt: 1}
	at := e.clk.Now().Add(time.Minute)

	got, claimed, err := e.Executions.ClaimRun(ctx, x.ID, holderA, at)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v; want claimed", claimed, err)
	}
	if got.Status != domain.StatusRunning || got.Attempts != 1 || got.LastRecoveredAt != nil {
		t.Errorf("first claim returned %+v; want running, 1 attempt, no recovery", got)
	}

	// Another job cannot claim a running execution it does not hold.
	got, claimed, err = e.Executions.ClaimRun(ctx, x.ID, domain.RunHolder{JobID: jobB, Attempt: 1}, at)
	if err != nil || claimed {
		t.Fatalf("racing claim = %v, %v; want refused", claimed, err)
	}
	if got.Status != domain.StatusRunning || got.Attempts != 1 {
		t.Errorf("refused claim returned %+v; want the stored running execution", got)
	}

	// The holder taking over its own run at a later attempt counts as a recovery.
	// Re-claiming at the same or an earlier attempt is refused.
	if _, claimed, err := e.Executions.ClaimRun(ctx, x.ID, holderA, at); err != nil || claimed {
		t.Errorf("same-attempt claim = %v, %v; want refused", claimed, err)
	}
	got, claimed, err = e.Executions.ClaimRun(ctx, x.ID, domain.RunHolder{JobID: jobA, Attempt: 2}, at)
	if err != nil || !claimed {
		t.Fatalf("takeover = %v, %v; want claimed", claimed, err)
	}
	if got.Attempts != 2 || got.LastRecoveredAt == nil || !got.LastRecoveredAt.Equal(at) {
		t.Errorf("takeover returned %+v; want 2 attempts and recovery at %v", got, at)
	}

	// Waiting executions are never claimed.
	x.Status = domain.StatusWaiting
	if err := e.Executions.Update(ctx, x); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := e.Executions.ClaimRun(ctx, x.ID, domain.RunHolder{JobID: jobA, Attempt: 3}, at); err != nil || claimed {
		t.Errorf("claim of a waiting execution = %v, %v; want refused", claimed, err)
	}

	if _, _, err := e.Executions.ClaimRun(ctx, e.fx.executionID(), holderA, at); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("claim of an unknown execution = %v, want ErrNotFound", err)
	}
}

func testRequeueRun(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("requeues")
	mustSave(t, e, w)
	x := e.fx.execution(w.ID, 1)
	mustCreate(t, e, x)
	job, stranger := e.fx.jobID(), e.fx.jobID()
	first := domain.RunHolder{JobID: job, Attempt: 1}
	second := domain.RunHolder{JobID: job, Attempt: 2}
	if _, claimed, err := e.Executions.ClaimRun(ctx, x.ID, first, e.clk.Now()); err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	recovered := e.clk.Now().Add(time.Second)

	requeued, err := e.Executions.RequeueRun(ctx, x.ID, domain.RunHolder{JobID: stranger, Attempt: 1}, &recovered)
	if err != nil || requeued {
		t.Fatalf("requeue by a job that does not hold the run = %v, %v; want refused", requeued, err)
	}
	if got, _ := e.Executions.Get(ctx, x.ID); got.Status != domain.StatusRunning || got.LastRecoveredAt != nil {
		t.Errorf("refused requeue changed the run: %+v", got)
	}

	requeued, err = e.Executions.RequeueRun(ctx, x.ID, first, &recovered)
	if err != nil || !requeued {
		t.Fatalf("requeue by the holder = %v, %v; want requeued", requeued, err)
	}
	got, _ := e.Executions.Get(ctx, x.ID)
	if got.Status != domain.StatusQueued || got.LastRecoveredAt == nil || !got.LastRecoveredAt.Equal(recovered) {
		t.Errorf("requeued execution = %+v; want queued with recovery at %v", got, recovered)
	}

	// Fencing: after the job takes the run over at attempt 2, a requeue from
	// attempt 1 (a reclaim that raced with the takeover) must not reset it.
	if _, claimed, err := e.Executions.ClaimRun(ctx, x.ID, second, e.clk.Now()); err != nil || !claimed {
		t.Fatalf("claim at attempt 2 = %v, %v", claimed, err)
	}
	if requeued, err := e.Executions.RequeueRun(ctx, x.ID, first, nil); err != nil || requeued {
		t.Fatalf("stale requeue after takeover = %v, %v; want refused", requeued, err)
	}
	if got, _ := e.Executions.Get(ctx, x.ID); got.Status != domain.StatusRunning {
		t.Fatalf("stale requeue reset the run: status %s", got.Status)
	}

	// A plain requeue by the current holder leaves the recovery time alone.
	if requeued, err := e.Executions.RequeueRun(ctx, x.ID, second, nil); err != nil || !requeued {
		t.Fatalf("plain requeue = %v, %v", requeued, err)
	}
	if got, _ := e.Executions.Get(ctx, x.ID); got.LastRecoveredAt == nil || !got.LastRecoveredAt.Equal(recovered) {
		t.Errorf("plain requeue changed the recovery time: %v", got.LastRecoveredAt)
	}

	// Update is a read-modify-write: a stale copy cannot undo the attempts.
	stale, _ := e.Executions.Get(ctx, x.ID)
	stale.Attempts = 0
	stale.Status = domain.StatusError
	if err := e.Executions.Update(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Executions.Get(ctx, x.ID); got.Attempts != 2 {
		t.Errorf("Update wrote Attempts: got %d, want 2", got.Attempts)
	}
}

// testClaimRunRace starts several jobs at once for one queued execution: the
// store must let exactly one of them win the claim.
func testClaimRunRace(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("race")
	mustSave(t, e, w)
	x := e.fx.execution(w.ID, 1)
	mustCreate(t, e, x)

	const racers = 8
	var wins atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	start := make(chan struct{})
	for range racers {
		holder := domain.RunHolder{JobID: e.fx.jobID(), Attempt: 1}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, claimed, err := e.Executions.ClaimRun(ctx, x.ID, holder, e.clk.Now())
			if err != nil {
				errs <- err
				return
			}
			if claimed {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("claim: %v", err)
	}
	if n := wins.Load(); n != 1 {
		t.Fatalf("claims won = %d, want exactly 1", n)
	}
	if got, _ := e.Executions.Get(ctx, x.ID); got.Attempts != 1 || got.Status != domain.StatusRunning {
		t.Errorf("after the race: %+v; want one attempt and running", got)
	}
}

// testFinishRunFenced checks that a result is written only by the holder of a
// running execution, and that a newer claim blocks an older holder.
func testFinishRunFenced(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("fenced results")
	mustSave(t, e, w)
	x := e.fx.execution(w.ID, 1)
	mustCreate(t, e, x)
	job := e.fx.jobID()
	first := domain.RunHolder{JobID: job, Attempt: 1}
	second := domain.RunHolder{JobID: job, Attempt: 2}
	if _, claimed, err := e.Executions.ClaimRun(ctx, x.ID, first, e.clk.Now()); err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}

	result := x
	result.Status = domain.StatusError
	result.Error = &domain.ErrorInfo{Kind: domain.ErrorKindWorkflow, Message: "zombie"}
	result.RunData = sampleRunData()
	fin := e.clk.Now().Add(time.Minute)
	result.FinishedAt = &fin

	// A holder that is not the stored one is refused and writes nothing.
	if written, err := e.Executions.FinishRun(ctx, result, domain.RunHolder{JobID: e.fx.jobID(), Attempt: 1}); err != nil || written {
		t.Fatalf("finish by a stranger = %v, %v; want refused", written, err)
	}
	if got, _ := e.Executions.Get(ctx, x.ID); got.Status != domain.StatusRunning || got.Error != nil || len(got.RunData) != 0 {
		t.Fatalf("refused finish changed the execution: %+v", got)
	}

	// After a takeover at attempt 2, the attempt-1 zombie is refused.
	if _, claimed, err := e.Executions.ClaimRun(ctx, x.ID, second, e.clk.Now()); err != nil || !claimed {
		t.Fatalf("takeover = %v, %v", claimed, err)
	}
	if written, err := e.Executions.FinishRun(ctx, result, first); err != nil || written {
		t.Fatalf("zombie finish after takeover = %v, %v; want refused", written, err)
	}
	if got, _ := e.Executions.Get(ctx, x.ID); got.Status != domain.StatusRunning {
		t.Fatalf("zombie finish changed the status to %s", got.Status)
	}

	// The current holder writes the result once; the run is then ended for everyone.
	if written, err := e.Executions.FinishRun(ctx, result, second); err != nil || !written {
		t.Fatalf("finish by the holder = %v, %v; want written", written, err)
	}
	got, _ := e.Executions.Get(ctx, x.ID)
	if got.Status != domain.StatusError || got.Error == nil || got.Error.Message != "zombie" || len(got.RunData["Start"]) != 1 {
		t.Errorf("stored result = %+v; want the holder's error and run data", got)
	}
	if written, err := e.Executions.FinishRun(ctx, result, second); err != nil || written {
		t.Errorf("second finish = %v, %v; want refused once ended", written, err)
	}

	// A queued execution (requeued, nobody holds it) is not finished by a result.
	y := e.fx.execution(w.ID, 1)
	mustCreate(t, e, y)
	if _, claimed, err := e.Executions.ClaimRun(ctx, y.ID, first, e.clk.Now()); err != nil || !claimed {
		t.Fatalf("claim y = %v, %v", claimed, err)
	}
	if requeued, err := e.Executions.RequeueRun(ctx, y.ID, first, nil); err != nil || !requeued {
		t.Fatalf("requeue y = %v, %v", requeued, err)
	}
	y.Status = domain.StatusSuccess
	if written, err := e.Executions.FinishRun(ctx, y, first); err != nil || written {
		t.Errorf("finish of a queued execution = %v, %v; want refused", written, err)
	}
}

// testCrashRunFenced checks that a crash may end a queued execution or one held
// by the crashing holder, but never one a newer claim holds or a finished one.
func testCrashRunFenced(t *testing.T, e *storeEnv) {
	ctx := context.Background()
	w := e.fx.workflow("fenced crashes")
	mustSave(t, e, w)
	crashed := func(x domain.Execution) domain.Execution {
		x.Status = domain.StatusCrashed
		now := e.clk.Now()
		x.FinishedAt = &now
		x.Error = &domain.ErrorInfo{Kind: domain.ErrorKindWorkflow, Message: "crashed"}
		return x
	}
	holderA := domain.RunHolder{JobID: e.fx.jobID(), Attempt: 1}
	holderB := domain.RunHolder{JobID: e.fx.jobID(), Attempt: 1}

	// Queued: no claim holds it, so a crash lands.
	q := e.fx.execution(w.ID, 1)
	mustCreate(t, e, q)
	if written, err := e.Executions.CrashRun(ctx, crashed(q), holderA); err != nil || !written {
		t.Fatalf("crash of a queued execution = %v, %v; want written", written, err)
	}

	// Running under another holder: refused.
	r := e.fx.execution(w.ID, 1)
	mustCreate(t, e, r)
	if _, claimed, err := e.Executions.ClaimRun(ctx, r.ID, holderB, e.clk.Now()); err != nil || !claimed {
		t.Fatalf("claim r = %v, %v", claimed, err)
	}
	if written, err := e.Executions.CrashRun(ctx, crashed(r), holderA); err != nil || written {
		t.Fatalf("crash by another holder = %v, %v; want refused", written, err)
	}
	if got, _ := e.Executions.Get(ctx, r.ID); got.Status != domain.StatusRunning {
		t.Fatalf("refused crash changed the status to %s", got.Status)
	}
	if written, err := e.Executions.CrashRun(ctx, crashed(r), holderB); err != nil || !written {
		t.Fatalf("crash by the holder = %v, %v; want written", written, err)
	}

	// Ended: refused.
	if written, err := e.Executions.CrashRun(ctx, crashed(r), holderB); err != nil || written {
		t.Fatalf("crash of an ended execution = %v, %v; want refused", written, err)
	}
}
