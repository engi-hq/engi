# 4. Execution Engine

Package: `internal/service/engine`. The engine is **pure orchestration**. All I/O (storage,
blobs, credentials, HTTP, clock) happens through ports, so it is deterministic and
golden-testable.

## 4.1 Inputs and outputs

```go
type Request struct {
    Workflow   domain.Workflow
    StartNode  string            // trigger node name, or a node chosen for a manual run
    StartItems []domain.Item     // trigger output
    Mode       domain.ExecutionMode
    Destination *Destination     // optional: run only up to this node (partial run)
    PinData    map[string][]domain.Item
    Resume     *State            // non-nil when resuming a waiting execution
    ResumeItems []domain.Item   // optional: replaces the waiting node's first output on resume
    Vars       map[string]any    // $vars
    Depth      int               // sub-workflow nesting level, checked against maxSubworkflowDepth
}
// Run returns the final State. Progress is reported through an injected Observer.
// Request problems (invalid graph, unknown start node) return an error and a nil State.
// Once running, every outcome (node failure, timeout, cancel) is reported in State.Status
// and State.Error and the returned error is nil.
func (e *Engine) Run(ctx context.Context, req Request) (*State, error)
```

## 4.2 Scheduling algorithm

The engine keeps a **ready queue** of `Task{node, inputs, source}` and a **waiting table**
for nodes with several inputs.

1. Seed the ready queue with the start node and its `StartItems`.
2. Loop until the ready queue is empty, the context is cancelled, or the execution timeout is reached:
   1. Pop the next task (order below).
   2. If the node is disabled, pass its first input straight through to output 0.
   3. If it has pin data, use that as the output and skip execution.
   4. Otherwise execute the node (see 4.4) and record a `NodeRun`.
   5. For each output port `i` with **non-empty** items, and each connection from `(node, i)`:
      - if the target has one `main` input, push a Task to the ready queue;
      - otherwise store the items in `waiting[target][inputIndex]`, and push the Task once **all**
        inputs are filled, or when the target's merge mode allows partial input (see 4.3).
3. Once the ready queue is empty, flush the waiting table. A node with partial inputs runs
   only if its descriptor declares `MergePolicy: partialOnFlush` (see 4.3). Other waiting
   nodes are dropped when the execution finishes.

**Ordering (deterministic):** depth-first by branch. When a node fans out, its children are
processed in canvas order (top → bottom by `position.y`, then left → right by `x`), and each
branch runs to completion before the next sibling starts. This matches what users expect
from the canvas and keeps runs reproducible.

The ready queue is a stack: a node's deliveries are sorted (canvas order of the target, then
output port, then input index) and pushed in reverse, so the first child runs next.

**Empty output** stops a branch: children are not executed unless the producing node has
`alwaysOutputData`. That setting looks only at output 0: when output 0 is empty
(even if another output has items), a single empty item `{json:{}}` is put there, paired to
every input item. This also applies to disabled and pinned nodes.

**Cycles** (loops) are allowed. Each pass increments that node's `runIndex`. A configurable
`maxNodeRuns` guard stops infinite loops.

## 4.3 Multiple inputs

Nodes declare their inputs. Merge-type nodes declare a policy:

| Policy | Behaviour |
|---|---|
| `waitAll` (default) | Run when every connected input has data |
| `any` | Run each time any input receives data |
| `partialOnFlush` | Run as soon as all connected inputs have data, otherwise with whatever arrived once the ready queue drains |

"Connected input" means an input index with at least one incoming `main` connection. Items
that reach the same unfired input twice are appended. A single-input node runs once per
arrival. `$("Node").item` is resolved through each item's `pairedItem` chain, and only
tracks one source per input, so lineage through appended inputs is best effort.

## 4.4 Node execution

```
resolve credentials → evaluate expressions per item → call node.Execute(ctx, in)
  → validate outputs → assign pairedItem if missing → record NodeRun
```

- **Per-item vs once:** nodes run once per call and receive all items. `executeOnce` setting
  means only the first item is passed in.
