package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/domain"
)

// ErrNotDeletable: only finished executions can be deleted; cancel a running
// one first.
var ErrNotDeletable = errors.New("execution is not finished")

// Delete removes a finished execution with its data. A queued, running or
// waiting execution is refused with an error wrapping ErrNotDeletable.
func (s *Service) Delete(ctx context.Context, id domain.ExecutionID) error {
	ex, err := s.d.Executions.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("deleting execution %s: %w", id, err)
	}
	if !ex.Status.Terminal() {
		return fmt.Errorf("deleting execution %s (%s): %w", id, ex.Status, ErrNotDeletable)
	}
	if err := s.d.Executions.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting execution %s: %w", id, err)
	}
	s.deleteBlobs(ctx, id)
	return nil
}

// deleteBlobs drops the execution's binary data. The execution is already
// gone, so a failure is logged and left to the orphan sweep.
func (s *Service) deleteBlobs(ctx context.Context, id domain.ExecutionID) {
	if s.d.Blobs == nil {
		return
	}
	if err := s.d.Blobs.DeleteExecution(ctx, id); err != nil {
		s.d.Logger.Warn("deleting binary data failed; the sweep will retry", "execution", id, "error", err)
	}
}

// Prune deletes finished executions older than the cutoff of their status
// (see port.ExecutionStore.Prune), then sweeps the binary data left behind.
func (s *Service) Prune(ctx context.Context, cutoffs map[domain.ExecutionStatus]time.Time) (int, error) {
	n, err := s.d.Executions.Prune(ctx, cutoffs)
	if err != nil {
		return n, fmt.Errorf("pruning executions: %w", err)
	}
	if s.d.Blobs == nil || n == 0 {
		return n, nil
	}
	_, err = s.d.Blobs.Sweep(ctx, s.d.Clock.Now(), func(ctx context.Context, id domain.ExecutionID) (bool, error) {
		_, err := s.d.Executions.Get(ctx, id)
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return err == nil, err //nolint:wrapcheck // wrapped by the sweep
	})
	if err != nil {
		return n, fmt.Errorf("sweeping binary data: %w", err)
	}
	return n, nil
}
