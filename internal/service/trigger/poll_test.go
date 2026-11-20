package trigger_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

const fakePollType = "test.poll"

// fakePoll emits one item per poll; its cursor is the number of polls so far.
type fakePoll struct{ rec *pollRecorder }

type pollRecorder struct {
	mu      sync.Mutex
	cursors []string
	done    int
	fail    bool
	polled  chan struct{}
}

func (f fakePoll) Describe() nodesdk.Descriptor {
	return nodesdk.Descriptor{
		Type: fakePollType, Version: 1, DisplayName: "Fake poll", Group: nodesdk.GroupTrigger,
		Outputs:     []nodesdk.PortSpec{{Name: "main"}},
		Credentials: []nodesdk.CredentialRef{{Name: "cred", Type: "test", Required: true}},
	}
}

func (fakePoll) Execute(context.Context, nodesdk.Input) (nodesdk.Output, error) {
	return nodesdk.Output{}, nil
}

func (fakePoll) CheckPoll(params map[string]any) (nodesdk.PollSpec, error) {
	d := 20 * time.Second
	if v, ok := params["interval"].(time.Duration); ok {
		d = v
	}
	return nodesdk.PollSpec{Interval: d}, nil
}

func (f fakePoll) Poll(_ context.Context, _ nodesdk.PollInput, cursor []byte) (nodesdk.PollResult, error) {
	f.rec.mu.Lock()
	defer f.rec.mu.Unlock()
	f.rec.cursors = append(f.rec.cursors, string(cursor))
	if f.rec.fail {
		return nodesdk.PollResult{}, errors.New("mail server down")
	}
	n, _ := strconv.Atoi(string(cursor))
	n++
	select {
	case f.rec.polled <- struct{}{}:
	default:
	}
	return nodesdk.PollResult{
		Items: []nodesdk.PolledItem{{Item: nodesdk.Item{JSON: map[string]any{"n": float64(n)}}, DedupKey: "v1:" + strconv.Itoa(n)}},
		Next:  []byte(strconv.Itoa(n)),
		Done:  func(context.Context) error { f.rec.mu.Lock(); f.rec.done++; f.rec.mu.Unlock(); return nil },
	}, nil
}

// onceSleeper returns at once for the first n poll-interval sleeps, then blocks like a real
// interval until the context ends.
type onceSleeper struct {
	mu sync.Mutex
	n  int
}

func (s *onceSleeper) Sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	if d == 20*time.Second && s.n > 0 { // only the poll interval; the scheduler tick blocks
		s.n--
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	<-ctx.Done()
	return ctx.Err() //nolint:wrapcheck // test double
}

func pollEnv(t *testing.T, rec *pollRecorder, sleeper port.Sleeper, mutate ...func(*config.TriggerConfig)) *env {
	t.Helper()
	e := newEnvWith(t, []registry.Factory{func() nodesdk.Node { return fakePoll{rec: rec} }}, sleeper, nil, mutate...)
	e.creds["c1"] = true
	return e
}

func pollNode(name string, params map[string]any) domain.Node {
	return domain.Node{Name: name, Type: fakePollType, TypeVersion: 1, Parameters: params,
		Credentials: map[string]domain.CredentialID{"cred": "c1"}}
}

func TestPollerRunsOnLeaderAndResumesFromCursor(t *testing.T) {
	rec := &pollRecorder{polled: make(chan struct{}, 8)}
	e := pollEnv(t, rec, &onceSleeper{n: 1})
	stop := e.lead(t)
	defer stop()
	ctx := context.Background()
	wf := e.save(ctx, t, pollNode("Mail", nil))
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-rec.polled:
		case <-time.After(5 * time.Second):
			t.Fatalf("poller did not poll twice: cursors=%q starts=%d", rec.cursors, e.starter.count())
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.starter.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.cursors) < 2 || rec.cursors[0] != "" || rec.cursors[1] != "1" {
		t.Errorf("cursors passed to Poll = %q, want [\"\" \"1\"]", rec.cursors)
	}
	if got := e.starter.reqs[0].DedupKey; got != "Mail:v1:1" {
		t.Errorf("dedup key = %q", got)
	}
	if rec.done < 1 {
		t.Error("Done was not called")
	}
}

func TestPollerDoesNotRunWithoutLeadership(t *testing.T) {
	rec := &pollRecorder{polled: make(chan struct{}, 8)}
	e := pollEnv(t, rec, blockingSleeper{})
	ctx := context.Background()
	wf := e.save(ctx, t, pollNode("Mail", nil))
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rec.polled:
		t.Fatal("a follower must not poll")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPollIntervalBelowMinimumIsRejected(t *testing.T) {
	rec := &pollRecorder{polled: make(chan struct{}, 8)}
	e := pollEnv(t, rec, blockingSleeper{})
	ctx := context.Background()
	wf := e.save(ctx, t, pollNode("Mail", map[string]any{"interval": time.Second}))
	if err := e.svc.Activate(ctx, wf.ID); err == nil {
		t.Fatal("Activate accepted an interval below poll.minInterval")
	}
}

func TestRepeatedPollFailuresDeactivateTheWorkflow(t *testing.T) {
	rec := &pollRecorder{polled: make(chan struct{}, 8), fail: true}
	e := pollEnv(t, rec, instantSleeper{}, func(c *config.TriggerConfig) { c.Poll.MaxFailures = 3 })
	stop := e.lead(t)
	defer stop()
	ctx := context.Background()
	wf := e.save(ctx, t, pollNode("Mail", nil))
	if err := e.svc.Activate(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := e.store.Get(ctx, wf.ID)
		if err == nil && !got.Active {
			msg, _ := e.acts.ActivationError(ctx, wf.ID)
			if msg == "" {
				t.Fatal("no activation error recorded")
			}
			rec.mu.Lock()
			n := len(rec.cursors)
			rec.mu.Unlock()
			if n != 3 {
				t.Errorf("polls = %d, want 3", n)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("workflow was not deactivated")
}
