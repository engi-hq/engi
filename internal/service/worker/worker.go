// Package worker runs executions: it claims jobs from the queue, runs the
// engine, stores the outcome, keeps the job lease alive while it works, and
// recovers the jobs of workers that died.
//
// Delivery is at-least-once. A job whose worker disappears is reclaimed when
// its lease expires and run again from the saved input (or the saved waiting
// state), so nodes with side effects can run twice after a crash.
package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/engine"
)

// Reasons an execution is stopped from outside. They travel as context causes.
var (
	errCancelRequested = errors.New("execution cancel requested")
	errShuttingDown    = errors.New("worker is shutting down")
	errLeaseLost       = errors.New("job lease lost")
	// errRunLost means a newer claim holds the execution, so this job's result is dropped.
	errRunLost = errors.New("run taken over by a newer claim")
)

// ErrNotStarted is returned by Shutdown when Start was not called.
var ErrNotStarted = errors.New("worker not started")

// Runner runs a workflow. *engine.Engine implements it.
type Runner interface {
	Run(ctx context.Context, req engine.Request) (*engine.State, error)
}

// FailureHandler is told about every execution that ends in error or crashed,
// after its result is stored. It must not block for long and cannot fail the
// execution: the error-workflow notifier implements it.
type FailureHandler interface {
	ExecutionFailed(ctx context.Context, ex domain.Execution)
}

// Deps are the collaborators of the Worker. All but Logger are required.
type Deps struct {
	// WorkerID names this worker as the owner of the leases it takes.
	WorkerID   string
	Dispatcher port.JobDispatcher
	Queue      port.JobQueue
	Reclaimer  port.JobReclaimer
	Executions port.ExecutionStore
	States     port.ExecutionStateStore
	Versions   port.WorkflowVersionStore
	Tx         port.TxManager
	Bus        port.Bus
	Clock      port.Clock
	Engine     Runner
	// Vars supplies $vars; nil means no variables.
	Vars  port.VarsResolver
	Codec engine.StateCodec
	// Metrics is optional; nil means no-op.
	Metrics port.Metrics
	// Failures, when set, is told about failed and crashed executions.
	Failures FailureHandler
	Logger   *slog.Logger
}

// Worker runs queued executions with a fixed number of concurrent slots.
type Worker struct {
	cfg   config.WorkerConfig
	queue config.QueueConfig
	d     Deps

	mu        sync.Mutex
	running   map[domain.ExecutionID]context.CancelCauseFunc
	busy      atomic.Int64
	started   bool
	stopClaim context.CancelFunc
	wg        sync.WaitGroup
}

// New builds a Worker.
func New(cfg config.WorkerConfig, queue config.QueueConfig, d Deps) (*Worker, error) {
	if err := errors.Join(cfg.Validate(), queue.Validate()); err != nil {
		return nil, fmt.Errorf("invalid worker config: %w", err)
	}
	if d.WorkerID == "" || d.Dispatcher == nil || d.Queue == nil || d.Reclaimer == nil || d.Executions == nil ||
		d.States == nil || d.Versions == nil || d.Tx == nil || d.Bus == nil || d.Clock == nil || d.Engine == nil || d.Codec == nil {
		return nil, errors.New("worker requires an id, dispatcher, queue, reclaimer, stores, tx manager, bus, clock, engine and codec")
	}
	if d.Metrics == nil {
		d.Metrics = port.NopMetrics{}
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	d.Logger = d.Logger.With("worker", d.WorkerID)
	return &Worker{cfg: cfg, queue: queue, d: d, running: map[domain.ExecutionID]context.CancelCauseFunc{}}, nil
}

// Start launches the slots, the lease reclaimer and the cancel listener. It
// returns once the cancel listener is subscribed. Canceling ctx stops claiming
// new jobs; call Shutdown to wait for the running ones.
func (w *Worker) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return errors.New("worker already started")
	}
	cancelSub, err := w.d.Bus.Subscribe(ctx, port.EventFilter{Kinds: []port.EventKind{port.EventCancelRequested}})
	if err != nil {
		return fmt.Errorf("subscribing to cancel requests: %w", err)
	}
	claimCtx, stop := context.WithCancel(ctx)
	// Running executions outlive a canceled ctx: Shutdown gives them a grace period.
	runCtx := context.WithoutCancel(ctx)
	w.started, w.stopClaim = true, stop

	w.goroutine(func() { w.listenForCancels(claimCtx, cancelSub) })
	w.goroutine(func() { w.reclaimLoop(claimCtx) })
	for range w.cfg.Concurrency {
		w.goroutine(func() { w.slot(claimCtx, runCtx) })
	}
	return nil
}

func (w *Worker) goroutine(f func()) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		f()
	}()
}

