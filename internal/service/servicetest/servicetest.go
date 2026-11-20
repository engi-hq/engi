// Package servicetest provides in-memory fakes of the persistence and queue
// ports, so the services that use them can be tested without a database.
//
// The fakes follow the documented port contracts but are not performance
// models; the real adapters are held to the contracts by internal/port/porttest.
package servicetest

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	idgen "github.com/engi-hq/engi/internal/id"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/engine"
)

// Clock is a manually advanced port.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

var _ port.Clock = (*Clock)(nil)

// NewClock starts at a fixed instant.
func NewClock() *Clock { return &Clock{now: time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)} }

// Now implements port.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// NewIDs returns an id generator on the clock.
func NewIDs(c *Clock) *idgen.Generator { return idgen.NewGenerator(c.Now, rand.Reader) }

// Store is an in-memory WorkflowStore, WorkflowVersionStore and TxManager
// (transactions just run the function). Executions() is the ExecutionStore and
// ExecutionStateStore over the same data.
type Store struct {
	mu         sync.Mutex
	workflows  map[domain.WorkflowID]domain.Workflow
	versions   map[domain.WorkflowID][]domain.WorkflowVersion
	executions map[domain.ExecutionID]domain.Execution
	// runs is the holder of each running execution (see ClaimRun).
	runs   map[domain.ExecutionID]domain.RunHolder
	states map[domain.ExecutionID][]byte
	clock  port.Clock
}

var (
	_ port.WorkflowStore        = (*Store)(nil)
	_ port.WorkflowVersionStore = (*Store)(nil)
	_ port.TxManager            = (*Store)(nil)
)

// NewStore returns an empty store.
func NewStore(clock port.Clock) *Store {
	return &Store{
		workflows: map[domain.WorkflowID]domain.Workflow{}, versions: map[domain.WorkflowID][]domain.WorkflowVersion{},
		executions: map[domain.ExecutionID]domain.Execution{}, runs: map[domain.ExecutionID]domain.RunHolder{},
		states: map[domain.ExecutionID][]byte{}, clock: clock,
	}
}

func notFound(what, key string) error { return fmt.Errorf("%s %s: %w", what, key, domain.ErrNotFound) }

// WithinTx implements port.TxManager.
func (s *Store) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// Save implements port.WorkflowStore.
func (s *Store) Save(_ context.Context, w domain.Workflow) (domain.Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Version = len(s.versions[w.ID]) + 1
	s.workflows[w.ID] = w
	s.versions[w.ID] = append(s.versions[w.ID], domain.WorkflowVersion{WorkflowID: w.ID, Version: w.Version, CreatedAt: s.clock.Now(), Workflow: w})
	return w, nil
}

// Get implements port.WorkflowStore.
func (s *Store) Get(_ context.Context, wf domain.WorkflowID) (domain.Workflow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workflows[wf]
	if !ok {
		return domain.Workflow{}, notFound("workflow", string(wf))
	}
	return w, nil
}

// SetActive implements port.WorkflowStore.
func (s *Store) SetActive(_ context.Context, wf domain.WorkflowID, active bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workflows[wf]
	if !ok {
		return notFound("workflow", string(wf))
	}
	w.Active = active
	s.workflows[wf] = w
	return nil
}

// Delete implements port.WorkflowStore.
func (s *Store) Delete(_ context.Context, id domain.WorkflowID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workflows[id]; !ok {
		return notFound("workflow", string(id))
	}
	delete(s.workflows, id)
	delete(s.versions, id)
	return nil
}

// List implements port.WorkflowStore.
func (s *Store) List(_ context.Context, f port.WorkflowFilter, _ port.Page) (port.PageResult[domain.Workflow], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Workflow
	for _, w := range s.workflows {
		if f.Active != nil && w.Active != *f.Active {
			continue
		}
		out = append(out, w)
	}
	return port.PageResult[domain.Workflow]{Items: out}, nil
}

