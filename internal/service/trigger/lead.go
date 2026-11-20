package trigger

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Start campaigns for leadership in a goroutine owned by the Service. While the
// process leads, it runs the schedules; the goroutines end when ctx is done or
// Shutdown is called. Webhooks do not depend on leadership.
func (s *Service) Start(ctx context.Context) error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.started {
		return errors.New("trigger service already started")
	}
	runCtx, stop := context.WithCancel(ctx)
	s.started, s.stop, s.done = true, stop, make(chan struct{})
	s.lmu.Lock()
	s.listenCtx = runCtx
	s.lmu.Unlock()
	var follow sync.WaitGroup
	s.followListeners(runCtx, &follow)
	go func() {
		defer close(s.done)
		defer follow.Wait()
		err := s.d.Lease.Run(runCtx, port.LeaderCallbacks{OnStartedLeading: s.lead, OnStoppedLeading: s.stopLeading})
		if err != nil && runCtx.Err() == nil {
			s.d.Logger.Error("leader election stopped", "error", err)
		}
	}()
	return nil
}

// Shutdown stops leading and waits for the schedule loop to end.
func (s *Service) Shutdown(ctx context.Context) error {
	s.runMu.Lock()
	if !s.started {
		s.runMu.Unlock()
		return nil
	}
	stop, done := s.stop, s.done
	s.runMu.Unlock()
	stop()
	listenersDone := make(chan struct{})
	go func() {
		s.listenWG.Wait()
		close(listenersDone)
	}()
	for _, c := range []<-chan struct{}{done, listenersDone} {
		select {
		case <-c:
		case <-ctx.Done():
			return fmt.Errorf("waiting for the trigger service to stop: %w", ctx.Err())
		}
	}
	return nil
}

// lead runs while this process is the leader: it loads the schedules of all
// active workflows and fires them every schedule.tick.
func (s *Service) lead(ctx context.Context) {
	s.mu.Lock()
	s.leading, s.leadCtx = true, ctx
	s.mu.Unlock()
	s.d.Logger.Info("leading: starting schedules")

	var wg sync.WaitGroup
	if s.d.Bus != nil {
		sub, err := s.d.Bus.Subscribe(ctx, port.EventFilter{Kinds: []port.EventKind{port.EventWorkflowActivated, port.EventWorkflowDeactivated}})
		if err != nil {
			s.d.Logger.Error("subscribing to activation events failed", "error", err)
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer sub.Close()
				s.follow(ctx, sub)
			}()
		}
	}
	s.loadSchedules(ctx)
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	for ctx.Err() == nil {
		if err := s.d.Sleeper.Sleep(ctx, s.cfg.Schedule.Tick); err != nil {
			break
		}
		s.Tick(ctx)
	}
	wg.Wait()
	s.stopAllPollers()
}

// stopLeading drops every schedule: another replica takes over.
func (s *Service) stopLeading() {
	s.mu.Lock()
	s.leading, s.ready, s.leadCtx = false, false, nil
	s.byWF = map[domain.WorkflowID][]*schedule{}
	s.mu.Unlock()
	s.stopAllPollers()
	s.d.Logger.Info("stopped leading: schedules stopped")
}

// follow applies activation events from other replicas.
func (s *Service) follow(ctx context.Context, sub port.Subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.Events():
			if !ok {
				return
			}
			s.syncSchedules(ctx, e.WorkflowID)
		}
	}
}

// loadSchedules starts the schedules of every active workflow.
func (s *Service) loadSchedules(ctx context.Context) {
	active := true
	page := port.Page{Limit: s.cfg.Webhook.ActivationPageSize}
	for ctx.Err() == nil {
		res, err := s.d.Workflows.List(ctx, port.WorkflowFilter{Active: &active}, page)
		if err != nil {
			s.d.Logger.Error("listing active workflows for schedules failed", "error", err)
			return
		}
		for _, wf := range res.Items {
			s.syncSchedules(ctx, wf.ID)
		}
		if res.NextCursor == "" {
			return
		}
		page.Cursor = res.NextCursor
	}
}

// syncSchedules makes the running schedules of a workflow match the store.
func (s *Service) syncSchedules(ctx context.Context, id domain.WorkflowID) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	wf, err := s.d.Workflows.Get(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			s.removeSchedules(id)
			s.stopPollers(id)
			return
		}
		s.d.Logger.Error("loading workflow for schedules failed", "workflow", id, "error", err)
		return
	}
	if !wf.Active {
		s.removeSchedules(id)
		s.stopPollers(id)
		return
	}
	p, err := s.inspect(ctx, wf)
	if err != nil {
		s.d.Logger.Error("workflow schedules are invalid", "workflow", wf.ID, "error", err)
		return
	}
	s.setSchedules(ctx, wf.ID, p.schedules)
	s.setPollers(wf.ID, p.polls) //nolint:contextcheck // pollers outlive the event that changed them
}
