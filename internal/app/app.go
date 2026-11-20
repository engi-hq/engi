// Package app is the composition root: it builds the concrete adapters, the
// node registry, the expression evaluator and the engine, and wires them
// together. It is the only place that knows every concrete type.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"slices"

	"github.com/engi-hq/engi/internal/adapter/clock/system"
	"github.com/engi-hq/engi/internal/adapter/dbpool"
	"github.com/engi-hq/engi/internal/adapter/embedding"
	"github.com/engi-hq/engi/internal/adapter/httpclient"
	"github.com/engi-hq/engi/internal/adapter/kafkapool"
	"github.com/engi-hq/engi/internal/adapter/mongopool"
	"github.com/engi-hq/engi/internal/adapter/redispool"
	"github.com/engi-hq/engi/internal/adapter/s3pool"
	"github.com/engi-hq/engi/internal/config"
	credtypes "github.com/engi-hq/engi/internal/credtype/registry"
	"github.com/engi-hq/engi/internal/domain"
	"github.com/engi-hq/engi/internal/id"
	"github.com/engi-hq/engi/internal/node/registry"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/blob"
	"github.com/engi-hq/engi/internal/service/credential"
	"github.com/engi-hq/engi/internal/service/engine"
	"github.com/engi-hq/engi/internal/service/expression"
)

// Options carry the process-level inputs the composition root must not read
// from globals itself. Zero values select the real implementations.
type Options struct {
	// Logger receives engine and node logs. Defaults to a logger built from the
	// configuration writing to LogOutput.
	Logger *slog.Logger
	// LogOutput is used when Logger is nil. Defaults to os.Stderr.
	LogOutput io.Writer
	// Version is the Engi version, sent to plugins in initialize.
	Version string
	// LookupEnv backs $env in expressions and the variables plugins inherit
	// (plugins.inheritEnv). Defaults to os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// ReadFile loads the optional TLS CA bundle. Defaults to os.ReadFile.
	ReadFile func(string) ([]byte, error)
	// Credentials overrides the credential resolver (tests, alternative
	// backends). Normally nil: New builds the credential service when a
	// keyring is configured and uses a resolver that knows no credentials
	// otherwise.
	Credentials port.CredentialResolver
	// CredentialStore and Tx let the caller share its store with the credential
	// service. When nil and a keyring is configured, New opens the configured
	// database itself; the runtime then reuses that same handle.
	CredentialStore port.CredentialStore
	Tx              port.TxManager
	// Metrics and Tracer override the observability section (tests). Nil
	// builds them from configuration, or no-ops when disabled.
	Metrics port.Metrics
	Tracer  port.Tracer
}

// App is the wired application.
type App struct {
	Config   config.Config
	Registry *registry.Registry
	Engine   *engine.Engine
	Clock    port.Clock
	Sleeper  port.Sleeper
	Logger   *slog.Logger
	// Credentials is the credential service; nil when no keyring is configured.
	Credentials *credential.Service
	// CredentialTypes lists the registered credential types.
	CredentialTypes *credtypes.Registry
	db              *database // shared by the credential service and the runtime; see storeDatabase
	credResolver    port.CredentialResolver
	ids             *id.Generator
	subworkflows    *subworkflowBridge
	chatMemory      *chatMemoryBridge
	dbPools         *dbpool.Manager
	redisClients    *redispool.Manager
	kafkaClients    *kafkapool.Manager
	s3Clients       *s3pool.Manager
	// Blobs stores binary item data.
	Blobs        *blob.Service
	mongoClients *mongopool.Manager
	obs          *observability
	readFile     func(string) ([]byte, error)
	plugins      *pluginHost
}