// GetVersion implements port.WorkflowVersionStore.
func (s *Store) GetVersion(_ context.Context, id domain.WorkflowID, version int) (domain.WorkflowVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[id]
	if version < 1 || version > len(vs) {
		return domain.WorkflowVersion{}, notFound("workflow version", fmt.Sprintf("%s@%d", id, version))
	}
	return vs[version-1], nil
}

// ListVersions implements port.WorkflowVersionStore.
func (s *Store) ListVersions(_ context.Context, id domain.WorkflowID, _ port.Page) (port.PageResult[domain.WorkflowVersion], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[id]
	out := make([]domain.WorkflowVersion, len(vs))
	for i, v := range vs {
		out[len(vs)-1-i] = v
	}
	return port.PageResult[domain.WorkflowVersion]{Items: out}, nil
}

// Executions implements port.ExecutionStore and port.ExecutionStateStore.
type Executions struct{ *Store }

// Create implements port.ExecutionStore.
func (e *Executions) Create(_ context.Context, x domain.Execution) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if x.DedupKey != "" {
		for _, o := range e.executions {
			if o.WorkflowID == x.WorkflowID && o.DedupKey == x.DedupKey {
				return &domain.DuplicateError{WorkflowID: x.WorkflowID, DedupKey: x.DedupKey, ExistingID: o.ID}
			}
		}
	}
	if _, ok := e.executions[x.ID]; ok {
		return fmt.Errorf("execution %s: %w", x.ID, domain.ErrAlreadyExists)
	}
	e.executions[x.ID] = x
	return nil
}

func (s *Store) getExecution(id domain.ExecutionID) (domain.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.executions[id]
	if !ok {
		return domain.Execution{}, notFound("execution", string(id))
	}
	return e, nil
}

// Executions returns the port.ExecutionStore view (Get and List clash by name
// with the workflow store).
func (s *Store) Executions() *Executions { return &Executions{s} }

var (
	_ port.ExecutionStore      = (*Executions)(nil)
	_ port.ExecutionStateStore = (*Executions)(nil)
)

// Get implements port.ExecutionStore.
func (e *Executions) Get(_ context.Context, id domain.ExecutionID) (domain.Execution, error) {
	return e.getExecution(id)
}

// List implements port.ExecutionStore.
func (e *Executions) List(_ context.Context, f port.ExecutionFilter, _ port.Page) (port.PageResult[domain.Execution], error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []domain.Execution
	for _, x := range e.executions {
		if f.WorkflowID != "" && x.WorkflowID != f.WorkflowID {
			continue
		}
		x.RunData = nil
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return port.PageResult[domain.Execution]{Items: out}, nil
}

// Delete implements port.ExecutionStore.
func (e *Executions) Delete(_ context.Context, id domain.ExecutionID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.executions[id]; !ok {
		return notFound("execution", string(id))
	}
	delete(e.executions, id)
	return nil
}

// Update implements port.ExecutionStore.
func (e *Executions) Update(_ context.Context, x domain.Execution) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, ok := e.executions[x.ID]
	if !ok {
		return notFound("execution", string(x.ID))
	}
	cur.Status, cur.FinishedAt, cur.WaitUntil, cur.Error, cur.RunData = x.Status, x.FinishedAt, x.WaitUntil, x.Error, x.RunData
	e.executions[x.ID] = cur
	return nil
}

// ClaimRun implements port.ExecutionStore: it succeeds only for a queued execution.
func (e *Executions) ClaimRun(_ context.Context, id domain.ExecutionID, holder domain.RunHolder, claimedAt time.Time) (domain.Execution, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, ok := e.executions[id]
	if !ok {
		return domain.Execution{}, false, notFound("execution", string(id))
	}
	held := e.runs[id]
	takeover := cur.Status == domain.StatusRunning && held.JobID == holder.JobID && held.Attempt < holder.Attempt
	if cur.Status != domain.StatusQueued && !takeover {
		return cur, false, nil
	}
	cur.Status, cur.WaitUntil, cur.Attempts = domain.StatusRunning, nil, cur.Attempts+1
	if takeover {
		at := claimedAt
		cur.LastRecoveredAt = &at
	}
	e.executions[id] = cur
	e.runs[id] = holder
	return cur, true, nil
}

