package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/engine"
)

// outcome tells process what to do with the job after execute returned.
type outcome int

const (
	// outcomeDone: the execution reached a stored state; complete the job.
	outcomeDone outcome = iota
	// outcomeReleased: the worker is stopping; give the job back untouched.
	outcomeReleased
)

// RunInline runs a stored execution to its next stopping point in the calling
// goroutine, without a queue job. Sub-workflows that a parent waits for use it.
func (w *Worker) RunInline(ctx context.Context, id domain.ExecutionID) error {
	out, err := w.execute(ctx, id, domain.RunHolder{})
	if err != nil {
		return err
	}
	if out == outcomeReleased {
		return fmt.Errorf("running execution %s: %w", id, errShuttingDown)
	}
	return nil
}

// execute loads an execution, claims it for holder, runs it, and stores what
// happened. holder is the zero value for an inline run. A returned error is an
// infrastructure failure (the job may be retried); anything the workflow itself
// does, failure included, is stored as the execution's result.
//
// Only the claim (queued to running) lets a job run the engine. When another
// job already holds the execution, this job is a duplicate and is dropped.
func (w *Worker) execute(ctx context.Context, id domain.ExecutionID, holder domain.RunHolder) (outcome, error) {
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	w.register(id, cancel)
	defer w.unregister(id)

	// Read after registering, so a cancel that raced with the claim is seen
	// either here (stored) or through the registered cancel (announced).
	ex, err := w.d.Executions.Get(runCtx, id)
	if errors.Is(err, domain.ErrNotFound) {
		w.d.Logger.Warn("job for a missing execution, dropping it", "execution", id)
		return outcomeDone, nil
	}
	if err != nil {
		return outcomeDone, fmt.Errorf("loading execution %s: %w", id, err)
	}
	if ex.Status.Terminal() {
		return outcomeDone, nil // finished or canceled while the job waited
	}
	if ex.Status == domain.StatusWaiting {
		// A waiting execution has no job of its own: the waiter or a resume
		// token queues a fresh one when the wait is over. This job is stale
		// (its worker stopped before acking the wait), so it must not resume
		// the execution early or without its token.
		w.d.Logger.Info("stale job for a waiting execution, dropping it", "execution", id)
		return outcomeDone, nil
	}

	req, err := w.request(runCtx, ex)
	if err != nil {
		return outcomeDone, err
	}
	claimed, ok, err := w.d.Executions.ClaimRun(runCtx, id, holder, w.d.Clock.Now())
	if err != nil {
		return outcomeDone, fmt.Errorf("marking execution %s running: %w", id, err)
	}
	if !ok {
		w.d.Logger.Info("execution already claimed by another job or ended, dropping this job",
			"execution", id, "job", holder.JobID, "status", claimed.Status)
		return outcomeDone, nil
	}
	ex = claimed

	state, err := w.d.Engine.Run(runCtx, req)
	if err != nil {
		// The request itself was unusable (invalid graph, unknown start node):
		// retrying cannot help, so the execution fails.
		return w.settle(runCtx, ex, &engine.State{
			ExecutionID: id, Status: domain.StatusError, RunData: ex.RunData,
			FinishedAt: w.d.Clock.Now(), Error: errorInfo(err),
		}, holder)
	}

	cause := context.Cause(runCtx)
	switch {
	case errors.Is(cause, errLeaseLost):
		return outcomeDone, nil // another worker owns the job now; store nothing
	case state.Status == domain.StatusCanceled && errors.Is(cause, errShuttingDown):
		w.requeue(runCtx, id, holder)
		return outcomeReleased, nil
	}
	return w.settle(runCtx, ex, state, holder)
}

// settle stores the result of the run this job holds. A result the fence refuses
// (a newer claim took the run over) is dropped: the job completes, and nothing
// is published or reported.
func (w *Worker) settle(ctx context.Context, ex domain.Execution, st *engine.State, holder domain.RunHolder) (outcome, error) {
	if err := w.finish(ctx, ex, st, holder); err != nil && !errors.Is(err, errRunLost) {
		return outcomeDone, err
	}
	return outcomeDone, nil
}