// New builds the application from a validated configuration.
func New(ctx context.Context, cfg config.Config, opts Options) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	if opts.ReadFile == nil {
		opts.ReadFile = os.ReadFile
	}
	if opts.Logger == nil {
		out := opts.LogOutput
		if out == nil {
			out = os.Stderr
		}
		logger, err := NewLogger(cfg.Log, out)
		if err != nil {
			return nil, err
		}
		opts.Logger = logger
	}

	clock := system.Clock{}
	bridge := &subworkflowBridge{}
	memoryBridge := &chatMemoryBridge{}
	dbCfg := cfg.Nodes.Database
	dbPools := dbpool.New(dbpool.Config{
		MaxOpenConns: dbCfg.MaxOpenConns, MaxIdleConns: dbCfg.MaxIdleConns, IdleTimeout: dbCfg.IdleTimeout,
		MaxLifetime: dbCfg.MaxLifetime, ConnectTimeout: dbCfg.ConnectTimeout,
	}, opts.Logger)
	redisClients := redispool.New(redispool.Config{
		PoolSize: dbCfg.MaxOpenConns, IdleTimeout: dbCfg.IdleTimeout, MaxLifetime: dbCfg.MaxLifetime, ConnectTimeout: dbCfg.ConnectTimeout,
	}, opts.Logger)
	mongoClients := mongopool.New(mongopool.Config{
		MaxPoolSize: dbCfg.MaxOpenConns, IdleTimeout: dbCfg.IdleTimeout, ConnectTimeout: dbCfg.ConnectTimeout,
	}, opts.Logger)
	kafkaClients := kafkapool.New(kafkapool.Config{
		ConnectTimeout: cfg.Nodes.Kafka.ConnectTimeout, FlushTimeout: cfg.Nodes.Kafka.FlushTimeout,
	}, opts.Logger)
	s3Clients := s3pool.New(s3pool.Config{ConnectTimeout: cfg.Nodes.S3.ConnectTimeout}, opts.Logger)
	evaluator, err := expression.New(cfg.Expression, clock, opts.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("building expression evaluator: %w", err)
	}
	obs, err := buildObservability(ctx, cfg.Observability, opts)
	if err != nil {
		return nil, err
	}
	reg, err := NewRegistry(cfg.Nodes, clock, WithDBPools(dbPools), WithRedisClients(redisClients), WithMongoClients(mongoClients), WithKafkaClients(kafkaClients), WithS3Clients(s3Clients), WithKafkaTrigger(cfg.Trigger.Kafka), WithSubworkflows(bridge, cfg.Engine.MaxSubworkflowDepth),
		WithScheduleTimezone(cfg.Trigger.Schedule.DefaultTimezone),
		WithChatMemory(memoryBridge),
		WithLLMFactory(newProviderFactory(cfg.Nodes.AI, obs.metrics, clock.Now)),
		WithEmbedderFactory(embedding.New(cfg.Nodes.AI, obs.metrics, clock.Now)))
	if err != nil {
		return nil, err
	}
	httpc, err := httpclient.New(cfg.HTTPClient, opts.ReadFile, httpclient.WithTracer(obs.tracer))
	if err != nil {
		return nil, fmt.Errorf("building http client: %w", err)
	}
	creds, err := buildCredentials(ctx, cfg, opts, clock, httpc, opts.Logger)
	if err != nil {
		return nil, err
	}
	blobs, err := buildBlobs(ctx, cfg.Blob)
	if err != nil {
		return nil, err
	}
	resumeBase, err := url.JoinPath(cfg.Server.PublicURL, cfg.Trigger.Webhook.WaitBasePath)
	if err != nil {
		return nil, fmt.Errorf("building wait resume URL: %w", err)
	}
	eng, err := engine.New(cfg.Engine, engine.Deps{
		Registry: reg, Evaluator: evaluator, Clock: clock, Sleeper: clock,
		Credentials: creds.resolver, HTTP: httpc, Blob: blobs, Metrics: obs.metrics, Tracer: obs.tracer, Logger: opts.Logger,
		Random: cryptoRand(), ResumeBase: resumeBase,
	})
	if err != nil {
		if creds.db != nil {
			_ = creds.db.Close()
		}
		return nil, fmt.Errorf("building engine: %w", err)
	}
	plugins := loadPlugins(ctx, pluginEnv{cfg: cfg.Plugins, version: opts.Version, lookupEnv: opts.LookupEnv, readFile: opts.ReadFile, logger: opts.Logger}, reg)
	return &App{
		plugins: plugins,
		Config:  cfg, Blobs: blobs, Registry: reg, Engine: eng, Clock: clock, Sleeper: clock, Logger: opts.Logger,
		Credentials: creds.service, CredentialTypes: creds.types, db: creds.db, credResolver: creds.resolver,
		dbPools: dbPools, redisClients: redisClients, kafkaClients: kafkaClients, s3Clients: s3Clients, mongoClients: mongoClients, ids: id.NewGenerator(clock.Now, cryptoRand()), subworkflows: bridge, chatMemory: memoryBridge, obs: obs, readFile: opts.ReadFile,
	}, nil
}

// NewLogger builds a slog logger from the log configuration.
func NewLogger(cfg config.LogConfig, w io.Writer) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		return nil, fmt.Errorf("log level %q: %w", cfg.Level, err)
	}
	ho := &slog.HandlerOptions{Level: level}
	switch cfg.Format {
	case config.LogFormatJSON:
		return slog.New(slog.NewJSONHandler(w, ho)), nil
	case config.LogFormatText:
		return slog.New(slog.NewTextHandler(w, ho)), nil
	}
	return nil, errors.New("unknown log format " + cfg.Format)
}

// Plugins reports every installed plugin and whether it loaded.
func (a *App) Plugins() []domain.PluginStatus {
	return slices.Clone(a.plugins.statuses)
}

// ClosePlugins stops the plugin processes. Call it once no execution runs.
func (a *App) ClosePlugins(ctx context.Context) error {
	return a.plugins.close(ctx)
}

// CloseDatabasePools closes every connection pool and client the database
// nodes (SQL, Redis, Kafka, MongoDB) opened.
func (a *App) CloseDatabasePools() error {
	return errors.Join(a.dbPools.Close(), a.redisClients.Close(), a.kafkaClients.Close(), a.s3Clients.Close(), a.mongoClients.Close())
}

// CloseCredentials releases the database handle the credential stack and the
// runtime share. It is safe to call more than once.
func (a *App) CloseCredentials() error {
	return a.closeDatabase()
}
