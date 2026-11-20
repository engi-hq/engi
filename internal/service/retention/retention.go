// Package retention prunes old finished executions in the background. Only the
// process that leads runs it, so a cluster prunes from one replica.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Executions is the part of port.ExecutionStore the job needs.
type Executions interface {
	Get(ctx context.Context, id domain.ExecutionID) (domain.Execution, error)
	List(ctx context.Context, f port.ExecutionFilter, p port.Page) (port.PageResult[domain.Execution], error)
	Delete(ctx context.Context, id domain.ExecutionID) error
}

// Blobs is the part of the blob service the job needs.
type Blobs interface {
	DeleteExecution(ctx context.Context, id domain.ExecutionID) error
	Sweep(ctx context.Context, now time.Time, exists func(context.Context, domain.ExecutionID) (bool, error)) (int, error)
}

// Leader reports whether this process may run leader-only work.
type Leader interface{ Leading() bool }

// Deps are the collaborators of Service. Blobs may be nil.
type Deps struct {
	Executions Executions
	Blobs      Blobs
	Leader     Leader
	Clock      port.Clock
	Sleeper    port.Sleeper
	Logger     *slog.Logger
}

// Service is the pruning job.
type Service struct {
	cfg    config.RetentionConfig
	d      Deps
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds the job; Start runs it.
func New(cfg config.RetentionConfig, d Deps) *Service {
	return &Service{cfg: cfg, d: d}
}

// Start runs the loop in a goroutine until ctx is done or Shutdown is called.
// It does nothing when retention is disabled.
func (s *Service) Start(ctx context.Context) {
	if !s.cfg.Enabled || s.done != nil {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		for s.d.Sleeper.Sleep(ctx, s.cfg.Interval) == nil {
			if _, err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
				s.d.Logger.Error("retention pass failed", "error", err)
			}
		}
	}()
}

// Shutdown stops the loop and waits for it, or for ctx.
func (s *Service) Shutdown(ctx context.Context) error {
	if s.done == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for retention to stop: %w", ctx.Err())
	}
}

// RunOnce does one pass when this process leads and returns how many
// executions it deleted. A statuses' failure stops that status, not the pass.
func (s *Service) RunOnce(ctx context.Context) (int, error) {
	if !s.d.Leader.Leading() {
		return 0, nil
	}
	now := s.d.Clock.Now()
	ages := map[domain.ExecutionStatus]time.Duration{
		domain.StatusSuccess: s.cfg.MaxAge.Success, domain.StatusError: s.cfg.MaxAge.Error,
		domain.StatusCanceled: s.cfg.MaxAge.Canceled, domain.StatusCrashed: s.cfg.MaxAge.Crashed,
	}
	total := 0
	var errs []error
	for _, st := range []domain.ExecutionStatus{domain.StatusSuccess, domain.StatusError, domain.StatusCanceled, domain.StatusCrashed} {
		if ages[st] <= 0 {
			continue
		}
		n, err := s.prune(ctx, st, now.Add(-ages[st]))
		total += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	if s.d.Blobs != nil {
		if _, err := s.d.Blobs.Sweep(ctx, now, s.exists); err != nil {
			errs = append(errs, fmt.Errorf("sweeping binary data: %w", err))
		}
	}
	return total, errors.Join(errs...)
}

// prune deletes executions of one status started before cutoff, a batch at a
// time. Deleted rows leave the listing, so every round reads the first page.
func (s *Service) prune(ctx context.Context, st domain.ExecutionStatus, cutoff time.Time) (int, error) {
	filter := port.ExecutionFilter{Statuses: []domain.ExecutionStatus{st}, StartedBefore: cutoff}
	deleted := 0
	for ctx.Err() == nil && s.d.Leader.Leading() {
		page, err := s.d.Executions.List(ctx, filter, port.Page{Limit: s.cfg.BatchSize})
		if err != nil {
			return deleted, fmt.Errorf("listing %s executions: %w", st, err)
		}
		for _, ex := range page.Items {
			if err := s.d.Executions.Delete(ctx, ex.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
				return deleted, fmt.Errorf("deleting execution %s: %w", ex.ID, err)
			}
			deleted++
			if s.d.Blobs != nil {
				if err := s.d.Blobs.DeleteExecution(ctx, ex.ID); err != nil {
					s.d.Logger.Warn("deleting binary data failed; the sweep will retry", "execution", ex.ID, "error", err)
				}
			}
		}
		if len(page.Items) < s.cfg.BatchSize {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return deleted, fmt.Errorf("pruning %s executions: %w", st, err)
	}
	return deleted, nil
}

func (s *Service) exists(ctx context.Context, id domain.ExecutionID) (bool, error) {
	_, err := s.d.Executions.Get(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	return err == nil, err //nolint:wrapcheck // wrapped by the sweep
}
