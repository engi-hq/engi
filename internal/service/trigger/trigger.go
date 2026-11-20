// Package trigger activates workflows: it validates their trigger nodes,
// registers webhook routes, runs schedules (on the leader only) and records why
// an activation failed. It also resolves incoming webhook calls to a workflow
// node, for the HTTP adapter.
package trigger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Nodes instantiates node types; the node registry implements it.
type Nodes interface {
	Lookup(nodeType string, version int) (nodesdk.Node, error)
}

// Starter starts executions; *execution.Service implements it.
type Starter interface {
	Start(ctx context.Context, req execution.StartRequest) (domain.ExecutionID, error)
	// Await waits until the execution has finished or is waiting.
	Await(ctx context.Context, id domain.ExecutionID) (domain.Execution, error)
}

// Blobs gives pollers binary storage; *blob.Service implements it.
type Blobs interface {
	Scoped(execution domain.ExecutionID) nodesdk.BlobAccess
	DeleteExecution(ctx context.Context, execution domain.ExecutionID) error
}

// ExecutionIDs mints execution ids.
type ExecutionIDs interface {
	ExecutionID() (domain.ExecutionID, error)
}

// Deps are the collaborators of the Service. All but Blobs, IDs, Bus and Logger are required.
// Pollers get binary storage only when both Blobs and IDs are set.
type Deps struct {
	Workflows port.WorkflowStore
	Webhooks  port.WebhookStore
	// TestWebhooks holds the armed test webhooks, shared by every pod.
	TestWebhooks port.TestWebhookStore
	States       port.TriggerStateStore
	Activations  port.ActivationStore
	Credentials  port.CredentialResolver
	Nodes        Nodes
	Executions   Starter
	Blobs        Blobs
	IDs          ExecutionIDs
	Lease        port.LeaseProvider
	// Queue, when set, lets listeners pause while the job queue is deep.
	Queue port.QueueStats
	// Bus, when set, carries workflow.activated and workflow.deactivated so
	// that the leader of a cluster picks up schedules activated elsewhere.
	Bus     port.Bus
	Clock   port.Clock
	Sleeper port.Sleeper
	// Metrics is optional; nil means no-op.
	Metrics port.Metrics
	Logger  *slog.Logger
}

// Service implements activation, scheduling and webhook routing. It is safe for
// concurrent use.
type Service struct {
	cfg config.TriggerConfig
	d   Deps

	// opMu serializes activation changes with the leader's follow-up of
	// activation events, so a late event cannot undo a newer change.
	opMu sync.Mutex

	mu      sync.Mutex
	leading bool
	ready   bool // the leader has loaded the schedules of all active workflows
	byWF    map[domain.WorkflowID][]*schedule
	// pollers run while this process leads; leadCtx is the leadership context
	// they derive from. Both are guarded by mu.
	pollers map[domain.WorkflowID][]*poller
	leadCtx context.Context //nolint:containedctx // the leadership lifetime, like listenCtx

	// lmu guards the running listeners, which every instance runs.
	lmu       sync.Mutex
	listeners map[domain.WorkflowID][]*listener
	listenCtx context.Context // set by Start; listeners run only while it is live
	listenWG  sync.WaitGroup
	gate      *gate

	runMu   sync.Mutex
	started bool
	stop    context.CancelFunc
	done    chan struct{}
}

// New builds a Service.
func New(cfg config.TriggerConfig, d Deps) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid trigger config: %w", err)
	}
	if d.Workflows == nil || d.Webhooks == nil || d.TestWebhooks == nil || d.States == nil || d.Activations == nil || d.Credentials == nil ||
		d.Nodes == nil || d.Executions == nil || d.Lease == nil || d.Clock == nil || d.Sleeper == nil {
		return nil, errors.New("trigger service requires workflow, webhook, state and activation stores, credentials, nodes, executions, lease, clock and sleeper")
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.Metrics == nil {
		d.Metrics = port.NopMetrics{}
	}
	k := cfg.Kafka
	g := &gate{metrics: d.Metrics, stats: d.Queue, clock: d.Clock, pause: k.PauseAboveDepth, resume: k.ResumeBelowDepth, interval: k.DepthCheckInterval, log: d.Logger}
	return &Service{
		cfg: cfg, d: d, byWF: map[domain.WorkflowID][]*schedule{}, pollers: map[domain.WorkflowID][]*poller{},
		listeners: map[domain.WorkflowID][]*listener{}, gate: g,
	}, nil
}

// Activate validates the workflow's trigger nodes, registers its webhooks and
// schedules and marks it active. Activating an active workflow re-registers it.
// When validation or registration fails the workflow ends up inactive and the
// reason is recorded (ActivationStore.ActivationError).
func (s *Service) Activate(ctx context.Context, id domain.WorkflowID) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	wf, err := s.d.Workflows.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("activating workflow %s: %w", id, err)
	}
	if err := s.activate(ctx, wf); err != nil {
		s.failActivation(ctx, id, err)
		return fmt.Errorf("activating workflow %s: %w", id, err)
	}
	s.publish(ctx, port.EventWorkflowActivated, id)
	return nil
}

