package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// NodeRegistry resolves node types. A version of 0 selects the latest.
type NodeRegistry interface {
	Lookup(nodeType string, version int) (nodesdk.Node, error)
}

// BlobScopes hands a node the binary-data access of its own execution;
// *blob.Service implements it.
type BlobScopes interface {
	Scoped(execution domain.ExecutionID) nodesdk.BlobAccess
}

// Deps are the collaborators of the engine. Blob, HTTP, Observer and Logger
// are optional.
type Deps struct {
	Registry    NodeRegistry
	Evaluator   port.ExpressionEvaluator
	Clock       port.Clock
	Sleeper     port.Sleeper
	Credentials port.CredentialResolver
	Blob        BlobScopes
	HTTP        nodesdk.HTTPClient
	Observer    Observer
	// Metrics and Tracer are optional; nil means no-op.
	Metrics port.Metrics
	Tracer  port.Tracer
	Logger  *slog.Logger
	// Random draws execution resume secrets. Nil means crypto/rand.
	Random io.Reader
	// ResumeBase is the absolute URL prefix of the wait resume route, for
	// example https://engi.example.com/hook-wait. $execution.resumeUrl is
	// ResumeBase + "/" + token. Empty leaves the resume URL unset.
	ResumeBase string
}

// Engine runs workflows. It is safe for concurrent use; each Run is independent.
type Engine struct {
	cfg config.EngineConfig
	d   Deps
}

// Destination limits a run to the nodes needed to compute Node (a partial run).
// The destination runs; its children do not.
type Destination struct {
	Node string
	// Exclusive stops the run before Node: its ancestors run, it does not.
	Exclusive bool
}

// Request describes one engine invocation.
type Request struct {
	ExecutionID domain.ExecutionID
	Workflow    domain.Workflow
	Mode        domain.ExecutionMode
	// StartNode is the trigger (or chosen) node. Ignored when resuming.
	StartNode string
	// StartItems are the start node's input. Empty means one empty item.
	StartItems  []domain.Item
	Destination *Destination
	// PinData replaces node output. In manual mode the workflow's own pin data
	// applies too; entries here win.
	PinData map[string][]domain.Item
	// Vars backs $vars.
	Vars map[string]any
	// Depth is the sub-workflow nesting level of this execution (0 = top level).
	Depth int
	// Resume continues a waiting execution. The engine takes ownership of it.
	Resume *State
	// ResumeItems, when non-nil, replace the waiting node's first output port.
	ResumeItems []domain.Item
	// Observer receives the progress events of this run instead of Deps.Observer.
	// The worker sets it so every event can be tied to its execution.
	Observer Observer
	// TraceParent is the W3C trace context the execution continues, if any.
	TraceParent string
}

// New builds an Engine and checks that required dependencies are present.
func New(cfg config.EngineConfig, d Deps) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, errors.Join(errors.New("invalid engine config"), err)
	}
	if d.Registry == nil || d.Evaluator == nil || d.Clock == nil || d.Sleeper == nil || d.Credentials == nil {
		return nil, errors.New("engine requires a registry, evaluator, clock, sleeper and credential resolver")
	}
	if d.Observer == nil {
		d.Observer = NopObserver{}
	}
	if d.Metrics == nil {
		d.Metrics = port.NopMetrics{}
	}
	if d.Tracer == nil {
		d.Tracer = port.NopTracer{}
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.Random == nil {
		d.Random = rand.Reader
	}
	return &Engine{cfg: cfg, d: d}, nil
}

// Validate checks a workflow against the registered node types: known types and
// existing ports, on top of the structural checks of domain.Workflow.Validate.
func (e *Engine) Validate(wf domain.Workflow) error {
	_, err := buildGraph(&wf, e.d.Registry)
	return err
}

// Run executes (or resumes) a workflow and returns the final State.
//
// Problems with the request itself (invalid graph, unknown start node) return
// an error and a nil State. Once execution has begun, every outcome, including
// node failure, timeout and cancellation, is reported through State.Status and
// State.Error and Run returns a nil error.
func (e *Engine) Run(ctx context.Context, req Request) (*State, error) {
	r, err := e.newRunner(req)
	if err != nil {
		return nil, err
	}
	return r.run(ctx), nil
}
