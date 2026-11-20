package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/engi-hq/engi/internal/adapter/crypto/argon2id"
	"github.com/engi-hq/engi/internal/adapter/http/api"
	"github.com/engi-hq/engi/internal/config"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/service/auth"
	tagsvc "github.com/engi-hq/engi/internal/service/tag"
	"github.com/engi-hq/engi/internal/service/trigger"
	workflowsvc "github.com/engi-hq/engi/internal/service/workflow"
)

// ServerOptions are the extension points of the server. The zero value is a
// complete, working server.
type ServerOptions struct {
	// Mounts maps ServeMux patterns (for example "/hook/", "/hook-test/") to
	// handlers served next to the API. They are mounted only when the webhook
	// role is enabled (a mount replaces the built-in webhook handler on the same
	// pattern), and take precedence over the API's catch-all, so a mount
	// can never be shadowed by an API route.
	Mounts map[string]http.Handler
	// Activator backs POST /workflows/{id}/activate and /deactivate. Nil means
	// the trigger service when the trigger role is enabled, otherwise
	// api.UnavailableActivator, which answers 501 Not Implemented.
	Activator api.Activator
	// UI, when set, serves the editor at every path the API does not own. It
	// is mounted only when the api role is enabled.
	UI http.Handler
}

// Server is `engi serve`: the HTTP server (REST API, mounted handlers, probes)
// plus the runtime (worker and wait scheduler) according to the enabled roles.
// Build it with App.NewServer, then call Start and Shutdown, or Run.
type Server struct {
	// Runtime is the persistent runtime the server was built on.
	Runtime *Runtime
	// API is the REST adapter; use it to Drain or list Routes.
	API *api.API
	// Auth and Workflows are the services the API uses, exposed so other
	// components (the trigger service, tests) can share them.
	Auth      *auth.Service
	Workflows *workflowsvc.Service

	app  *App
	http *http.Server
	lis  net.Listener

	metrics   *metricsServer
	mu        sync.Mutex
	started   bool
	stopBG    context.CancelFunc
	bgDone    sync.WaitGroup
	serveDone chan struct{}
	serveErr  error
}

// NewServer wires the REST API and the runtime. It opens the database (and
// migrates it when server.autoMigrate is on) but does not listen or start
// workers; call Start for that. The credential keyring must be configured.
func (a *App) NewServer(ctx context.Context, opts ServerOptions) (*Server, error) {
	cfg := a.Config
	if err := cfg.Credentials.RequireKeyring(); err != nil {
		return nil, fmt.Errorf("starting the server: %w", err)
	}
	if a.Credentials == nil {
		return nil, errors.New("starting the server: credential service is not available")
	}
	rt, err := a.NewRuntime(ctx)
	if err != nil {
		return nil, err
	}
	srv, err := a.assembleServer(rt, opts) //nolint:contextcheck // request handlers get their own request contexts
	if err != nil {
		_ = rt.Shutdown(context.WithoutCancel(ctx))
		return nil, err
	}
	return srv, nil
}