// Shutdown stops claiming jobs and waits for the running executions. After the
// configured grace period (or when ctx ends) it stops them and releases their
// jobs so another worker can pick them up, then waits for that to finish.
func (w *Worker) Shutdown(ctx context.Context) error {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return ErrNotStarted
	}
	stop := w.stopClaim
	w.mu.Unlock()
	stop()

	done := make(chan struct{})
	go func() { w.wg.Wait(); close(done) }()

	grace := time.NewTimer(w.cfg.ShutdownGrace)
	defer grace.Stop()
	select {
	case <-done:
		return nil
	case <-grace.C:
		w.d.Logger.Warn("shutdown grace period over, stopping running executions")
	case <-ctx.Done():
	}
	w.cancelAll(errShuttingDown)
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for executions to stop: %w", ctx.Err())
	}
}

func (w *Worker) cancelAll(cause error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, cancel := range w.running {
		cancel(cause)
	}
}

// cancelExecution stops one running execution; it reports whether it was running here.
func (w *Worker) cancelExecution(id domain.ExecutionID, cause error) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	cancel, ok := w.running[id]
	if ok {
		cancel(cause)
	}
	return ok
}

func (w *Worker) listenForCancels(ctx context.Context, sub port.Subscription) {
	defer sub.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.Events():
			if !ok {
				return
			}
			if w.cancelExecution(e.ExecutionID, errCancelRequested) {
				w.d.Logger.Info("canceling execution", "execution", e.ExecutionID)
			}
		}
	}
}

// slot is one concurrent executor: claim a job, process it, repeat.
func (w *Worker) slot(claimCtx, runCtx context.Context) {
	for claimCtx.Err() == nil {
		job, err := w.d.Dispatcher.Next(claimCtx, w.d.WorkerID, w.queue.LeaseDuration)
		if err != nil {
			if claimCtx.Err() != nil {
				return
			}
			w.d.Logger.Error("claiming a job failed", "error", err)
			if !sleep(claimCtx, w.queue.PollInterval) {
				return
			}
			continue
		}
		w.d.Metrics.JobClaimed(w.claimLatency(job))
		w.d.Metrics.WorkerBusy(int(w.busy.Add(1)))
		w.process(runCtx, job)
		w.d.Metrics.WorkerBusy(int(w.busy.Add(-1)))
	}
}

// claimLatency is how long a job waited between becoming available and being
// claimed.
func (w *Worker) claimLatency(job domain.Job) time.Duration {
	since := job.AvailableAt
	if since.IsZero() {
		since = job.CreatedAt
	}
	return max(0, w.d.Clock.Now().Sub(since))
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// reclaimLoop recovers jobs of workers whose lease ran out.
func (w *Worker) reclaimLoop(ctx context.Context) {
	t := time.NewTicker(w.cfg.ReclaimInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.ReclaimOnce(ctx); err != nil && ctx.Err() == nil {
				w.d.Logger.Error("reclaiming expired jobs failed", "error", err)
			}
		}
	}
}

// ReclaimOnce recovers expired jobs once. Executions whose job ran out of
// attempts are marked crashed; the others go back to queued and record the
// recovery. Every job is handled even when one of them fails.
func (w *Worker) ReclaimOnce(ctx context.Context) error {
	jobs, err := w.d.Reclaimer.ReclaimExpired(ctx)
	if err != nil {
		return fmt.Errorf("reclaiming jobs: %w", err)
	}
	var errs []error
	for _, j := range jobs {
		if j.Status == domain.JobFailed {
			w.d.Metrics.JobReclaimed(port.ReclaimFailed)
			w.d.Logger.Error("job lost its worker too often, marking execution crashed",
				"job", j.ID, "execution", j.ExecutionID, "previous_owner", j.PreviousOwner, "attempts", j.Attempts)
			w.markFailed(ctx, holderOf(j.Job), j.ExecutionID, domain.StatusCrashed, "the worker running this execution stopped responding and the retries ran out")
			continue
		}
		w.d.Metrics.JobReclaimed(port.ReclaimRequeued)
		w.d.Logger.Warn("job lease expired, job requeued",
			"job", j.ID, "execution", j.ExecutionID, "previous_owner", j.PreviousOwner, "attempts", j.Attempts)
		if err := w.recoverExecution(ctx, j); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// holderOf is the run holder for the claim a job is in: its id and attempt.
func holderOf(j domain.Job) domain.RunHolder {
	return domain.RunHolder{JobID: j.ID, Attempt: j.Attempts}
}

// recoverExecution shows the run of a reclaimed job as queued again and records
// the recovery. It acts only while the job still holds the run, so a job whose
// execution is driven by a newer job leaves it alone.
func (w *Worker) recoverExecution(ctx context.Context, j domain.ReclaimedJob) error {
	now := w.d.Clock.Now()
	if _, err := w.d.Executions.RequeueRun(ctx, j.ExecutionID, holderOf(j.Job), &now); err != nil {
		return fmt.Errorf("requeueing execution %s of reclaimed job %s: %w", j.ExecutionID, j.ID, err)
	}
	return nil
}

// markFailed ends an execution that its job could not finish, unless it already
// ended or a newer claim holds it. The write is fenced by holder.
func (w *Worker) markFailed(ctx context.Context, holder domain.RunHolder, id domain.ExecutionID, status domain.ExecutionStatus, msg string) {
	ctx, cancel := w.persistContext(ctx)
	defer cancel()
	ex, err := w.d.Executions.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			w.d.Logger.Error("loading execution to mark it failed", "execution", id, "error", err)
		}
		return
	}
	if ex.Status.Terminal() {
		return
	}
	now := w.d.Clock.Now()
	ex.Status, ex.FinishedAt, ex.WaitUntil = status, &now, nil
	ex.Error = &domain.ErrorInfo{Kind: domain.ErrorKindWorkflow, Message: msg}
	err = w.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		written, err := w.d.Executions.CrashRun(ctx, ex, holder)
		if err != nil {
			return err //nolint:wrapcheck // logged below
		}
		if !written {
			return errRunLost
		}
		return w.d.States.DeleteState(ctx, id)
	})
	if errors.Is(err, errRunLost) {
		w.d.Logger.Warn("crash dropped, a newer claim holds the execution", "execution", id, "job", holder.JobID)
		return
	}
	if err != nil {
		w.d.Logger.Error("marking execution failed", "execution", id, "error", err)
		return
	}
	w.publish(ctx, port.Event{Kind: port.EventExecutionFinished, ExecutionID: id, WorkflowID: ex.WorkflowID, Status: status, Error: ex.Error})
	w.reportFailure(ctx, ex)
}

