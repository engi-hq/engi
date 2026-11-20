// Package execution holds the use cases around executions: starting one (which
// stores it and queues a job), reading, listing, canceling, retrying, and
// running one synchronously. Running the workflow itself is the worker's job.
package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
)

// Errors returned by the use cases; check with errors.Is.
var (
	// ErrNotCancelable: the execution has already finished.
	ErrNotCancelable = errors.New("execution is not cancelable")
	// ErrNotRetryable: only failed, crashed or canceled executions can be retried.
	ErrNotRetryable = errors.New("execution is not retryable")
	// ErrSyncTimeout: a synchronous run did not finish in time. The execution
	// keeps running; fetch it with Get.
	ErrSyncTimeout = errors.New("timed out waiting for the execution")
)

// IDGenerator creates identifiers.
type IDGenerator interface {
	ExecutionID() (domain.ExecutionID, error)
	JobID() (domain.JobID, error)
}

// InputEncoder serializes the input of an execution that has not run yet.
type InputEncoder interface {
	EncodeInput(in domain.ExecutionInput) ([]byte, error)
}

// BlobCleaner removes the binary data of executions; *blob.Service implements it.
type BlobCleaner interface {
	DeleteExecution(ctx context.Context, id domain.ExecutionID) error
	Sweep(ctx context.Context, now time.Time, exists func(context.Context, domain.ExecutionID) (bool, error)) (int, error)
}

// Deps are the collaborators of the Service. All but Blobs and Logger are required.
type Deps struct {
	Workflows  port.WorkflowStore
	Versions   port.WorkflowVersionStore
	Executions port.ExecutionStore
	States     port.ExecutionStateStore
	Queue      port.JobQueue
	Tx         port.TxManager
	Bus        port.Bus
	Clock      port.Clock
	IDs        IDGenerator
	Codec      InputEncoder
	// Blobs, when set, is cleaned when executions are deleted or pruned.
	Blobs BlobCleaner
	// Tracer, when set, stores the caller's trace context with the execution.
	Tracer port.Tracer
	Logger *slog.Logger
}

// Service implements the execution use cases. It is safe for concurrent use.
type Service struct {
	cfg config.WorkerConfig
	d   Deps
}

// New builds a Service.
func New(cfg config.WorkerConfig, d Deps) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid worker config: %w", err)
	}
	if d.Workflows == nil || d.Versions == nil || d.Executions == nil || d.States == nil || d.Queue == nil ||
		d.Tx == nil || d.Bus == nil || d.Clock == nil || d.IDs == nil || d.Codec == nil {
		return nil, errors.New("execution service requires workflow, version, execution and state stores, queue, tx manager, bus, clock, id generator and codec")
	}
	if d.Tracer == nil {
		d.Tracer = port.NopTracer{}
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{cfg: cfg, d: d}, nil
}

// StartRequest describes a new execution.
type StartRequest struct {
	WorkflowID domain.WorkflowID
	// Version selects a workflow version; zero means the current one.
	Version int
	Mode    domain.ExecutionMode
	// StartNode is the node the run begins at.
	StartNode string
	// ExecutionID, when set, is used instead of a generated id. The webhook
	// handler sets it so uploaded files can be stored under the execution
	// before it exists. Callers must pass a fresh id from the same generator.
	ExecutionID domain.ExecutionID
	// Items feed the start node. Empty means one empty item.
	Items []domain.Item
	// DedupKey, when set, makes the start idempotent per workflow.
	DedupKey string
	// ParentExecutionID links a sub-workflow execution to its caller.
	ParentExecutionID *domain.ExecutionID
	// Destination makes the run partial (see domain.ExecutionInput). Empty runs
	// the whole workflow.
	Destination          string
	DestinationExclusive bool
	// PinData overrides node output for this run only.
	PinData map[string][]domain.Item
	// Workflow is an unsaved document to run instead of the stored version. The
	// caller must have validated it. The execution still records the stored
	// version it was started against; nothing is persisted as a new version.
	Workflow *domain.Workflow
}