func (a *App) assembleServer(rt *Runtime, opts ServerOptions) (*Server, error) {
	cfg := a.Config
	store := rt.stores

	authSvc, err := auth.New(cfg.Auth, auth.Deps{
		Users: store.Users, Sessions: store.Sessions, APIKeys: store.APIKeys,
		Hasher: argon2id.New(cfg.Auth.Argon2, cryptoRand()), Tx: store.Tx, Clock: a.Clock, IDs: a.ids,
		Logger: a.Logger.With("component", "auth"),
	})
	if err != nil {
		return nil, fmt.Errorf("building auth service: %w", err)
	}
	wfSvc, err := workflowsvc.New(workflowsvc.Deps{
		Workflows: rt.Workflows, Versions: rt.Versions, Validator: a.Engine, IDs: a.ids,
		NoteLimits: domain.NoteLimits{MaxNotes: cfg.Workflow.MaxNotes, MaxTextBytes: cfg.Workflow.MaxNoteTextBytes},
		Logger:     a.Logger.With("component", "workflow"),
	})
	if err != nil {
		return nil, fmt.Errorf("building workflow service: %w", err)
	}
	tagSvc, err := tagsvc.New(tagsvc.Deps{Store: store.Tags, IDs: a.ids})
	if err != nil {
		return nil, fmt.Errorf("building tag service: %w", err)
	}
	var armer api.TestArmer
	if opts.Activator == nil {
		// Activation only writes shared state (webhook routes, the active flag)
		// and publishes an event that trigger pods follow, so any API process
		// may activate. Schedules, pollers and listeners start only where the
		// trigger role runs (Start and leadership), never here.
		opts.Activator = triggerAdapter{rt.Triggers}
		// Armed test webhooks live in process memory: only a process that also
		// serves webhooks can arm them.
		if cfg.Server.HasRole(config.RoleWebhook) {
			armer = triggerAdapter{rt.Triggers}
		}
	}
	rest, err := api.New(api.Deps{
		Server: cfg.Server, Session: cfg.Auth.Session,
		Auth: authSvc, Workflows: wfSvc, Executions: rt.Executions, Credentials: a.Credentials,
		Tags: tagSvc, Variables: rt.Variables,
		Editor:    api.EditorSettings{ChatBasePath: cfg.Trigger.Chat.BasePath, MaxNoteTextBytes: cfg.Workflow.MaxNoteTextBytes},
		Activator: opts.Activator, TestArmer: armer, Nodes: a.Registry, CredentialTypes: a.CredentialTypes, StartNodes: a, Expressions: a.Engine,
		Plugins: a, Binary: a.Blobs, Bus: rt.Bus, Ready: rt.db.PingContext, IDs: a.ids, Metrics: a.obs.metrics, Logger: a.Logger.With("component", "api"),
	})
	if err != nil {
		return nil, fmt.Errorf("building api: %w", err)
	}

	mux := http.NewServeMux()
	if cfg.Server.HasRole(config.RoleAPI) {
		root := rest.Handler()
		if opts.UI != nil {
			root = withUI(root, opts.UI)
		}
		mux.Handle("/", root)
	} else {
		mux.Handle("/", rest.OpsHandler())
	}
	if cfg.Server.HasRole(config.RoleWebhook) {
		// Webhook routes sit beside, not inside, the API: no session, API key
		// or CSRF check applies to them.
		hooks := countWebhooks(rt.Webhooks, a.obs.metrics)
		for _, pattern := range rt.Webhooks.Patterns() {
			if _, overridden := opts.Mounts[pattern]; !overridden {
				mux.Handle(pattern, hooks)
			}
		}
		// MCP server endpoints are public routes of the same kind: each
		// endpoint authenticates callers with its trigger node's credential.
		for _, pattern := range rt.MCP.Patterns() {
			if _, overridden := opts.Mounts[pattern]; !overridden {
				mux.Handle(pattern, rt.MCP)
			}
		}
		for pattern, h := range opts.Mounts {
			mux.Handle(pattern, h)
		}
	}
	a.mountMetrics(mux)
	s := cfg.Server
	return &Server{
		Runtime: rt, API: rest, Auth: authSvc, Workflows: wfSvc, app: a,
		http: &http.Server{
			Handler: a.instrument(mux), ReadHeaderTimeout: s.ReadHeaderTimeout, ReadTimeout: s.ReadTimeout,
			WriteTimeout: s.WriteTimeout, IdleTimeout: s.IdleTimeout,
			ErrorLog: slog.NewLogLogger(a.Logger.With("component", "http").Handler(), slog.LevelError),
		},
	}, nil
}

// Addr returns the address the server listens on; empty before Start.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lis == nil {
		return ""
	}
	return s.lis.Addr().String()
}