// reportFailure tells the failure handler about an execution that just ended.
func (w *Worker) reportFailure(ctx context.Context, ex domain.Execution) {
	if w.d.Failures == nil || (ex.Status != domain.StatusError && ex.Status != domain.StatusCrashed) {
		return
	}
	w.d.Failures.ExecutionFailed(ctx, ex)
}

// persistContext returns a context for final writes that survives the
// cancellation of ctx, bounded by worker.persistTimeout.
func (w *Worker) persistContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), w.cfg.PersistTimeout)
}

func (w *Worker) publish(ctx context.Context, e port.Event) {
	if e.At.IsZero() {
		e.At = w.d.Clock.Now()
	}
	if err := w.d.Bus.Publish(ctx, e); err != nil {
		w.d.Logger.Warn("publishing event failed", "kind", e.Kind, "execution", e.ExecutionID, "error", err)
	}
}

// process runs the execution of one claimed job and settles the job.
func (w *Worker) process(ctx context.Context, job domain.Job) {
	jobCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	log := w.d.Logger.With("job", job.ID, "execution", job.ExecutionID, "attempt", job.Attempts)

	stopBeat := w.heartbeat(jobCtx, cancel, job)
	out, err := w.execute(jobCtx, job.ExecutionID, holderOf(job))
	stopBeat()

	sctx, scancel := w.persistContext(ctx)
	defer scancel()
	switch {
	case errors.Is(context.Cause(jobCtx), errLeaseLost):
		log.Warn("lease lost while running, leaving the job to its new owner")
	case err != nil:
		w.failJob(sctx, log, job, err)
	case out == outcomeReleased:
		if rerr := w.d.Queue.Release(sctx, job.ID, w.d.WorkerID); rerr != nil {
			log.Error("releasing job failed", "error", rerr)
		}
	default:
		if cerr := w.d.Queue.Complete(sctx, job.ID, w.d.WorkerID); cerr != nil {
			log.Error("completing job failed", "error", cerr)
		}
	}
}

// failJob hands a job that hit an infrastructure error back for a retry, or
// ends its execution when the attempts are used up.
func (w *Worker) failJob(ctx context.Context, log *slog.Logger, job domain.Job, cause error) {
	log.Error("execution failed with an infrastructure error", "error", cause)
	// The execution goes back to queued before the job, so the retry never
	// finds the run still held by this failed attempt.
	if _, err := w.d.Executions.RequeueRun(ctx, job.ExecutionID, holderOf(job), nil); err != nil {
		log.Error("resetting execution to queued for the retry failed", "error", err)
	}
	failed, err := w.d.Queue.Fail(ctx, job.ID, w.d.WorkerID, cause.Error(), w.d.Clock.Now().Add(w.queue.RetryDelay))
	if err != nil {
		log.Error("failing job failed", "error", err)
		return
	}
	if failed.Status == domain.JobFailed {
		w.markFailed(ctx, holderOf(job), job.ExecutionID, domain.StatusCrashed, "the execution could not be run: "+cause.Error())
	}
}

// heartbeat extends the job lease on an interval until the returned function
// is called. A lost lease cancels the execution.
func (w *Worker) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, job domain.Job) (stop func()) {
	hctx, hcancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(w.queue.HeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-hctx.Done():
				return
			case <-t.C:
				err := w.d.Queue.Heartbeat(hctx, job.ID, w.d.WorkerID, w.queue.LeaseDuration)
				switch {
				case errors.Is(err, domain.ErrLeaseLost):
					cancel(errLeaseLost)
					return
				case err != nil && hctx.Err() == nil:
					w.d.Logger.Warn("heartbeat failed", "job", job.ID, "error", err)
				}
			}
		}
	}()
	return func() { hcancel(); <-done }
}