- **Expressions** are evaluated lazily by the node through `in.Param(name, itemIndex)`, so
  each item can resolve to a different value.
- **Timeout:** a per-node `timeoutMs`, or the default from config, is applied as a context deadline.

## 4.5 Errors and retries

| Setting | Behaviour |
|---|---|
| `retry.maxAttempts` | Total attempts including the first (0 or 1 = no retry). Delay is `initialDelay`, doubled per attempt when `backoff` is `exponential`, capped by `engine.maxRetryDelay`. Respects ctx |
| `onError: stop` | Mark the execution `error` and stop. Trigger the workflow's error workflow if one is set |
| `onError: continue` | One error item per input item on output 0, and continue. The item's json is `{"error": "<message>"}`, its `Error` field is set, and it is paired to the input item |
| `onError: errorOutput` | The same items on a dedicated error port (the output index right after the declared outputs), but the json also keeps the failing input item's fields. Items a node returns with `Error` set are moved to this port too, merged with their input item |

An item that carries an `Error` always gets json `{"error": "<message>"}`, so `$json.error` works
the same for every node.

Pin data: `Request.PinData` always applies; the workflow's own `pinData` applies only in
`manual` mode. A pinned node outputs the pinned items on port 0 without running.
Partial run: only ancestors of the destination (and the destination itself) are scheduled;
the destination's children never run. `NodeRun` also records `Attempts` and `Sources`
(which node run fed each input) and its status is one of `success`, `error`, `disabled`,
`pinned`, `waiting`.

Input items are stamped with their own position (`pairedItem` = input port and index) before
a node runs, so a node that returns an input item unchanged, and a disabled node, keep a correct
lineage. Missing `pairedItem` on output is filled in when unambiguous: one input item pairs to
all outputs; with one output port, equal counts pair by index and a single output item of
several inputs pairs to the first. `$("Node").item` follows every `pairedItem` reference of each
hop and fails with a "multiple items match" error if the paths reach different items of that
node (use `.first()`, `.last()` or `.all()`), a "pairedItem information is missing" error if a
node on the path did not say where its items came from, and a "not on the path" error if the
node is not an ancestor of the current item.

`pairedItem` references that point at no input item of this run (for example copied from an earlier
execution) are dropped from node output, and the item is then paired by the rules above. Pinned items
and the items of a resume call (4.6) are new data: whatever pairing they carry is discarded and they are
paired to the node's input. (Nodes that return their input items need no explicit pairing, because the
stamp above already points at the right position.)

`nodesdk.Input.EvalExpression(ctx, expr, itemIndex)` evaluates a JavaScript expression in the same scope
as a parameter of that item (`$json`, `$("Node")`, `$node`, `$vars`, `$env`, `$fn`, ...). Nodes that run
user code, such as the Code node, use it so that code sees the workflow scope without the node SDK
exposing engine internals.

Error types: `NodeError` (node failed: message, description, http status, item index) and
`WorkflowError` (bad graph, missing node, expression failure). Both carry the node name.

## 4.6 Waiting & resume

A node can return `Wait{Until time.Time | Webhook resumeToken}`. The engine then:
1. Keeps `State` (ready queue, waiting table, runData so far) and records `Pending`
   (node, `Until`/`ResumeToken`, and the output that is delivered when the wait ends).
2. Marks the execution `waiting` with `waitUntil` set, and stops.
3. A timer (scheduler) or a resume webhook re-enqueues the execution later, and the engine continues from the saved state.

`State` must therefore be fully serializable: no closures and no live connections.