// fenced reports whether holder may write the execution now: it holds the running
// execution, and a queued one is writable only for a crash (fenceQueued).
func (e *Executions) fenced(cur domain.Execution, holder domain.RunHolder, fenceQueued bool) bool {
	if fenceQueued && cur.Status == domain.StatusQueued {
		return true
	}
	return cur.Status == domain.StatusRunning && e.runs[cur.ID] == holder
}

// write stores the result fields of x and its run data.
func (e *Executions) write(x domain.Execution) {
	cur := e.executions[x.ID]
	cur.Status, cur.FinishedAt, cur.WaitUntil, cur.Error, cur.RunData = x.Status, x.FinishedAt, x.WaitUntil, x.Error, x.RunData
	e.executions[x.ID] = cur
}

// FinishRun implements port.ExecutionStore.
func (e *Executions) FinishRun(_ context.Context, x domain.Execution, holder domain.RunHolder) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, ok := e.executions[x.ID]
	if !ok {
		return false, notFound("execution", string(x.ID))
	}
	if !e.fenced(cur, holder, false) {
		return false, nil
	}
	e.write(x)
	return true, nil
}

// CrashRun implements port.ExecutionStore.
func (e *Executions) CrashRun(_ context.Context, x domain.Execution, holder domain.RunHolder) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, ok := e.executions[x.ID]
	if !ok {
		return false, notFound("execution", string(x.ID))
	}
	if !e.fenced(cur, holder, true) {
		return false, nil
	}
	e.write(x)
	return true, nil
}

// RequeueRun implements port.ExecutionStore: only the job holding the run may requeue it.
func (e *Executions) RequeueRun(_ context.Context, id domain.ExecutionID, holder domain.RunHolder, recoveredAt *time.Time) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, ok := e.executions[id]
	if !ok {
		return false, notFound("execution", string(id))
	}
	if cur.Status != domain.StatusRunning || e.runs[id] != holder {
		return false, nil
	}
	cur.Status = domain.StatusQueued
	if recoveredAt != nil {
		at := *recoveredAt
		cur.LastRecoveredAt = &at
	}
	e.executions[id] = cur
	return true, nil
}

// FindDueWaiting implements port.ExecutionStore.
func (e *Executions) FindDueWaiting(_ context.Context, now time.Time, limit int) ([]domain.Execution, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []domain.Execution
	for _, x := range e.executions {
		if x.Status == domain.StatusWaiting && x.WaitUntil != nil && !x.WaitUntil.After(now) {
			x.RunData = nil
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WaitUntil.Before(*out[j].WaitUntil) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Prune implements port.ExecutionStore.
func (e *Executions) Prune(context.Context, map[domain.ExecutionStatus]time.Time) (int, error) {
	return 0, nil
}

// SaveState implements port.ExecutionStateStore.
func (e *Executions) SaveState(_ context.Context, id domain.ExecutionID, state []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.states[id] = append([]byte(nil), state...)
	return nil
}

// LoadState implements port.ExecutionStateStore.
func (e *Executions) LoadState(_ context.Context, id domain.ExecutionID) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.states[id]
	if !ok {
		return nil, notFound("state", string(id))
	}
	return append([]byte(nil), b...), nil
}

// DeleteState implements port.ExecutionStateStore.
func (e *Executions) DeleteState(_ context.Context, id domain.ExecutionID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.states, id)
	return nil
}

// HasState reports whether a state blob is stored.
func (e *Executions) HasState(id domain.ExecutionID) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.states[id]
	return ok
}

// SetExecution overwrites an execution, for arranging tests.
func (e *Executions) SetExecution(x domain.Execution) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.executions[x.ID] = x
}

