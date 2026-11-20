package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/execution"
)

// RunPersisted runs a workflow through the persistent runtime: the workflow
// and the execution are stored in the SQLite database, the job goes through the
// durable queue and a worker runs it. Time-based waits are resumed by the wait
// scheduler. It returns when the execution has finished, or when it waits for
// an external signal, with the execution as stored.
func (a *App) RunPersisted(ctx context.Context, req RunRequest) (domain.Execution, error) {
	start := req.StartNode
	if start == "" {
		var err error
		if start, err = a.FindStartNode(req.Workflow); err != nil {
			return domain.Execution{}, err
		}
	}
	rt, err := a.NewRuntime(ctx)
	if err != nil {
		return domain.Execution{}, err
	}
	if err := rt.Start(ctx); err != nil {
		return domain.Execution{}, errors.Join(err, rt.Shutdown(context.WithoutCancel(ctx)))
	}
	ex, runErr := a.runStored(ctx, rt, req.Workflow, start, req.Items)

	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.Config.Worker.ShutdownGrace)
	defer cancel()
	if err := rt.Shutdown(sctx); err != nil {
		runErr = errors.Join(runErr, err)
	}
	return ex, runErr
}

func (a *App) runStored(ctx context.Context, rt *Runtime, wf domain.Workflow, start string, items []domain.Item) (domain.Execution, error) {
	if wf.ID == "" {
		id, err := a.ids.WorkflowID()
		if err != nil {
			return domain.Execution{}, fmt.Errorf("generating workflow id: %w", err)
		}
		wf.ID = id
	}
	saved, err := rt.Workflows.Save(ctx, wf)
	if err != nil {
		return domain.Execution{}, fmt.Errorf("storing workflow: %w", err)
	}
	id, err := rt.Executions.Start(ctx, execution.StartRequest{
		WorkflowID: saved.ID, Mode: domain.ModeManual, StartNode: start, Items: items,
	})
	if err != nil {
		return domain.Execution{}, fmt.Errorf("starting execution: %w", err)
	}
	for {
		ex, err := rt.Executions.Await(ctx, id)
		if err != nil {
			return domain.Execution{}, fmt.Errorf("waiting for execution: %w", err)
		}
		// Await also returns for a waiting execution. A timed wait is resumed by
		// the scheduler, so keep waiting; a wait for a signal ends the run.
		if ex.Status != domain.StatusWaiting || ex.WaitUntil == nil {
			return ex, nil
		}
		a.Logger.Info("waiting", "until", ex.WaitUntil.Format(time.RFC3339))
		if err := a.Sleeper.Sleep(ctx, a.Config.Worker.SyncPollInterval); err != nil {
			return ex, fmt.Errorf("waiting for execution: %w", err)
		}
	}
}

// ReportFromExecution summarises a stored execution like BuildReport does for
// an engine state. The stored execution does not keep the order in which nodes
// finished, so the last output is the one that ended last.
func ReportFromExecution(ex domain.Execution) Report {
	st := &engine.State{ExecutionID: ex.ID, Status: ex.Status, StartedAt: ex.StartedAt, RunData: ex.RunData, Error: ex.Error}
	if ex.FinishedAt != nil {
		st.FinishedAt = *ex.FinishedAt
	}
	for node, runs := range ex.RunData {
		for _, r := range runs {
			st.Order = append(st.Order, engine.RunRef{Node: node, RunIndex: r.RunIndex})
		}
	}
	end := func(r engine.RunRef) time.Time {
		run := ex.RunData[r.Node][r.RunIndex]
		return run.StartedAt.Add(run.Duration)
	}
	sort.Slice(st.Order, func(i, j int) bool {
		a, b := st.Order[i], st.Order[j]
		if ea, eb := end(a), end(b); !ea.Equal(eb) {
			return ea.Before(eb)
		}
		if a.Node != b.Node {
			return a.Node < b.Node
		}
		return a.RunIndex < b.RunIndex
	})
	rep := BuildReport(st)
	if ex.Status == domain.StatusWaiting && ex.WaitUntil != nil {
		rep.Pending = &ReportPending{Until: ex.WaitUntil}
	}
	return rep
}
