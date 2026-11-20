package app

import (
	"context"
	"errors"
	"fmt"

	mcphttp "github.com/engi-hq/engi/internal/adapter/http/mcpserver"
	"github.com/engi-hq/engi/internal/adapter/http/webhook"
	"github.com/engi-hq/engi/internal/adapter/jsoncodec"
	"github.com/engi-hq/engi/internal/node/errortrigger"
	"github.com/engi-hq/engi/internal/node/executeworkflowtrigger"
	"github.com/engi-hq/engi/internal/port"
	"github.com/engi-hq/engi/internal/service/errorflow"
	"github.com/engi-hq/engi/internal/service/execution"
	"github.com/engi-hq/engi/internal/service/retention"
	"github.com/engi-hq/engi/internal/service/trigger"
	"github.com/engi-hq/engi/internal/service/variable"
	"github.com/engi-hq/engi/internal/service/waiter"
	"github.com/engi-hq/engi/internal/service/worker"
)

// workerIDPrefix prefixes the generated id a worker uses as lease owner.
const workerIDPrefix = "wk"

// Runtime is the standalone runtime: SQLite store and job queue, in-process
// bus, the worker, the wait scheduler and the execution service. One process,
// no other dependencies.
type Runtime struct {
	Executions *execution.Service
	Worker     *worker.Worker
	Waiter     *waiter.Service
	// Triggers activates workflows (it is the API's Activator); StartTriggers
	// brings it up.
	Triggers *trigger.Service
	// Webhooks serves /hook, /hook-test and /hook-wait; mount it on
	// Webhooks.Patterns().
	Webhooks *webhook.Handler
	// MCP serves the MCP server trigger endpoints (/mcp/...); mount it on
	// MCP.Patterns().
	MCP       *mcphttp.Handler
	Bus       port.Bus
	Workflows port.WorkflowStore
	Versions  port.WorkflowVersionStore
	// Variables manages variables and supplies $vars to executions.
	Variables *variable.Service
	// Retention prunes old executions on the leader; StartTriggers starts it.
	Retention *retention.Service

	db      *database
	queue   jobQueue
	stores  storeSet
	stopNet func() // stops queue/bus listener connections
	states  port.ExecutionStateStore
	// closeDB releases the database handle, which the App shares with the
	// credential service.
	closeDB func() error
}

// NewRuntime wires the persistent runtime around the app's engine over the
// database named by the store configuration (SQLite, or Postgres in cluster
// mode). The database is opened once per App and shared with the credential
// service, so the runtime does not open a second pool. Call Start to begin
// working and Shutdown to stop and release the database.
func (a *App) NewRuntime(ctx context.Context) (*Runtime, error) {
	db, err := a.storeDatabase(ctx)
	if err != nil {
		return nil, err
	}
	rt, err := a.buildRuntime(ctx, db)
	if err != nil {
		_ = a.closeDatabase()
		return nil, err
	}
	return rt, nil
}