// Queue is an in-memory JobQueue, JobReclaimer and JobDispatcher on a Clock.
type Queue struct {
	mu          sync.Mutex
	clock       port.Clock
	maxAttempts int
	jobs        []*domain.Job
	wake        chan struct{}
}

var (
	_ port.JobQueue      = (*Queue)(nil)
	_ port.JobReclaimer  = (*Queue)(nil)
	_ port.JobDispatcher = (*Queue)(nil)
)

// NewQueue returns an empty queue whose jobs fail after maxAttempts claims.
func NewQueue(clock port.Clock, maxAttempts int) *Queue {
	return &Queue{clock: clock, maxAttempts: maxAttempts, wake: make(chan struct{}, 1)}
}

func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Enqueue implements port.JobQueue.
func (q *Queue) Enqueue(_ context.Context, j domain.Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.Status, j.CreatedAt = domain.JobQueued, q.clock.Now()
	if j.AvailableAt.IsZero() {
		j.AvailableAt = j.CreatedAt
	}
	q.jobs = append(q.jobs, &j)
	q.signal()
	return nil
}

// Claim implements port.JobQueue.
func (q *Queue) Claim(_ context.Context, workerID string, lease time.Duration) (domain.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clock.Now()
	for _, j := range q.jobs {
		if j.Status == domain.JobQueued && !j.AvailableAt.After(now) {
			j.Status, j.LeaseOwner, j.LeaseUntil = domain.JobLeased, workerID, now.Add(lease)
			j.Attempts++
			return *j, nil
		}
	}
	return domain.Job{}, domain.ErrNoJob
}

// Next implements port.JobDispatcher.
func (q *Queue) Next(ctx context.Context, workerID string, lease time.Duration) (domain.Job, error) {
	for {
		j, err := q.Claim(ctx, workerID, lease)
		if err == nil {
			return j, nil
		}
		select {
		case <-ctx.Done():
			return domain.Job{}, fmt.Errorf("waiting for job: %w", ctx.Err())
		case <-q.wake:
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (q *Queue) owned(id domain.JobID, workerID string) (*domain.Job, error) {
	for _, j := range q.jobs {
		if j.ID == id && j.Status == domain.JobLeased && j.LeaseOwner == workerID {
			return j, nil
		}
	}
	return nil, fmt.Errorf("job %s: %w", id, domain.ErrLeaseLost)
}

// Heartbeat implements port.JobQueue.
func (q *Queue) Heartbeat(_ context.Context, id domain.JobID, workerID string, lease time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.owned(id, workerID)
	if err != nil {
		return err
	}
	j.LeaseUntil = q.clock.Now().Add(lease)
	return nil
}

// Complete implements port.JobQueue.
func (q *Queue) Complete(_ context.Context, id domain.JobID, workerID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, err := q.owned(id, workerID); err != nil {
		return err
	}
	for i, j := range q.jobs {
		if j.ID == id {
			q.jobs = append(q.jobs[:i], q.jobs[i+1:]...)
			break
		}
	}
	return nil
}

// Fail implements port.JobQueue.
func (q *Queue) Fail(_ context.Context, id domain.JobID, workerID, reason string, retryAt time.Time) (domain.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.owned(id, workerID)
	if err != nil {
		return domain.Job{}, err
	}
	j.LeaseOwner, j.LastError, j.AvailableAt = "", reason, retryAt
	j.Status = domain.JobQueued
	if j.Attempts >= q.maxAttempts {
		j.Status = domain.JobFailed
	}
	q.signal()
	return *j, nil
}

// Release implements port.JobQueue.
func (q *Queue) Release(_ context.Context, id domain.JobID, workerID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.owned(id, workerID)
	if err != nil {
		return err
	}
	j.Status, j.LeaseOwner = domain.JobQueued, ""
	j.Attempts = max(j.Attempts-1, 0)
	q.signal()
	return nil
}

// ReclaimExpired implements port.JobReclaimer.
func (q *Queue) ReclaimExpired(context.Context) ([]domain.ReclaimedJob, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clock.Now()
	var out []domain.ReclaimedJob
	for _, j := range q.jobs {
		if j.Status != domain.JobLeased || !j.LeaseUntil.Before(now) {
			continue
		}
		prev := j.LeaseOwner
		j.LeaseOwner = ""
		j.Status = domain.JobQueued
		if j.Attempts >= q.maxAttempts {
			j.Status = domain.JobFailed
		}
		out = append(out, domain.ReclaimedJob{Job: *j, PreviousOwner: prev})
	}
	q.signal()
	return out, nil
}

// Jobs returns a copy of the jobs in the queue.
func (q *Queue) Jobs() []domain.Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]domain.Job, len(q.jobs))
	for i, j := range q.jobs {
		out[i] = *j
	}
	return out
}