**Who resumes what.** The worker (`internal/service/worker`) stores a waiting execution as status `waiting`,
`waitUntil` set when the node asked for a time, and the serialized `State` in the execution state store.
The wait scheduler (`internal/service/waiter`) runs on `worker.waiterInterval`: it takes waiting executions
whose `waitUntil` has passed (at most `worker.waiterBatchSize` per tick), marks each `queued` and enqueues a
job, in one transaction, so a second tick cannot queue it twice. A worker that claims the job decodes the
saved `State` and calls the engine with `Request.Resume`. `ResumeByToken(token, items)` does the same for a
node that waits for a signal: the token is `"<executionId>.<secret>"` (`nodesdk.NewResumeToken`), the
service finds the execution from the prefix, compares the whole token with `Pending.ResumeToken` in constant
time, stores `items` in `Pending.ResumeItems` (they replace the node's first output, like
`Request.ResumeItems`) and queues the job. Every unusable token is the same `ErrInvalidToken`; an execution
that is no longer waiting is `ErrNotWaiting`. A crash during a resumed run is safe: the saved state is only
deleted when the execution finishes, so the reclaimed job resumes again. The job of a waiting execution is
never run itself: if its worker died before acking the wait, the reclaimed job is dropped and only the waiter
or a token resume starts the next run (delivery rules in [02-architecture](02-architecture.md)).

## 4.7 Sub-workflows

The `engi.flow.executeWorkflow` node starts a child execution (`parentExecutionId` set).
It can wait for the result or fire-and-forget. A depth limit from config prevents runaway recursion.

The node gets a `nodesdk.SubworkflowRunner` at construction (wired in `internal/app`; `execution.Subworkflows`
implements it). Waiting children run inline in the parent's worker slot instead of through the queue, so
all slots can be held by parents without deadlock; fire-and-forget children are queued normally. The
depth is `ExecutionInfo.Depth` for the node and is computed by the worker from the `parentExecutionId`
chain; `engine.maxSubworkflowDepth` is checked by the node (clear error) and by the engine (backstop).
Known limit: an inline child is not re-queued separately after a crash; the parent's job is, and the
parent runs again (at-least-once, like any node).

### Sub-ports

Connections of an `ai.*` kind are not part of scheduling. `buildGraph` records them per parent input
(`nodeInfo.subs`) and validates them (this is also the save and activation validation):

- the sub-node must have an output of that kind and implement `nodesdk.Supplier`; the parent must have an
  input of that kind at the connection's `to.index`;
- `PortSpec.MaxConnections` is enforced; supply cycles are rejected;
- a missing `Required` connection is **not** a graph error (the editor saves unfinished workflows). It is
  checked just before the parent runs and fails the parent with `ai.model input is required: connect and
  enable a node on it`; that check is not retried.

Sub-nodes have no main ports, so they never receive a task, never count as a start node and are not
orphan errors. A sub-node cannot be the `StartNode` or a partial-run `Destination`; running up to the
parent works because suppliers are pulled lazily, not through `needed`.

`Input.SubPort(ctx, kind, itemIndex)` pulls on demand: for each enabled sub-node connected to the
parent's inputs of that kind, in connection order, the engine builds an `Input` for the sub-node (its own
parameters, credentials, HTTP, blob, logger and `ExecutionInfo`; `NodeName` is the sub-node's) and calls
`Supply`. Parameters and expressions resolve against the parent's item `itemIndex` (`$json` is the
parent's item) whatever index the sub-node passes. Disabled sub-nodes are skipped. A failure, panic or
the sub-node's own `settings.timeout` is wrapped as `sub-node "Name" (type) failed: ...` (keeping the
inner description and HTTP status) and, returned by the parent, fails the parent node, so its retry
policy, `onError` and error output apply to it. Sub-nodes produce no `NodeRun`, observer event or span.

## 4.8 Observer (progress)

```go
type Observer interface {
    ExecutionStarted(ctx context.Context, ex ExecutionInfo)
    NodeStarted(ctx context.Context, node string, runIndex int)
    NodeFinished(ctx context.Context, node string, run domain.NodeRun)
    ExecutionFinished(ctx context.Context, result Result)
}
```