// Start stores a queued execution and enqueues a job for it. When DedupKey is
// already taken it returns the id of the existing execution together with an
// error matching domain.ErrDuplicate (a *domain.DuplicateError), so callers
// can treat a repeated delivery as success.
func (s *Service) Start(ctx context.Context, req StartRequest) (domain.ExecutionID, error) {
	return s.create(ctx, req, true)
}

// create stores the execution and, when enqueue is set, queues its job.
func (s *Service) create(ctx context.Context, req StartRequest, enqueue bool) (domain.ExecutionID, error) {
	if !req.Mode.Valid() {
		return "", &domain.ValidationError{Issues: []domain.Issue{{Path: "mode", Message: fmt.Sprintf("unknown execution mode %q", req.Mode)}}}
	}
	wf, err := s.workflow(ctx, req.WorkflowID, req.Version) // the stored version the run is recorded against
	if err != nil {
		return "", err
	}
	graph := wf
	if req.Workflow != nil {
		graph = *req.Workflow
	}
	if _, ok := graph.NodeByName(req.StartNode); !ok {
		return "", &domain.ValidationError{Issues: []domain.Issue{{Path: "startNode", Message: fmt.Sprintf("workflow %s has no node %q", wf.ID, req.StartNode)}}}
	}
	if req.Destination != "" {
		if _, ok := graph.NodeByName(req.Destination); !ok {
			return "", &domain.ValidationError{Issues: []domain.Issue{{Path: "destination.node", Message: fmt.Sprintf("workflow has no node %q", req.Destination)}}}
		}
	}
	id := req.ExecutionID
	if id == "" {
		id, err = s.d.IDs.ExecutionID()
		if err != nil {
			return "", fmt.Errorf("starting execution: %w", err)
		}
	}
	payload, err := s.d.Codec.EncodeInput(domain.ExecutionInput{
		StartNode: req.StartNode, Items: req.Items, Destination: req.Destination,
		DestinationExclusive: req.DestinationExclusive, PinData: req.PinData, Workflow: req.Workflow,
		TraceParent: s.d.Tracer.Inject(ctx),
	})
	if err != nil {
		return "", fmt.Errorf("starting execution: %w", err)
	}
	ex := domain.Execution{
		ID: id, WorkflowID: wf.ID, WorkflowVersion: wf.Version, Mode: req.Mode, Status: domain.StatusQueued,
		StartedAt: s.d.Clock.Now(), DedupKey: req.DedupKey, ParentExecutionID: req.ParentExecutionID,
	}
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.d.Executions.Create(ctx, ex); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		if err := s.d.States.SaveState(ctx, id, payload); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		if !enqueue {
			return nil
		}
		return s.enqueue(ctx, id)
	})
	var dup *domain.DuplicateError
	if errors.As(err, &dup) {
		return dup.ExistingID, fmt.Errorf("starting execution of workflow %s: %w", wf.ID, err)
	}
	if err != nil {
		return "", fmt.Errorf("starting execution of workflow %s: %w", wf.ID, err)
	}
	s.d.Logger.Info("execution queued", "execution", id, "workflow", wf.ID, "mode", req.Mode)
	return id, nil
}

// enqueue adds a job for the execution.
func (s *Service) enqueue(ctx context.Context, id domain.ExecutionID) error {
	jid, err := s.d.IDs.JobID()
	if err != nil {
		return fmt.Errorf("creating job id: %w", err)
	}
	if err := s.d.Queue.Enqueue(ctx, domain.Job{ID: jid, ExecutionID: id}); err != nil {
		return fmt.Errorf("enqueueing execution %s: %w", id, err)
	}
	return nil
}

func (s *Service) workflow(ctx context.Context, id domain.WorkflowID, version int) (domain.Workflow, error) {
	if version <= 0 {
		wf, err := s.d.Workflows.Get(ctx, id)
		if err != nil {
			return domain.Workflow{}, fmt.Errorf("loading workflow %s: %w", id, err)
		}
		return wf, nil
	}
	v, err := s.d.Versions.GetVersion(ctx, id, version)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("loading workflow %s version %d: %w", id, version, err)
	}
	wf := v.Workflow
	wf.Version = v.Version
	return wf, nil
}

