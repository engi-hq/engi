// Package api is the REST adapter (docs/09): routing, authentication,
// middleware, problem+json errors and the DTOs of the wire format. Handlers are
// thin: decode, call a service, encode. Business rules live in the services.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/auth"
	"github.com/engi-hq/engi/internal/service/credential"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/tag"
	"github.com/engi-hq/engi/internal/service/variable"
	"github.com/engi-hq/engi/internal/service/workflow"
	"github.com/engi-hq/engi/pkg/nodesdk"
)

// Fixed parts of the public URL space. The version prefix is part of the API
// contract (docs/09 section 9.7), not a setting.
const (
	// BasePath prefixes every versioned route.
	BasePath = "/api/v1"
	// OAuth2CallbackPath is where OAuth2 providers send the browser back.
	OAuth2CallbackPath = "/oauth2/callback"
	// HealthPath and ReadyPath are the probe routes.
	HealthPath = "/healthz"
	ReadyPath  = "/readyz"

	// requestIDPrefix prefixes generated request IDs.
	requestIDPrefix = "rq"
)

// ErrNotImplemented is returned by an Activator that cannot do its job; it maps
// to 501 Not Implemented.
var ErrNotImplemented = errors.New("not implemented")

// Activator registers and removes the triggers of a workflow and flips its
// active flag. The trigger service implements it; the API only depends on this
// narrow view.
type Activator interface {
	Activate(ctx context.Context, id domain.WorkflowID) error
	Deactivate(ctx context.Context, id domain.WorkflowID) error
}

// TestWebhook describes an armed test webhook.
type TestWebhook struct {
	Method string
	// URLPath is the path to call, including the test base path.
	URLPath   string
	ExpiresAt time.Time
}

// TestArmer arms the test webhook of a workflow's webhook trigger node (node
// may be empty when there is exactly one).
type TestArmer interface {
	ArmTest(ctx context.Context, id domain.WorkflowID, node string) (TestWebhook, error)
}

// UnavailableActivator answers every activation request with ErrNotImplemented.
// It is used by processes that run without the trigger role.
type UnavailableActivator struct{}

// Activate implements Activator.
func (UnavailableActivator) Activate(context.Context, domain.WorkflowID) error {
	return fmt.Errorf("workflow activation: %w", ErrNotImplemented)
}

// Deactivate implements Activator.
func (UnavailableActivator) Deactivate(context.Context, domain.WorkflowID) error {
	return fmt.Errorf("workflow deactivation: %w", ErrNotImplemented)
}

// ArmTest implements TestArmer.
func (UnavailableActivator) ArmTest(context.Context, domain.WorkflowID, string) (TestWebhook, error) {
	return TestWebhook{}, fmt.Errorf("test webhooks: %w", ErrNotImplemented)
}

// NodeCatalog describes the registered node types.
type NodeCatalog interface {
	Descriptors() []nodesdk.Descriptor
	Descriptor(nodeType string, version int) (nodesdk.Descriptor, error)
}

// PluginLister reports the installed plugins and whether they loaded.
type PluginLister interface {
	Plugins() []domain.PluginStatus
}

// CredentialTypeCatalog describes the registered credential types.
type CredentialTypeCatalog interface {
	List() []nodesdk.CredentialType
	Get(name string) (nodesdk.CredentialType, bool)
}

// StartNodeFinder picks the node a manual run starts at.
type StartNodeFinder interface {
	FindStartNode(wf domain.Workflow) (string, error)
}

// ExpressionPreviewer evaluates an expression against recorded run data.
type ExpressionPreviewer interface {
	PreviewExpression(ctx context.Context, p engine.Preview) (any, error)
}

// IDGenerator creates prefixed identifiers (used for request IDs).
type IDGenerator interface {
	New(prefix string) (string, error)
}

// EditorSettings are the server settings the browser editor reads at start.
type EditorSettings struct {
	// ChatBasePath is the URL prefix of chat endpoints and widgets (chat.basePath).
	ChatBasePath string
	// MaxNoteTextBytes caps the text of one sticky note (workflow.maxNoteTextBytes).
	MaxNoteTextBytes int
}

// Deps are the collaborators of API. Everything but Logger is required; Ready
// may be nil (then /readyz only reflects draining).
type Deps struct {
	Server  config.ServerConfig
	Session config.SessionConfig

	Auth        *auth.Service
	Workflows   *workflow.Service
	Executions  *execution.Service
	Credentials *credential.Service
	Tags        *tag.Service
	Variables   *variable.Service

	Activator Activator
	// TestArmer may be nil; test-webhook then answers 501.
	TestArmer       TestArmer
	Nodes           NodeCatalog
	CredentialTypes CredentialTypeCatalog
	StartNodes      StartNodeFinder
	// Expressions backs the expression preview; nil answers 501.
	Expressions ExpressionPreviewer
	// Editor is the server setting the editor needs; it is served with GET /me.
	Editor EditorSettings
	// Plugins lists installed plugins; nil lists none.
	Plugins PluginLister
	// Binary serves execution binary data; nil answers 501.
	Binary BinaryReader
	// Bus feeds the live execution stream.
	Bus port.Bus
	// Ready checks the dependencies for /readyz.
	Ready func(ctx context.Context) error
	IDs   IDGenerator
	// Metrics is optional; nil means no-op.
	Metrics port.Metrics

	Logger *slog.Logger
}

// API serves the REST API. Build it with New and mount Handler.
type API struct {
	d        Deps
	handler  http.Handler
	routes   []route
	drain    chan struct{}
	drainOne sync.Once
}

// New builds the API and its router.
func New(d Deps) (*API, error) {
	switch {
	case d.Auth == nil, d.Workflows == nil, d.Executions == nil, d.Credentials == nil:
		return nil, errors.New("api requires the auth, workflow, execution and credential services")
	case d.Activator == nil, d.Nodes == nil, d.CredentialTypes == nil, d.StartNodes == nil, d.Bus == nil, d.IDs == nil:
		return nil, errors.New("api requires an activator, node and credential type catalogs, start node finder, bus and id generator")
	}
	if err := d.Server.Validate(); err != nil {
		return nil, fmt.Errorf("invalid server config: %w", err)
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Metrics == nil {
		d.Metrics = port.NopMetrics{}
	}
	if d.TestArmer == nil {
		d.TestArmer = UnavailableActivator{}
	}
	a := &API{d: d, drain: make(chan struct{})}
	a.routes = a.routeTable()
	a.handler = a.build(a.routes)
	return a, nil
}

// Handler returns the full HTTP handler: routes and middleware.
func (a *API) Handler() http.Handler { return a.handler }

// Routes lists the registered routes as "METHOD /path" patterns, the form the
// OpenAPI spec is checked against.
func (a *API) Routes() []string {
	out := make([]string, len(a.routes))
	for i, r := range a.routes {
		out[i] = r.pattern()
	}
	return out
}

// OpsHandler serves only the probe routes (/healthz, /readyz) with the same
// middleware. Processes that run without the api role use it so Kubernetes
// probes still work.
func (a *API) OpsHandler() http.Handler {
	var ops []route
	for _, r := range a.routes {
		if r.path == HealthPath || r.path == ReadyPath {
			ops = append(ops, r)
		}
	}
	return a.build(ops)
}

// Drain makes /readyz fail and ends open event streams, so a load balancer
// stops sending traffic and shutdown is not held up by long-lived requests
// (docs/11 section 11.3). It is safe to call more than once.
func (a *API) Drain() { a.drainOne.Do(func() { close(a.drain) }) }

func (a *API) draining() bool {
	select {
	case <-a.drain:
		return true
	default:
		return false
	}
}
