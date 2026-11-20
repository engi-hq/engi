// Package waiter resumes waiting executions: on a ticker it finds the ones
// whose wake-up time has come and queues them again, and it resumes the ones
// that wait for an external signal when that signal arrives.
//
// Resume tokens are "<executionID>.<secret>" (nodesdk.NewResumeToken), so the
// waiter finds the execution without an index and then compares the whole token
// with the one saved in the execution's state.
//
// A resumed execution is marked queued and gets a new job; the worker that
// claims the job continues from the state saved when the execution paused.
package waiter

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Errors returned by ResumeByToken; check with errors.Is.
var (
	// ErrInvalidToken covers malformed, unknown and wrong tokens alike, so a
	// caller learns nothing about which executions exist.
	ErrInvalidToken = errors.New("invalid resume token")
	// ErrNotWaiting: the execution is not waiting any more (for example it was
	// already resumed or canceled).
	ErrNotWaiting = errors.New("execution is not waiting")
)

// IDGenerator creates job identifiers.
type IDGenerator interface {
	JobID() (domain.JobID, error)
}

// Deps are the collaborators of the Service. All but Logger are required.
type Deps struct {
	Executions port.ExecutionStore
	States     port.ExecutionStateStore
	Queue      port.JobQueue
	Tx         port.TxManager
	Clock      port.Clock
	IDs        IDGenerator
	Codec      engine.StateCodec
	Logger     *slog.Logger
}

// Service is the wait scheduler.
type Service struct {
	cfg config.WorkerConfig
	d   Deps

	mu      sync.Mutex
	started bool
	stop    context.CancelFunc
	done    chan struct{}
}

// New builds a Service.
func New(cfg config.WorkerConfig, d Deps) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid worker config: %w", err)
	}
	if d.Executions == nil || d.States == nil || d.Queue == nil || d.Tx == nil || d.Clock == nil || d.IDs == nil || d.Codec == nil {
		return nil, errors.New("waiter requires execution and state stores, queue, tx manager, clock, id generator and codec")
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{cfg: cfg, d: d}, nil
}

// Start runs the ticker in a goroutine owned by the Service until Shutdown or
// until ctx is canceled.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("waiter already started")
	}
	loopCtx, stop := context.WithCancel(ctx)
	s.started, s.stop, s.done = true, stop, make(chan struct{})
	go s.loop(loopCtx, s.done)
	return nil
}

// Shutdown stops the ticker and waits for the current tick to end.
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	stop, done := s.stop, s.done
	s.mu.Unlock()
	stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for the waiter to stop: %w", ctx.Err())
	}
}

func (s *Service) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(s.cfg.WaiterInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				s.d.Logger.Error("resuming due executions failed", "error", err)
			}
		}
	}
}

// Tick queues every waiting execution whose wake-up time has come, up to
// worker.waiterBatchSize, and returns how many it resumed.
func (s *Service) Tick(ctx context.Context) (int, error) {
	due, err := s.d.Executions.FindDueWaiting(ctx, s.d.Clock.Now(), s.cfg.WaiterBatchSize)
	if err != nil {
		return 0, fmt.Errorf("finding due executions: %w", err)
	}
	resumed := 0
	for _, ex := range due {
		err := s.resume(ctx, ex.ID, nil)
		switch {
		case err == nil:
			resumed++
		case errors.Is(err, ErrNotWaiting):
			// Resumed or canceled by somebody else in the meantime.
		default:
			s.d.Logger.Error("resuming execution failed", "execution", ex.ID, "error", err)
		}
	}
	return resumed, nil
}