// Get returns one execution with its run data.
func (s *Service) Get(ctx context.Context, id domain.ExecutionID) (domain.Execution, error) {
	ex, err := s.d.Executions.Get(ctx, id)
	if err != nil {
		return domain.Execution{}, fmt.Errorf("getting execution %s: %w", id, err)
	}
	return ex, nil
}

// List returns a page of executions, newest first, without run data.
func (s *Service) List(ctx context.Context, f port.ExecutionFilter, p port.Page) (port.PageResult[domain.Execution], error) {
	res, err := s.d.Executions.List(ctx, f, p)
	if err != nil {
		return port.PageResult[domain.Execution]{}, fmt.Errorf("listing executions: %w", err)
	}
	return res, nil
}

// Cancel stops an execution. One that has not started running, or is waiting,
// is marked canceled at once; one that is running is told to stop through the
// bus and ends as canceled when its worker notices.
func (s *Service) Cancel(ctx context.Context, id domain.ExecutionID) error {
	ex, err := s.d.Executions.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("canceling execution %s: %w", id, err)
	}
	if ex.Status.Terminal() {
		return fmt.Errorf("canceling execution %s (%s): %w", id, ex.Status, ErrNotCancelable)
	}
	if ex.Status != domain.StatusRunning {
		now := s.d.Clock.Now()
		ex.Status, ex.FinishedAt, ex.WaitUntil = domain.StatusCanceled, &now, nil
		ex.Error = &domain.ErrorInfo{Kind: domain.ErrorKindCanceled, Message: "execution canceled"}
		if err := s.d.Executions.Update(ctx, ex); err != nil {
			return fmt.Errorf("canceling execution %s: %w", id, err)
		}
		if err := s.d.States.DeleteState(ctx, id); err != nil {
			return fmt.Errorf("canceling execution %s: %w", id, err)
		}
	}
	// Always announce: the execution may have started between the read and the update.
	if err := s.d.Bus.Publish(ctx, port.Event{Kind: port.EventCancelRequested, ExecutionID: id, WorkflowID: ex.WorkflowID, At: s.d.Clock.Now()}); err != nil {
		return fmt.Errorf("announcing cancellation of %s: %w", id, err)
	}
	return nil
}

// Retry starts a new execution, in mode retry, with the input of a failed one:
// the same workflow version, start node and start items.
func (s *Service) Retry(ctx context.Context, id domain.ExecutionID) (domain.ExecutionID, error) {
	ex, err := s.d.Executions.Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("retrying execution %s: %w", id, err)
	}
	switch ex.Status {
	case domain.StatusError, domain.StatusCrashed, domain.StatusCanceled:
	default:
		return "", fmt.Errorf("retrying execution %s (%s): %w", id, ex.Status, ErrNotRetryable)
	}
	start, items, ok := startOf(ex)
	if !ok {
		return "", fmt.Errorf("retrying execution %s: it never ran its start node: %w", id, ErrNotRetryable)
	}
	return s.Start(ctx, StartRequest{
		WorkflowID: ex.WorkflowID, Version: ex.WorkflowVersion, Mode: domain.ModeRetry,
		StartNode: start, Items: items, ParentExecutionID: ex.ParentExecutionID,
	})
}

// startOf recovers the start node and its input from the recorded runs: the
// start task is the only one with no source.
func startOf(ex domain.Execution) (node string, items []domain.Item, ok bool) {
	for name, runs := range ex.RunData {
		for _, r := range runs {
			if len(r.Sources) > 0 && r.Sources[0].Node != "" {
				continue
			}
			if ports := r.Input[domain.PortMain]; len(ports) > 0 && (!ok || name < node) {
				node, items, ok = name, ports[0], true
			}
		}
	}
	return node, items, ok
}