func (a *App) buildRuntime(ctx context.Context, db *database) (*Runtime, error) {
	cfg := a.Config
	if err := prepareSchema(ctx, db, cfg.Server.AutoMigrate); err != nil {
		return nil, err
	}
	store := db.stores(cfg.Store, a.Clock)
	cl, err := a.newCluster(ctx, db)
	if err != nil {
		return nil, err
	}
	queue, bus := cl.queue, cl.bus
	built := false
	defer func() {
		if !built {
			cl.stop()
		}
	}()
	workerID, err := a.ids.New(workerIDPrefix)
	if err != nil {
		return nil, fmt.Errorf("generating worker id: %w", err)
	}
	codec := jsoncodec.Codec{}
	wfs, execs := store.Workflows, store.Executions

	svc, err := execution.New(cfg.Worker, execution.Deps{
		Workflows: wfs, Versions: store.Versions, Executions: execs, States: store.States, Queue: queue, Tx: store.Tx, Bus: bus,
		Clock: a.Clock, IDs: a.ids, Codec: codec, Blobs: a.Blobs, Tracer: a.obs.tracer, Logger: a.Logger.With("component", "execution"),
	})
	if err != nil {
		return nil, fmt.Errorf("building execution service: %w", err)
	}
	vars, err := a.buildVariables(store)
	if err != nil {
		return nil, err
	}
	failures, err := errorflow.New(errorflow.Deps{
		Workflows: wfs, Versions: store.Versions, Executions: execs, Starter: svc, TriggerType: errortrigger.Type,
		Logger: a.Logger.With("component", "errorflow"),
	})
	if err != nil {
		return nil, fmt.Errorf("building error workflow notifier: %w", err)
	}
	w, err := worker.New(cfg.Worker, cfg.Queue, worker.Deps{
		WorkerID: workerID, Dispatcher: queue, Queue: queue, Reclaimer: queue, Executions: execs, States: store.States,
		Versions: store.Versions, Tx: store.Tx, Bus: bus, Clock: a.Clock, Engine: a.Engine, Codec: codec, Vars: vars, Metrics: a.obs.metrics, Failures: failures,
		Logger: a.Logger.With("component", "worker"),
	})
	if err != nil {
		return nil, fmt.Errorf("building worker: %w", err)
	}
	wt, err := waiter.New(cfg.Worker, waiter.Deps{
		Executions: execs, States: store.States, Queue: queue, Tx: store.Tx, Clock: a.Clock, IDs: a.ids, Codec: codec,
		Logger: a.Logger.With("component", "waiter"),
	})
	if err != nil {
		return nil, fmt.Errorf("building waiter: %w", err)
	}
	subs, err := execution.NewSubworkflows(svc, w, executeworkflowtrigger.Type)
	if err != nil {
		return nil, fmt.Errorf("building sub-workflow runner: %w", err)
	}
	a.subworkflows.set(subs)
	a.chatMemory.set(db.chatMemory(cfg.Store, cfg.Nodes.AI.Memory, a.Clock))
	a.obs.metrics.WatchQueueDepth(queue)
	triggers, hooks, err := a.buildTriggers(store, cl.lease, bus, svc, wt, queue)
	if err != nil {
		return nil, err
	}
	mcpHandler, err := a.buildMCPServer(triggers, wfs, svc)
	if err != nil {
		return nil, err
	}
	built = true
	ret := retention.New(cfg.Retention, retention.Deps{
		Executions: execs, Blobs: a.Blobs, Leader: triggers, Clock: a.Clock, Sleeper: a.Sleeper, Logger: a.Logger.With("component", "retention"),
	})
	return &Runtime{Retention: ret, Executions: svc, Worker: w, Waiter: wt, Triggers: triggers, Webhooks: hooks, MCP: mcpHandler, Bus: bus, Workflows: wfs, Versions: store.Versions, Variables: vars, db: db, queue: queue, stores: store, stopNet: cl.stop, states: store.States, closeDB: a.closeDatabase}, nil
}

// Start begins claiming jobs and resuming waiting executions. Canceling ctx
// stops claiming; Shutdown waits for what is running.
func (r *Runtime) Start(ctx context.Context) error {
	if err := r.Worker.Start(ctx); err != nil {
		return fmt.Errorf("starting worker: %w", err)
	}
	if err := r.Waiter.Start(ctx); err != nil {
		return errors.Join(fmt.Errorf("starting waiter: %w", err), r.Worker.Shutdown(ctx))
	}
	return nil
}

// Shutdown stops the waiter, lets running executions finish within the
// configured grace period (releasing their jobs afterwards), and closes the
// database. ctx bounds the whole shutdown.
func (r *Runtime) Shutdown(ctx context.Context) error {
	errTriggers := errors.Join(r.Retention.Shutdown(ctx), r.Triggers.Shutdown(ctx))
	errWaiter := r.Waiter.Shutdown(ctx)
	errWorker := r.Worker.Shutdown(ctx)
	if errors.Is(errWorker, worker.ErrNotStarted) {
		errWorker = nil
	}
	r.stopNet()
	if err := r.closeDB(); err != nil {
		return errors.Join(errTriggers, errWaiter, errWorker, fmt.Errorf("closing store: %w", err))
	}
	return errors.Join(errTriggers, errWaiter, errWorker)
}