func (w *Worker) register(id domain.ExecutionID, cancel context.CancelCauseFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.running[id] = cancel
}

func (w *Worker) unregister(id domain.ExecutionID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.running, id)
}

// request builds the engine request from the stored execution and its saved
// input or waiting state.
func (w *Worker) request(ctx context.Context, ex domain.Execution) (engine.Request, error) {
	v, err := w.d.Versions.GetVersion(ctx, ex.WorkflowID, ex.WorkflowVersion)
	if err != nil {
		return engine.Request{}, fmt.Errorf("loading workflow %s version %d: %w", ex.WorkflowID, ex.WorkflowVersion, err)
	}
	wf := v.Workflow
	wf.Version = v.Version
	blob, err := w.d.States.LoadState(ctx, ex.ID)
	if err != nil {
		return engine.Request{}, fmt.Errorf("loading saved state of execution %s: %w", ex.ID, err)
	}
	input, state, err := w.d.Codec.DecodeSaved(blob)
	if err != nil {
		return engine.Request{}, fmt.Errorf("reading saved state of execution %s: %w", ex.ID, err)
	}
	depth, err := w.depth(ctx, ex)
	if err != nil {
		return engine.Request{}, err
	}
	if input != nil && input.Workflow != nil {
		wf = *input.Workflow // an unsaved document run from the editor
		wf.ID, wf.Version = ex.WorkflowID, ex.WorkflowVersion
	}
	req := engine.Request{
		ExecutionID: ex.ID, Workflow: wf, Mode: ex.Mode, Depth: depth,
		Observer: &busObserver{w: w, execution: ex.ID, workflow: ex.WorkflowID},
	}
	if w.d.Vars != nil {
		if req.Vars, err = w.d.Vars.Vars(ctx); err != nil {
			return engine.Request{}, fmt.Errorf("loading variables for execution %s: %w", ex.ID, err)
		}
	}
	if state != nil {
		req.Resume = state
	} else {
		req.StartNode, req.StartItems = input.StartNode, input.Items
		req.TraceParent = input.TraceParent
		req.PinData = input.PinData
		if input.Destination != "" {
			req.Destination = &engine.Destination{Node: input.Destination, Exclusive: input.DestinationExclusive}
		}
	}
	return req, nil
}

// depth counts the parents of an execution: its sub-workflow nesting level.
func (w *Worker) depth(ctx context.Context, ex domain.Execution) (int, error) {
	depth := 0
	for p := ex.ParentExecutionID; p != nil; depth++ {
		parent, err := w.d.Executions.Get(ctx, *p)
		if err != nil {
			return 0, fmt.Errorf("loading parent %s of execution %s: %w", *p, ex.ID, err)
		}
		p = parent.ParentExecutionID
	}
	return depth, nil
}

// finish stores the result of a run: a waiting execution keeps its state, a
// finished one drops it. The finish event follows the write so that a listener
// that reads the store always sees the result.
//
// The write is fenced by holder: when another claim holds the execution, the
// state save and the result are rolled back, the event is not published, and
// errRunLost is returned.
func (w *Worker) finish(ctx context.Context, ex domain.Execution, st *engine.State, holder domain.RunHolder) error {
	pctx, cancel := w.persistContext(ctx)
	defer cancel()

	ex.Status, ex.RunData, ex.Error = st.Status, st.RunData, st.Error
	ex.WaitUntil, ex.FinishedAt = nil, nil
	var saved []byte
	if st.Status == domain.StatusWaiting {
		if st.Pending != nil && !st.Pending.Until.IsZero() {
			until := st.Pending.Until
			ex.WaitUntil = &until
		}
		var err error
		if saved, err = w.d.Codec.EncodeState(st); err != nil {
			return fmt.Errorf("saving state of execution %s: %w", ex.ID, err)
		}
	} else {
		finished := st.FinishedAt
		if finished.IsZero() {
			finished = w.d.Clock.Now()
		}
		ex.FinishedAt = &finished
	}
	err := w.d.Tx.WithinTx(pctx, func(ctx context.Context) error {
		if saved != nil {
			if err := w.d.States.SaveState(ctx, ex.ID, saved); err != nil {
				return err //nolint:wrapcheck // wrapped below
			}
		}
		written, err := w.d.Executions.FinishRun(ctx, ex, holder)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		if !written {
			return errRunLost
		}
		if saved == nil {
			return w.d.States.DeleteState(ctx, ex.ID)
		}
		return nil
	})
	if errors.Is(err, errRunLost) {
		w.d.Logger.Warn("result dropped, a newer claim took the execution over",
			"execution", ex.ID, "job", holder.JobID, "attempt", holder.Attempt, "status", st.Status)
		return errRunLost
	}
	if err != nil {
		return fmt.Errorf("storing result of execution %s: %w", ex.ID, err)
	}
	w.publish(pctx, port.Event{
		Kind: port.EventExecutionFinished, ExecutionID: ex.ID, WorkflowID: ex.WorkflowID, Status: ex.Status, Error: ex.Error,
	})
	w.reportFailure(pctx, ex)
	w.logResult(ex)
	return nil
}

