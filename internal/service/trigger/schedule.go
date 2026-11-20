package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/cronexpr"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/execution"
)

// schedule is one running schedule trigger. Its fields are guarded by Service.mu.
type schedule struct {
	workflow domain.WorkflowID
	node     string
	interval time.Duration
	cron     *cronexpr.Expr
	loc      *time.Location
	// fingerprint identifies the settings, so that re-registering an unchanged
	// schedule keeps its running state.
	fingerprint string
	// next is the scheduled time of the next run. It may lie in the past after
	// downtime; Tick applies the missed-run policy.
	next time.Time
}

// scheduleState is what survives a restart.
type scheduleState struct {
	Next time.Time `json:"next"`
}

// after returns the first scheduled time strictly after t. For intervals the
// grid stays anchored at the schedule's own previous time.
func (sc *schedule) after(anchor, t time.Time) time.Time {
	if sc.cron != nil {
		return sc.cron.Next(t, sc.loc)
	}
	n := anchor.Add(sc.interval)
	if n.After(t) {
		return n
	}
	skipped := t.Sub(anchor) / sc.interval
	return anchor.Add((skipped + 1) * sc.interval)
}

// setSchedules replaces the schedules of a workflow. It does nothing unless
// this process is the leader; a new leader loads schedules itself.
func (s *Service) setSchedules(ctx context.Context, id domain.WorkflowID, nodes []scheduleNode) {
	s.mu.Lock()
	leading := s.leading
	s.mu.Unlock()
	if !leading {
		return
	}
	now := s.d.Clock.Now()
	var list []*schedule
	for _, n := range nodes {
		sc, err := s.newSchedule(id, n)
		if err != nil {
			s.d.Logger.Error("starting schedule failed", "workflow", id, "node", n.node.Name, "error", err)
			continue
		}
		s.mu.Lock()
		old := s.find(id, sc.node, sc.fingerprint)
		s.mu.Unlock()
		if old != nil {
			list = append(list, old)
			continue
		}
		sc.next = s.initialNext(ctx, sc, now)
		list = append(list, sc)
	}
	s.mu.Lock()
	s.byWF[id] = list
	s.mu.Unlock()
}

// find returns the running schedule of a node with the same settings; the
// caller holds s.mu.
func (s *Service) find(id domain.WorkflowID, node, fingerprint string) *schedule {
	for _, sc := range s.byWF[id] {
		if sc.node == node && sc.fingerprint == fingerprint {
			return sc
		}
	}
	return nil
}

func (s *Service) newSchedule(id domain.WorkflowID, n scheduleNode) (*schedule, error) {
	tz := n.spec.Timezone
	if tz == "" {
		tz = s.cfg.Schedule.DefaultTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("timezone %q: %w", tz, err)
	}
	sc := &schedule{
		workflow: id, node: n.node.Name, interval: n.spec.Interval, loc: loc,
		fingerprint: fmt.Sprintf("%s|%d|%s", n.spec.Cron, n.spec.Interval, loc),
	}
	if n.spec.Cron != "" {
		if sc.cron, err = cronexpr.Parse(n.spec.Cron); err != nil {
			return nil, fmt.Errorf("cron expression: %w", err)
		}
	} else if sc.interval <= 0 {
		return nil, errors.New("schedule has neither a cron expression nor an interval")
	}
	return sc, nil
}

// initialNext restores the saved next run, or starts the schedule from now.
func (s *Service) initialNext(ctx context.Context, sc *schedule, now time.Time) time.Time {
	blob, err := s.d.States.LoadTriggerState(ctx, sc.workflow, sc.node)
	if err == nil {
		var st scheduleState
		if json.Unmarshal(blob, &st) == nil && !st.Next.IsZero() {
			return st.Next
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		s.d.Logger.Warn("loading schedule state failed; starting fresh", "workflow", sc.workflow, "node", sc.node, "error", err)
	}
	next := sc.after(now, now)
	s.saveNext(ctx, sc, next)
	return next
}

func (s *Service) saveNext(ctx context.Context, sc *schedule, next time.Time) {
	blob, err := json.Marshal(scheduleState{Next: next})
	if err == nil {
		err = s.d.States.SaveTriggerState(ctx, sc.workflow, sc.node, blob)
	}
	if err != nil {
		s.d.Logger.Error("saving schedule state failed", "workflow", sc.workflow, "node", sc.node, "error", err)
	}
}

func (s *Service) removeSchedules(id domain.WorkflowID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byWF, id)
}

// Tick fires every schedule that is due. The leader calls it every
// schedule.tick; tests call it directly. A run that is overdue by more than
// schedule.missedGrace is a missed run and follows schedule.missedPolicy: skip
// drops it, runOnce starts one catch-up run. Either way the schedule continues
// with its next future time.
func (s *Service) Tick(ctx context.Context) {
	now := s.d.Clock.Now()
	for _, sc := range s.due(now) {
		s.fire(ctx, sc, now)
	}
}

type dueRun struct {
	sc   *schedule
	at   time.Time
	next time.Time
}

func (s *Service) due(now time.Time) []dueRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []dueRun
	for _, list := range s.byWF {
		for _, sc := range list {
			if !sc.next.After(now) {
				out = append(out, dueRun{sc: sc, at: sc.next, next: sc.after(sc.next, now)})
			}
		}
	}
	return out
}

func (s *Service) fire(ctx context.Context, d dueRun, now time.Time) {
	log := s.d.Logger.With("workflow", d.sc.workflow, "node", d.sc.node, "scheduled", d.at)
	missed := now.Sub(d.at) > s.cfg.Schedule.MissedGrace
	run := !missed || s.cfg.Schedule.MissedPolicy == config.MissedRunOnce
	if !run {
		log.Warn("skipping a missed schedule run", "late", now.Sub(d.at))
	} else if err := s.start(ctx, d); err != nil {
		log.Error("starting scheduled execution failed", "error", err)
		if !errors.Is(err, domain.ErrNotFound) && !isValidation(err) {
			return // transient: try again on the next tick
		}
	}
	s.mu.Lock()
	still := false
	for _, sc := range s.byWF[d.sc.workflow] {
		if sc == d.sc {
			sc.next, still = d.next, true
		}
	}
	s.mu.Unlock()
	if still {
		s.saveNext(ctx, d.sc, d.next)
	}
}

func isValidation(err error) bool {
	var v *domain.ValidationError
	return errors.As(err, &v)
}

// start queues the execution of one scheduled run. The dedup key names the
// scheduled time, so a restart that repeats the run does not start it twice.
func (s *Service) start(ctx context.Context, d dueRun) error {
	at := d.at.In(d.sc.loc)
	item := domain.Item{JSON: map[string]any{
		"timestamp": at.Format(time.RFC3339), "timezone": d.sc.loc.String(),
	}}
	_, err := s.d.Executions.Start(ctx, execution.StartRequest{
		WorkflowID: d.sc.workflow, Mode: domain.ModeTrigger, StartNode: d.sc.node, Items: []domain.Item{item},
		DedupKey: d.sc.node + ":" + d.at.UTC().Format(time.RFC3339Nano),
	})
	if errors.Is(err, domain.ErrDuplicate) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("starting execution: %w", err)
	}
	return nil
}
