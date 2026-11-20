package execution

import (
	"context"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// RunSync starts an execution and waits until it finishes (or stops waiting
// for a signal), for callers that need the result: the CLI against a running
// instance, and webhooks that answer with the last node's output. It waits at
// most worker.syncRunTimeout and then fails with ErrSyncTimeout without
// stopping the execution.
func (s *Service) RunSync(ctx context.Context, req StartRequest) (domain.Execution, error) {
	// Subscribe before starting so the finish event cannot be missed. The id
	// is unknown yet, so filter on the event kind and match the id afterwards.
	subCtx, stop := context.WithCancel(ctx)
	defer stop()
	sub, err := s.d.Bus.Subscribe(subCtx, port.EventFilter{Kinds: []port.EventKind{port.EventExecutionFinished}})
	if err != nil {
		return domain.Execution{}, fmt.Errorf("waiting for execution: %w", err)
	}
	defer sub.Close()

	id, err := s.Start(ctx, req)
	if err != nil {
		return domain.Execution{}, err
	}
	return s.await(ctx, id, sub)
}

// Await waits for an existing execution like RunSync does.
func (s *Service) Await(ctx context.Context, id domain.ExecutionID) (domain.Execution, error) {
	subCtx, stop := context.WithCancel(ctx)
	defer stop()
	sub, err := s.d.Bus.Subscribe(subCtx, port.EventFilter{ExecutionID: id, Kinds: []port.EventKind{port.EventExecutionFinished}})
	if err != nil {
		return domain.Execution{}, fmt.Errorf("waiting for execution: %w", err)
	}
	defer sub.Close()
	return s.await(ctx, id, sub)
}

func (s *Service) await(ctx context.Context, id domain.ExecutionID, sub port.Subscription) (domain.Execution, error) {
	timeout := time.NewTimer(s.cfg.SyncRunTimeout)
	defer timeout.Stop()
	poll := time.NewTicker(s.cfg.SyncPollInterval)
	defer poll.Stop()
	for {
		// Check the store first: the execution may already be over.
		ex, err := s.d.Executions.Get(ctx, id)
		if err != nil {
			return domain.Execution{}, fmt.Errorf("waiting for execution %s: %w", id, err)
		}
		if ex.Status.Terminal() || ex.Status == domain.StatusWaiting {
			return ex, nil
		}
		select {
		case <-ctx.Done():
			return domain.Execution{}, fmt.Errorf("waiting for execution %s: %w", id, ctx.Err())
		case <-timeout.C:
			return domain.Execution{}, fmt.Errorf("execution %s still %s after %s: %w", id, ex.Status, s.cfg.SyncRunTimeout, ErrSyncTimeout)
		case <-poll.C:
		case _, ok := <-sub.Events():
			if !ok {
				return domain.Execution{}, fmt.Errorf("waiting for execution %s: event stream closed", id)
			}
			// Any finish event wakes us; the store check above decides.
		}
	}
}