// Eventually polls cond until it holds or the test's patience (a few seconds)
// runs out.
func Eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Bus is a synchronous in-memory port.Bus that records every published event.
type Bus struct {
	mu     sync.Mutex
	subs   map[int]*busSub
	next   int
	events []port.Event
}

var _ port.Bus = (*Bus)(nil)

// NewBus returns an empty bus.
func NewBus() *Bus { return &Bus{subs: map[int]*busSub{}} }

type busSub struct {
	bus    *Bus
	id     int
	filter port.EventFilter
	ch     chan port.Event
	once   sync.Once
}

func (s *busSub) Events() <-chan port.Event { return s.ch }

func (s *busSub) Close() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		defer s.bus.mu.Unlock()
		delete(s.bus.subs, s.id)
		close(s.ch)
	})
}

// Publish implements port.Bus.
func (b *Bus) Publish(_ context.Context, e port.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
	for _, s := range b.subs {
		if s.filter.Matches(e) {
			select {
			case s.ch <- e:
			default:
			}
		}
	}
	return nil
}

// Subscribe implements port.Bus.
func (b *Bus) Subscribe(ctx context.Context, f port.EventFilter) (port.Subscription, error) {
	b.mu.Lock()
	s := &busSub{bus: b, id: b.next, filter: f, ch: make(chan port.Event, 64)}
	b.next++
	b.subs[s.id] = s
	b.mu.Unlock()
	context.AfterFunc(ctx, s.Close)
	return s, nil
}

// Events returns the events published so far, optionally only of the given kinds.
func (b *Bus) Events(kinds ...port.EventKind) []port.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []port.Event
	for _, e := range b.events {
		if len(kinds) == 0 || (port.EventFilter{Kinds: kinds}).Matches(e) {
			out = append(out, e)
		}
	}
	return out
}

// Codec is an engine.StateCodec on encoding/json, enough to round-trip what the
// service tests store.
type Codec struct{}

var _ engine.StateCodec = Codec{}

type envelope struct {
	Input *domain.ExecutionInput `json:"input,omitempty"`
	State *engine.State          `json:"state,omitempty"`
}

// EncodeState implements engine.StateCodec.
func (Codec) EncodeState(s *engine.State) ([]byte, error) {
	return marshal(envelope{State: s})
}

// DecodeState implements engine.StateCodec.
func (Codec) DecodeState(data []byte) (*engine.State, error) {
	_, st, err := Codec{}.DecodeSaved(data)
	return st, err
}

// EncodeInput implements engine.StateCodec.
func (Codec) EncodeInput(in domain.ExecutionInput) ([]byte, error) {
	return marshal(envelope{Input: &in})
}

// DecodeSaved implements engine.StateCodec.
func (Codec) DecodeSaved(data []byte) (*domain.ExecutionInput, *engine.State, error) {
	var e envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, nil, fmt.Errorf("decoding payload: %w", err)
	}
	return e.Input, e.State, nil
}

func marshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding payload: %w", err)
	}
	return b, nil
}