func (s *Service) activate(ctx context.Context, wf domain.Workflow) error {
	p, err := s.inspect(ctx, wf)
	if err != nil {
		return err
	}
	if len(p.webhooks) == 0 && len(p.schedules) == 0 && len(p.listeners) == 0 && len(p.polls) == 0 {
		return &domain.ValidationError{Issues: []domain.Issue{{Path: "nodes", Message: "the workflow has no enabled Webhook, Schedule, polling or listener trigger node to activate"}}}
	}
	if err := s.registerWebhooks(ctx, wf.ID, p.webhooks); err != nil {
		return err
	}
	if err := s.d.Workflows.SetActive(ctx, wf.ID, true); err != nil {
		return fmt.Errorf("marking active: %w", err)
	}
	if err := s.d.Activations.ClearActivationError(ctx, wf.ID); err != nil {
		return fmt.Errorf("clearing activation error: %w", err)
	}
	s.setSchedules(ctx, wf.ID, p.schedules)
	s.setPollers(wf.ID, p.polls)             //nolint:contextcheck // pollers outlive the activating request
	s.setListeners(wf.ID, p.listeners, true) //nolint:contextcheck // listeners outlive the activating request
	return nil
}

// failActivation leaves the workflow inactive and records why. Cleanup errors
// are logged: the caller gets the original failure.
func (s *Service) failActivation(ctx context.Context, id domain.WorkflowID, cause error) {
	log := s.d.Logger.With("workflow", id)
	if err := s.teardown(ctx, id, true); err != nil {
		log.Error("cleaning up a failed activation", "error", err)
	}
	if err := s.d.Workflows.SetActive(ctx, id, false); err != nil && !errors.Is(err, domain.ErrNotFound) {
		log.Error("marking the workflow inactive", "error", err)
	}
	if err := s.d.Activations.SetActivationError(ctx, id, cause.Error()); err != nil && !errors.Is(err, domain.ErrNotFound) {
		log.Error("recording the activation error", "error", err)
	}
}

// Deactivate removes the workflow's webhooks, stops its schedules and marks it
// inactive.
func (s *Service) Deactivate(ctx context.Context, id domain.WorkflowID) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.teardown(ctx, id, true); err != nil {
		return fmt.Errorf("deactivating workflow %s: %w", id, err)
	}
	if err := s.d.Workflows.SetActive(ctx, id, false); err != nil {
		return fmt.Errorf("deactivating workflow %s: %w", id, err)
	}
	if err := s.d.Activations.ClearActivationError(ctx, id); err != nil {
		return fmt.Errorf("deactivating workflow %s: %w", id, err)
	}
	s.publish(ctx, port.EventWorkflowDeactivated, id)
	return nil
}

// teardown removes routes and schedules; dropState also forgets schedule state.
func (s *Service) teardown(ctx context.Context, id domain.WorkflowID, dropState bool) error {
	s.removeSchedules(id)
	s.stopPollers(id)
	s.stopListeners(id)
	if err := s.d.Webhooks.DeleteByWorkflow(ctx, id); err != nil {
		return fmt.Errorf("removing webhooks: %w", err)
	}
	if err := s.d.TestWebhooks.DeleteByWorkflow(ctx, id); err != nil {
		return fmt.Errorf("disarming test webhooks: %w", err)
	}
	if dropState {
		if err := s.d.States.DeleteTriggerState(ctx, id); err != nil {
			return fmt.Errorf("removing trigger state: %w", err)
		}
	}
	return nil
}

// ActivateAll registers the triggers of every active workflow, for process
// start. A workflow that fails to activate is made inactive with the reason
// recorded and logged; it does not stop the others. The returned error is
// about reading the workflow list.
func (s *Service) ActivateAll(ctx context.Context) error {
	active := true
	page := port.Page{Limit: s.cfg.Webhook.ActivationPageSize}
	for {
		res, err := s.d.Workflows.List(ctx, port.WorkflowFilter{Active: &active}, page)
		if err != nil {
			return fmt.Errorf("listing active workflows: %w", err)
		}
		for _, wf := range res.Items {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("activating workflows: %w", err)
			}
			if err := s.Activate(ctx, wf.ID); err != nil {
				s.d.Logger.Error("activating workflow at startup failed; it was made inactive", "workflow", wf.ID, "error", err)
			}
		}
		if res.NextCursor == "" {
			return nil
		}
		page.Cursor = res.NextCursor
	}
}

func (s *Service) publish(ctx context.Context, kind port.EventKind, id domain.WorkflowID) {
	if s.d.Bus == nil {
		return
	}
	if err := s.d.Bus.Publish(ctx, port.Event{Kind: kind, WorkflowID: id, At: s.d.Clock.Now()}); err != nil {
		s.d.Logger.Warn("publishing activation event failed", "workflow", id, "kind", kind, "error", err)
	}
}