// logResult logs the end of a run, so the worker's log shows each execution
// it ran next to the "execution queued" line of the process that started it.
func (w *Worker) logResult(ex domain.Execution) {
	attrs := []any{"execution", ex.ID, "workflow", ex.WorkflowID, "mode", ex.Mode, "status", ex.Status, "nodes", len(ex.RunData)}
	if ex.FinishedAt != nil {
		attrs = append(attrs, "durationMs", ex.FinishedAt.Sub(ex.StartedAt).Milliseconds())
	}
	if ex.WaitUntil != nil {
		attrs = append(attrs, "waitUntil", *ex.WaitUntil)
	}
	switch {
	case ex.Error != nil:
		w.d.Logger.Warn("execution finished", append(attrs, "errorNode", ex.Error.Node, "error", ex.Error.Message)...)
	case ex.Status == domain.StatusWaiting:
		w.d.Logger.Info("execution waiting", attrs...)
	default:
		w.d.Logger.Info("execution finished", attrs...)
	}
}

// requeue puts an interrupted execution back to queued so its released job
// starts it again.
func (w *Worker) requeue(ctx context.Context, id domain.ExecutionID, holder domain.RunHolder) {
	pctx, cancel := w.persistContext(ctx)
	defer cancel()
	if _, err := w.d.Executions.RequeueRun(pctx, id, holder, nil); err != nil {
		w.d.Logger.Warn("resetting interrupted execution to queued failed", "execution", id, "error", err)
	}
}

func errorInfo(err error) *domain.ErrorInfo {
	var we *domain.WorkflowError
	if errors.As(err, &we) {
		info := we.Info()
		return &info
	}
	return &domain.ErrorInfo{Kind: domain.ErrorKindWorkflow, Message: err.Error()}
}

// busObserver forwards engine progress to the bus. The final event is sent by
// finish, after the result is stored.
type busObserver struct {
	w         *Worker
	execution domain.ExecutionID
	workflow  domain.WorkflowID
}

var _ engine.Observer = (*busObserver)(nil)

func (o *busObserver) ExecutionStarted(ctx context.Context, info engine.ExecutionInfo) {
	o.w.publish(ctx, port.Event{Kind: port.EventExecutionStarted, ExecutionID: o.execution, WorkflowID: o.workflow, At: info.StartedAt})
}

func (o *busObserver) NodeStarted(ctx context.Context, node string, runIndex int) {
	o.w.publish(ctx, port.Event{Kind: port.EventNodeStarted, ExecutionID: o.execution, WorkflowID: o.workflow, Node: node, RunIndex: runIndex})
}

func (o *busObserver) NodeFinished(ctx context.Context, node string, run domain.NodeRun) {
	o.w.publish(ctx, port.Event{Kind: port.EventNodeFinished, ExecutionID: o.execution, WorkflowID: o.workflow, Node: node, RunIndex: run.RunIndex, Run: &run})
}

func (*busObserver) ExecutionFinished(context.Context, engine.Result) {}