// Start listens, starts the worker and wait scheduler when the worker role is
// enabled, and serves in the background. Canceling ctx stops claiming jobs but
// does not stop the HTTP server; call Shutdown for an orderly stop.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("server already started")
	}
	cfg := s.app.Config
	lis, err := net.Listen("tcp", cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.Server.Addr, err)
	}
	ms, err := s.app.startMetricsServer()
	if err != nil {
		_ = lis.Close()
		return err
	}
	s.metrics = ms
	if cfg.Server.HasRole(config.RoleWorker) {
		if err := s.Runtime.Start(ctx); err != nil {
			_ = lis.Close()
			return err
		}
	}
	if cfg.Server.HasRole(config.RoleTrigger) {
		if err := s.Runtime.StartTriggers(ctx); err != nil {
			_ = lis.Close()
			return err
		}
	}
	bgCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	s.stopBG = stop
	if cfg.Server.HasRole(config.RoleAPI) {
		s.bgDone.Go(func() { s.Auth.Run(bgCtx) })
	}
	s.lis, s.started = lis, true
	s.serveDone = make(chan struct{})
	go func() {
		defer close(s.serveDone)
		if err := s.http.Serve(lis); !errors.Is(err, http.ErrServerClosed) {
			s.serveErr = err
		}
	}()
	s.app.Logger.Info("engi listening", "addr", lis.Addr().String(), "roles", cfg.Server.Roles)
	return nil
}

// Shutdown stops the server in order: readiness fails and event streams end, the
// HTTP server drains within server.shutdownGrace, background jobs stop, then the
// runtime lets running executions finish within worker.shutdownGrace and the
// database is closed.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	cfg := s.app.Config
	var errs []error
	if started {
		s.API.Drain()
		hctx, cancel := context.WithTimeout(ctx, cfg.Server.ShutdownGrace)
		if err := s.http.Shutdown(hctx); err != nil {
			errs = append(errs, fmt.Errorf("stopping http server: %w", err), s.http.Close())
		}
		cancel()
		<-s.serveDone
		if s.serveErr != nil {
			errs = append(errs, fmt.Errorf("http server: %w", s.serveErr))
		}
		s.stopBG()
		s.bgDone.Wait()
	}
	rctx, cancel := context.WithTimeout(ctx, cfg.Worker.ShutdownGrace+time.Second)
	defer cancel()
	errs = append(errs, s.metrics.shutdown(rctx), s.Runtime.Shutdown(rctx), s.app.ClosePlugins(rctx), s.app.CloseCredentials(), s.app.CloseDatabasePools(), s.app.CloseTelemetry(rctx))
	return errors.Join(errs...)
}

// Run starts the server, waits until ctx is canceled (SIGINT/SIGTERM in the
// command), and shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	if err := s.Start(ctx); err != nil {
		return errors.Join(err, s.Shutdown(context.WithoutCancel(ctx)))
	}
	select {
	case <-ctx.Done():
		s.app.Logger.Info("shutting down")
	case <-s.serveDone:
		return errors.Join(errors.New("http server stopped unexpectedly"), s.Shutdown(context.WithoutCancel(ctx)))
	}
	return s.Shutdown(context.WithoutCancel(ctx))
}

// triggerAdapter presents the trigger service as the API's Activator and
// TestArmer.
type triggerAdapter struct{ svc *trigger.Service }

func (t triggerAdapter) Activate(ctx context.Context, id domain.WorkflowID) error {
	return t.svc.Activate(ctx, id) //nolint:wrapcheck // the API wraps with context
}

func (t triggerAdapter) Deactivate(ctx context.Context, id domain.WorkflowID) error {
	return t.svc.Deactivate(ctx, id) //nolint:wrapcheck // the API wraps with context
}

func (t triggerAdapter) ArmTest(ctx context.Context, id domain.WorkflowID, node string) (api.TestWebhook, error) {
	h, err := t.svc.ArmTest(ctx, id, node)
	if err != nil {
		return api.TestWebhook{}, err //nolint:wrapcheck // the API wraps with context
	}
	return api.TestWebhook{Method: h.Method, URLPath: h.URLPath, ExpiresAt: h.ExpiresAt}, nil
}