The worker wires the observer to the bus, so the editor receives live updates. `engine.Request.Observer`
overrides `Deps.Observer` for one run: the worker passes a per-execution observer so each event carries its
execution id. Bus events (`port.Event`): `execution.started`, `node.started`, `node.finished` (with the
`NodeRun`), `execution.finished` (published after the result is stored, also for `waiting`) and
`execution.cancel` (published by `Cancel`).

## 4.9 Golden tests

`testdata/<case>/workflow.json` + `input.json` → `expected.json` (runData order and outputs).
Cases must cover: linear flow, IF branches, fan-out order, Merge waitAll, empty-output stop,
alwaysOutputData, disabled node, pin data, retry, every onError mode, loops with maxNodeRuns,
wait/resume round-trip, and partial run to a destination.

## 4.10 Execution rules

The engine follows these rules: canvas ordering of siblings with each branch finishing first, empty outputs ending a branch, disabled nodes passing input 0 to output 0, `executeOnce` passing only the first item of each input, pin data only in manual runs, `$("Node")` defaulting to the node's last run, and a run index counted per node.

| Area | Engi | Why |
|---|---|---|
| Multi-input nodes with partial data at the end | Only nodes declaring `MergePolicy: partialOnFlush` run; others (`waitAll`) are dropped | The policy is explicit per node descriptor |
| Retry | `retry.maxAttempts` is the total tries, optional exponential backoff, caps from config; only returned errors are retried | Configurable, no magic clamps; Engi nodes signal failure with errors |
| Node throws with continue enabled | The engine builds the error items (one per input) | Nodes do not each reimplement error handling |
| Pin data in expressions | Only nodes that ran (or were pinned and reached) | Simpler lineage |
| Waiting data across loop runs | Keyed per node; do not feed a multi-input node from a loop and expect separate runs | Not needed yet |
| Loop guard | `maxNodeRuns` per node from config | Bounds all loops |

## 4.11 Error workflows

A workflow can name an error workflow in `settings.errorWorkflowId`. The error workflow starts with an
`engi.trigger.error` (Error Trigger) node.

- **When.** The worker tells `errorflow.Notifier` (`internal/service/errorflow`) after it has stored a
  terminal `error` or `crashed` result. That covers failures in `execute`, executions the lease reclaimer
  gives up on, and jobs that ran out of attempts, on whichever worker finishes the execution, so the hook
  runs once per failure in cluster mode too. `success` and `canceled` never start one.
- **Which modes.** `webhook`, `trigger`, `retry` and `subworkflow`. Not `manual` (the person running it sees
  the error) and not `error`.
- **How.** The notifier loads the workflow version the execution ran, reads its `errorWorkflowId`, checks
  the error workflow exists and has an enabled Error Trigger, then calls `execution.Service.Start` with mode
  `error`. The run is queued like any other execution, so it never delays or fails the original one. The
  dedup key `error:<failed execution id>` makes a repeated hook (for example a worker that died between
  storing the result and queueing the error run) start only one error execution.
- **Item.** `{execution: {id, mode, status, error: {message, description, node, kind}, lastNodeExecuted},
  workflow: {id, name}}`. `lastNodeExecuted` is the failing node, empty for failures outside a node.
- **Loops.** An execution in mode `error`, or one with an `error` execution among its parents (a
  sub-workflow of an error workflow), never starts another error workflow. A workflow may be its own error
  workflow; if that run fails it stops there.
- **Problems.** A missing error workflow, one without an Error Trigger, or a failed start is logged and
  ignored; the failed execution keeps its result.
- **Recognising them.** Error runs have mode `error`. There is no parent link: it would raise the sub-workflow
  depth and be copied by retries. The failed execution's id is in the item.

Manual runs are excluded, and the workflow may name itself. Engi stops every error chain: an execution in mode `error` never starts another error workflow, including through a sub-workflow. Engi requires the setting, so a workflow with an Error Trigger but no error workflow set is not used as its own error handler. The item has no `url` or `retryOf` field.

---
**← Previous** [3. Data Model](03-data-model.md) · **Index** [README](README.md) · **Next →** [5. Expressions](05-expressions.md)