// ResumeByToken resumes the execution that waits for token and delivers items
// as the output of the node it waited on. Every unusable token yields
// ErrInvalidToken; an execution that already moved on yields ErrNotWaiting.
func (s *Service) ResumeByToken(ctx context.Context, token string, items []domain.Item) error {
	id, _, ok := strings.Cut(token, nodesdk.ResumeTokenSeparator)
	if !ok || id == "" {
		return ErrInvalidToken
	}
	execID := domain.ExecutionID(id)
	if execID.Validate() != nil {
		return ErrInvalidToken
	}
	ex, err := s.d.Executions.Get(ctx, execID)
	if errors.Is(err, domain.ErrNotFound) {
		return ErrInvalidToken
	}
	if err != nil {
		return fmt.Errorf("resuming by token: %w", err)
	}
	st, err := s.loadState(ctx, execID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && !tokenMatches(st, token)) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	if ex.Status != domain.StatusWaiting {
		return fmt.Errorf("execution %s is %s: %w", execID, ex.Status, ErrNotWaiting)
	}
	if items == nil {
		items = []domain.Item{}
	}
	return s.resume(ctx, execID, items)
}

// PendingToken returns the resume token an execution is waiting for, so that an
// authorized caller (the API, the editor) can show or hand out the resume URL.
// The token is a secret: never log it. ErrNotWaiting means the execution is not
// waiting for a token.
func (s *Service) PendingToken(ctx context.Context, id domain.ExecutionID) (string, error) {
	ex, err := s.d.Executions.Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("reading execution %s: %w", id, err)
	}
	if ex.Status != domain.StatusWaiting {
		return "", fmt.Errorf("execution %s is %s: %w", id, ex.Status, ErrNotWaiting)
	}
	st, err := s.loadState(ctx, id)
	if err != nil {
		return "", err
	}
	if st.Pending == nil || st.Pending.ResumeToken == "" {
		return "", fmt.Errorf("execution %s waits for a time, not a token: %w", id, ErrNotWaiting)
	}
	return st.Pending.ResumeToken, nil
}

func tokenMatches(st *engine.State, token string) bool {
	if st == nil || st.Pending == nil || st.Pending.ResumeToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(st.Pending.ResumeToken), []byte(token)) == 1
}

func (s *Service) loadState(ctx context.Context, id domain.ExecutionID) (*engine.State, error) {
	blob, err := s.d.States.LoadState(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("loading state of execution %s: %w", id, err)
	}
	_, st, err := s.d.Codec.DecodeSaved(blob)
	if err != nil {
		return nil, fmt.Errorf("reading state of execution %s: %w", id, err)
	}
	if st == nil {
		return nil, fmt.Errorf("execution %s has not run yet: %w", id, domain.ErrNotFound)
	}
	return st, nil
}

// resume moves a waiting execution to queued and enqueues its job, in one
// transaction. With items non-nil they are stored as the data the waiting node
// hands on.
func (s *Service) resume(ctx context.Context, id domain.ExecutionID, items []domain.Item) error {
	return s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		// Read inside the transaction so two resumers cannot both queue it.
		ex, err := s.d.Executions.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("resuming execution %s: %w", id, err)
		}
		if ex.Status != domain.StatusWaiting {
			return fmt.Errorf("execution %s is %s: %w", id, ex.Status, ErrNotWaiting)
		}
		if items != nil {
			st, err := s.loadState(ctx, id)
			if err != nil {
				return err
			}
			if st.Pending == nil {
				return fmt.Errorf("execution %s has no pending wait: %w", id, ErrNotWaiting)
			}
			st.Pending.Resumed, st.Pending.ResumeItems = true, items
			blob, err := s.d.Codec.EncodeState(st)
			if err != nil {
				return fmt.Errorf("resuming execution %s: %w", id, err)
			}
			if err := s.d.States.SaveState(ctx, id, blob); err != nil {
				return fmt.Errorf("resuming execution %s: %w", id, err)
			}
		}
		ex.Status, ex.WaitUntil = domain.StatusQueued, nil
		if err := s.d.Executions.Update(ctx, ex); err != nil {
			return fmt.Errorf("resuming execution %s: %w", id, err)
		}
		jid, err := s.d.IDs.JobID()
		if err != nil {
			return fmt.Errorf("resuming execution %s: %w", id, err)
		}
		if err := s.d.Queue.Enqueue(ctx, domain.Job{ID: jid, ExecutionID: id}); err != nil {
			return fmt.Errorf("resuming execution %s: %w", id, err)
		}
		s.d.Logger.Info("waiting execution queued to resume", "execution", id)
		return nil
	})
}
