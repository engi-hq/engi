package porttest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// TestWebhookEnv is what a persistence adapter provides for the test webhook
// contract: the armed-test store and the workflow store, because armed tests
// refer to workflows.
type TestWebhookEnv struct {
	Tests     port.TestWebhookStore
	Workflows port.WorkflowStore
}

// TestWebhookFactory builds fresh, empty, migrated stores that read time from
// clk. It registers its own cleanup with t.
type TestWebhookFactory func(t *testing.T, clk port.Clock) TestWebhookEnv

// hookTTL is how long the suite's armed tests stay live.
const hookTTL = time.Minute

type hookEnv struct {
	TestWebhookEnv
	fx  *fixtures
	clk *FakeClock
}

// RunTestWebhookSuite runs the armed test webhook contract against stores from newEnv.
func RunTestWebhookSuite(t *testing.T, newEnv TestWebhookFactory) {
	t.Helper()
	cases := map[string]func(*testing.T, *hookEnv){
		"ArmThenClaimOnce":           testHookClaimOnce,
		"FindMatchesMethodAndLength": testHookFind,
		"ExpiredTestsAreInvisible":   testHookExpired,
		"ArmReplacesSameRoute":       testHookReplace,
		"ClaimIsExclusive":           testHookClaimExclusive,
		"DeleteByWorkflowDisarms":    testHookDeleteByWorkflow,
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			clk := NewFakeClock()
			fn(t, &hookEnv{TestWebhookEnv: newEnv(t, clk), fx: newFixtures(t, clk), clk: clk})
		})
	}
}

// arm saves a workflow (armed tests reference it) and arms a test of path.
func (e *hookEnv) arm(t *testing.T, wf domain.WorkflowID, method, path, node string) domain.ArmedTestWebhook {
	t.Helper()
	a := domain.ArmedTestWebhook{WorkflowID: wf, Node: node, Method: method, Path: path, ExpiresAt: e.clk.Now().Add(hookTTL)}
	if err := e.Tests.Arm(context.Background(), a, e.clk.Now()); err != nil {
		t.Fatalf("Arm(%s /%s): %v", method, path, err)
	}
	return a
}

func (e *hookEnv) workflow(t *testing.T) domain.WorkflowID {
	t.Helper()
	w, err := e.Workflows.Save(context.Background(), e.fx.workflow("Armed"))
	if err != nil {
		t.Fatalf("Save workflow: %v", err)
	}
	return w.ID
}

func segmentsOf(path string) int {
	n := 1
	for _, c := range path {
		if c == '/' {
			n++
		}
	}
	return n
}

func testHookClaimOnce(t *testing.T, e *hookEnv) {
	ctx := context.Background()
	wf := e.workflow(t)
	want := e.arm(t, wf, "POST", "try/:x", "In")
	got, err := e.Tests.Claim(ctx, "POST", want.Shape(), e.clk.Now())
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if got.WorkflowID != want.WorkflowID || got.Node != want.Node || got.Method != want.Method || got.Path != want.Path || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("Claim = %+v, want %+v", got, want)
	}
	if _, err := e.Tests.Claim(ctx, "POST", want.Shape(), e.clk.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second Claim = %v, want ErrNotFound", err)
	}
}

func testHookFind(t *testing.T, e *hookEnv) {
	ctx := context.Background()
	wf := e.workflow(t)
	e.arm(t, wf, "POST", "try/:x", "In")
	e.arm(t, wf, "GET", "try/now", "In")
	got, err := e.Tests.Find(ctx, "POST", segmentsOf("try/9"), e.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "try/:x" || got[0].Node != "In" || got[0].WorkflowID != wf {
		t.Fatalf("Find POST = %+v", got)
	}
	if got, _ := e.Tests.Find(ctx, "DELETE", segmentsOf("try/9"), e.clk.Now()); len(got) != 0 {
		t.Fatalf("Find of another method = %+v", got)
	}
	if got, _ := e.Tests.Find(ctx, "POST", 3, e.clk.Now()); len(got) != 0 {
		t.Fatalf("Find of another length = %+v", got)
	}
}

func testHookExpired(t *testing.T, e *hookEnv) {
	ctx := context.Background()
	wf := e.workflow(t)
	a := e.arm(t, wf, "POST", "old", "In")
	e.clk.Advance(hookTTL) // expires at the first instant after its life
	if got, _ := e.Tests.Find(ctx, "POST", 1, e.clk.Now()); len(got) != 0 {
		t.Fatalf("expired test found: %+v", got)
	}
	if _, err := e.Tests.Claim(ctx, "POST", a.Shape(), e.clk.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Claim of expired = %v, want ErrNotFound", err)
	}
	// Arming another test deletes the expired row; the new one is served.
	e.arm(t, wf, "POST", "fresh", "In")
	if got, _ := e.Tests.Find(ctx, "POST", 1, e.clk.Now()); len(got) != 1 || got[0].Path != "fresh" {
		t.Fatalf("after re-arm Find = %+v", got)
	}
}

func testHookReplace(t *testing.T, e *hookEnv) {
	ctx := context.Background()
	wf := e.workflow(t)
	e.arm(t, wf, "POST", "orders/:id", "First")
	e.arm(t, wf, "POST", "orders/:order", "Second") // same shape
	got, err := e.Tests.Find(ctx, "POST", 2, e.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Node != "Second" || got[0].Path != "orders/:order" {
		t.Fatalf("after re-arm Find = %+v, want the second test only", got)
	}
}

func testHookClaimExclusive(t *testing.T, e *hookEnv) {
	ctx := context.Background()
	wf := e.workflow(t)
	a := e.arm(t, wf, "POST", "race", "In")
	const callers = 8
	var wins, misses atomic.Int32
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.Tests.Claim(ctx, "POST", a.Shape(), e.clk.Now())
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, domain.ErrNotFound):
				misses.Add(1)
			default:
				t.Errorf("Claim: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || misses.Load() != callers-1 {
		t.Fatalf("wins = %d, misses = %d; want exactly one winner of %d", wins.Load(), misses.Load(), callers)
	}
}

func testHookDeleteByWorkflow(t *testing.T, e *hookEnv) {
	ctx := context.Background()
	mine, other := e.workflow(t), e.workflow(t)
	a := e.arm(t, mine, "POST", "mine", "In")
	b := e.arm(t, other, "POST", "theirs", "In")
	if err := e.Tests.DeleteByWorkflow(ctx, mine); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Tests.Claim(ctx, "POST", a.Shape(), e.clk.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("disarmed test = %v, want ErrNotFound", err)
	}
	if _, err := e.Tests.Claim(ctx, "POST", b.Shape(), e.clk.Now()); err != nil {
		t.Fatalf("other workflow's test: %v", err)
	}
}
