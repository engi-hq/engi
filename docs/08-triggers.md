# 8. Triggers & Webhooks

Triggers start executions. Node interfaces are in [page 6](06-nodes-and-plugins.md#61-node-contract-go-pkgnodesdk);
this page defines how they are run. Package: `internal/service/trigger`.

## 8.1 Activation lifecycle

A workflow is *active* when `active=true`. Activation is a use case, not a flag flip.

**Activate**
1. Validate the saved version (graph, parameters, credentials exist). Fail with a problem response if not.
2. For each enabled trigger node, register it by kind:
   - webhook: write a `WebhookRegistration` (`method + path` → workflow, node). Path must be unique, else conflict.
   - schedule: add a job to the scheduler.
   - poll / listen: create `TriggerState` and start the poller or listener.
3. Mark the workflow active and publish `workflow.activated` on the bus.

**Deactivate** reverses it: remove registrations, stop schedules, pollers and listeners,
then publish `workflow.deactivated`. Roles react to bus events, and also reconcile from the
store at startup and periodically (bus messages can be missed). A failing activation leaves
the workflow inactive and records the reason.

| Kind | Run by role | Needs leader |
|---|---|---|
| Webhook | `webhook` (any replica) | no |
| Schedule | `trigger` | yes |
| Polling | `trigger` | yes |
| Listen (Kafka, IMAP, Postgres LISTEN) | `trigger` | yes |

## 8.2 Webhooks

| Route | Purpose | Requires active workflow |
|---|---|---|
| `/hook/<path>` | Production | yes |
| `/hook-test/<path>` | Test: one-shot, armed from the editor ("Listen for event"), expires after a config timeout. Runs in `manual` mode and streams to the editor | no |

- Route key is `method + path`. Paths are user-defined, or generated (`ULID`) by default. Dynamic segments (`/hook/orders/:id`) are exposed as `$json.params`.
- Request becomes the trigger item: `{ headers, params, query, body }`. Binary uploads (multipart) go to the blob store as references.
- Base prefix, body size limit, header limit and read timeouts come from config (`webhook.*`).

### Response modes

| Mode | Behaviour |
|---|---|
| `immediately` | `202`-style reply with a configurable body; execution runs async |
| `lastNode` | Hold the connection until the execution finishes; respond with the last node's output |
| `respondNode` | Hold the connection until a **Respond to Webhook** node runs; it sets status, headers and body. The rest of the workflow continues after the reply |

In cluster mode the worker publishes the response on the bus; the `webhook` pod holding the
connection delivers it. If no response arrives within `webhook.responseTimeout`, reply `504`
and let the execution carry on.

### Authentication options

`none`, Basic, Header token, JWT (HS/RS), and IP allow-list. The secret comes from a
credential. Checks run **before** the body is read in full and before an execution row exists.
Failures return `401/403` without leaking which check failed. CORS origins are a per-node option.

## 8.3 Schedule

| Mode | Definition |
|---|---|
| Cron | 5- or 6-field expression plus an IANA **timezone** (default from config) |
| Interval | Every N seconds/minutes/hours/days, anchored at activation or a fixed time |

- Scheduler state is the next fire time per trigger, stored so a restart does not reset it.
- DST: a skipped local time fires at the next valid instant; a repeated one fires once.
- **Missed-run policy** (per trigger, default from config): `skip` (drop missed runs), `runOnce` (catch up with one run), `runAll` (bounded by `schedule.maxCatchUp`). Applies after downtime or leader failover.
- A run is skipped if the previous run of the same trigger is still going and `allowOverlap=false`.

## 8.4 Polling triggers

`Poll(ctx, cursor)` returns `(items, nextCursor)`.
- The cursor (last ID, timestamp, ETag) is stored in **TriggerState**.
- Order of operations: poll → create executions for items → persist `nextCursor`. A crash in between re-delivers, so delivery is at-least-once. Nodes should emit stable IDs for dedup (8.7).
- Interval, jitter and backoff on errors come from config. Repeated failure deactivates the trigger after `poll.maxFailures` and records the error.
- The first poll after activation only establishes the cursor unless the node sets `emitInitial`.

## 8.5 Listen triggers

Long-lived consumers: Kafka, IMAP, Postgres `LISTEN`, MQTT.
`Listen(ctx, emit)` blocks and calls `emit(items)`; `emit` returns only after the execution is **durably enqueued**.

| Source | Offset / ack rule |
|---|---|
| Kafka | Commit offset after `emit` returns (at-least-once). Consumer group = trigger ID. Offsets also mirrored in TriggerState |
| IMAP | Track UID + UIDVALIDITY; mark seen or move after `emit` |
| Postgres LISTEN | No replay (notifications are ephemeral); documented as at-most-once, with optional "poll fallback" |

Listeners reconnect with capped exponential backoff, stop on context cancel, and each has an
owner goroutine tracked by the trigger service (bounded count from config).

## 8.6 Leader election (cluster mode)

Only the **leader** `trigger` replica runs schedules, pollers and listeners. Webhooks do not
need it.

- Provider is the `LeaseProvider` port: Kubernetes `Lease` (default on k8s), Postgres advisory lock, or local (standalone, always leader).
- Config: `lease.duration`, `lease.renewDeadline`, `lease.retryPeriod`, standard Lease semantics.
- On losing the lease, a replica cancels its triggers' contexts **before** another can start them (renew deadline < duration). The new leader loads active workflows from the store and resumes from TriggerState.
- Sharding by workflow hash across several leaders is a later extension; the port allows one lease per shard.

## 8.7 Deduplication and idempotency

Delivery is at-least-once everywhere, so duplicates are expected and bounded:
- Triggers may set a **dedup key** (e.g. webhook `Idempotency-Key` header, Kafka partition+offset, email Message-ID). Executions carry it; a unique index on `(workflowId, triggerNode, dedupKey)` within a retention window rejects repeats.
- Webhook duplicates return the original execution's response where available.
- Workers re-running a crashed execution resume from the last persisted state; side-effecting nodes should accept an idempotency key where the target supports it.
- The *Remove Duplicates* node covers user-level dedup inside a workflow.

## 8.8 Implementation status (Phase 2b-3b)

Built: webhook and schedule triggers, activation, the webhook HTTP handler, wait-resume by token.
Listen triggers (Kafka, 8.9) and polling triggers (IMAP, 8.10) are built since; JWT and IP allow-list authentication, CORS options and Kubernetes/Postgres leases are still to do.

**Packages.** `internal/service/trigger` (activation, scheduler, route resolution, test webhooks), `internal/adapter/http/webhook` (`http.Handler`), `internal/adapter/lease/local`, `internal/cronexpr`, nodes `webhooktrigger`, `scheduletrigger`, `respondtowebhook`. `internal/app.Runtime` wires them: `rt.Triggers` (the activator), `rt.Webhooks` (mount on `rt.Webhooks.Patterns()`), `rt.StartTriggers(ctx)`.

**Activation.** `Activate(ctx, id)` reads the workflow, asks every enabled trigger node for its spec (`WebhookTrigger.Webhook`, `ScheduleTrigger.Schedule`), checks paths, methods, cron/interval and that the bound authentication credential exists, and reports all problems as one validation error (HTTP 422). A webhook or chat trigger in `responseNode` mode also needs an enabled Respond to Webhook node reachable from it over main connections (`nodesdk.WebhookResponder`, found by the node type's marker, so the service has no node type strings); otherwise it fails with e.g. `Webhook "Hook" responds with a Respond to Webhook node, but the workflow has none connected after it`. The same check runs when a test webhook is armed. A workflow with no Webhook or Schedule trigger cannot be activated. Webhook routes go to the store (`webhook_registrations`, unique by method and *shape*, so `orders/:id` and `orders/:order` collide); a clash returns `domain.ErrConflict`. Any failure leaves the workflow inactive and records the reason (`ActivationError`). Re-activating re-registers. `ActivateAll` runs at start: broken workflows are made inactive with the reason, the others continue.

**Routing.** Static routes win over parameter routes; among parameter routes the one with the most literal segments wins. The default path is `<workflowId>/<node name slug>`. A request resolves to the current saved version of an active workflow.

**Request item.** `{headers, params, query, body}`. Header names are lower-case; `Authorization`, `Proxy-Authorization`, `Cookie` and the header used for header authentication are dropped, because execution data is stored. Repeated query or form keys become lists. JSON bodies are parsed (400 on invalid JSON), `application/x-www-form-urlencoded` and multipart text fields become objects, anything else is text (base64 when not UTF-8), `rawBody` always gives text. Multipart *files* are streamed to the blob store and appear as binary fields of the trigger item, named after the form field (`doc`, `doc_1`, ...); text fields stay in `body` and together stay under `webhook.maxBodyBytes`. A file over `blob.maxItemBytes` (or an execution over `blob.maxExecutionBytes`) gets 413 and nothing is kept. Other bodies over `webhook.maxBodyBytes` get 413, also for chunked uploads.

**Authentication.** `basic` and `header` read the secret from the credential bound to the node (`basicAuth` / `headerAuth` slots). Checks run before the body is read and before an execution exists, in constant time; any failure is a bare 401.

**Response modes.** `immediately`: `202` (configurable) with `{"executionId"}` or the configured body. `lastNode`: `RunSync` bounded by `webhook.responseTimeout`; answers with the first entry, all entries or nothing of the last finished node; a failed run is `500` with only the execution id (node errors can hold internals); a run that went to `waiting` answers `202`. `responseNode`: the handler subscribes to the bus *before* starting, and answers with the first node run that carries a `Response` (the Respond to Webhook node); the execution carries on afterwards. If the execution ends without responding: `500`, or `202` when it is waiting. No answer in time: `504`, the execution continues. Because the response travels on the bus, the pod holding the connection need not be the one running the node.

**Idempotency.** The header `webhook.idempotencyHeader` becomes the dedup key `<node>:<value>` (not for test calls). A repeat returns the original execution id (`immediately`) or the original execution's last-node output (`lastNode`/`responseNode`); the Respond node's custom reply cannot be replayed because it is not stored.

**Test webhooks.** `Service.ArmTest(ctx, workflowID, node)` opens a one-shot route under `/hook-test/` for `webhook.testTimeout`; the call runs in `manual` mode, needs no active workflow and ignores idempotency. Armed tests are stored in the shared store (`test_webhooks`, migration 0007), keyed by method and shape, so arming again replaces the earlier test of that route. Any pod with the `webhook` role serves the call, whichever pod armed it. `ResolveTest` claims the test with one atomic `DELETE ... RETURNING`, so when calls race on several pods exactly one is served and the rest get `404`. Arming deletes the expired tests; expired rows never answer. Deactivating a workflow on any pod disarms its tests.

**Wait resume.** `/hook-wait/<executionId>.<secret>` (GET or POST) resumes the execution with the item `{headers, query, body}`. The secret is drawn when the execution starts and stored with its state; every webhook Wait node of the execution shares it, so `$execution.resumeUrl` (built on `server.publicUrl`) works for each of them. Unknown, malformed, wrong and already-used tokens are all `404`. The URL is a bearer link: whoever holds it can resume the waiting node.

**Schedules.** Run only while the process leads. The scheduler wakes every `schedule.tick` (through the injected `Sleeper`) and fires schedules whose next time has come. Interval schedules stay on the grid anchored at activation; cron expressions (5 or 6 fields, `*,-/` lists, names, `?`) are evaluated in the node's IANA timezone, DST rules as in 8.3. The next time is saved in `trigger_state` before the next tick, so a restart continues the grid. A run overdue by more than `schedule.missedGrace` is *missed*: `skip` drops it, `runOnce` starts one catch-up run; either way the schedule continues at its next future time (`runAll`/`maxCatchUp` and the `allowOverlap` check are not built). Each fire starts an execution in mode `trigger` with dedup key `<node>:<scheduled time>`, so a repeated fire after a crash is rejected. If starting fails for a transient reason the fire is retried on the next tick. A new leader loads active workflows from the store; replicas also follow `workflow.activated/deactivated` bus events.

**Webhook behaviour.** Test webhooks are one-shot and expire; production webhooks only answer for active workflows; static paths take precedence over parameterised ones; three response modes with the Respond node replying while the workflow continues; the request becomes `headers/params/query/body`; the Wait node resumes on a per-execution URL. **Design choices.** Resume URLs carry a random secret checked in constant time instead of a signature over the execution id; credentials never enter stored execution data (headers are scrubbed); failed `lastNode` runs return a generic error body; activation conflicts are checked on `method + shape`, so differently named parameters cannot shadow each other; missed-run handling is explicit and configurable.


## 8.9 Kafka trigger and producer (as built)

**Rule: the consumer only accepts messages, and workers run the workflows.** Accepting messages and running workflows scale independently.

```
topic (N partitions) → trigger pods (one consumer group per workflow+node; Kafka balances partitions)
   each message/batch: Executions.Start (dedupKey topic:partition:offset) → commit offset
→ Engi job queue → worker pods (autoscaled on queue depth) → Kafka Producer node (pooled producer)
```

| Concern | As built |
|---|---|
| Contract | `nodesdk.ListenTrigger`: `CheckListen(params)` at activation, `Listen(ctx, ListenInput, emit)` while active. `ListenInput` carries workflow id, node name, saved parameters, decrypted credentials by slot, a logger and `Overloaded(ctx)`. `emit(ctx, ListenEvent{Items, DedupKey, AwaitCompletion})` returns nil once the execution exists (or already existed) |
| Distribution | The trigger service runs listeners on **every** instance, not behind the lease (`internal/service/trigger/listen.go`). They start on `Activate`, on `workflow.activated/deactivated` events from other instances, and from `ActivateAll`; they stop on deactivate and `Shutdown`, which closes the consumer so it leaves the group at once. A failed listener restarts after `listen.retryDelay`. An event for an unchanged node does not restart it; an explicit `Activate` does (credentials are read again). Schedules and pollers stay leader-only |
| Group | `groupId`, default `engi-<workflowId>-<node name>` with blanks turned into `-`. Parallelism scales to the partition count. `startOffset` applies only while the group has no committed offset |
| Delivery | Manual commits only (`DisableAutoCommit`), `read_committed` fetch. Offsets are committed after `emit` returned, so a crash repeats messages rather than losing them. The host scopes the key to the node (`<node>:<dedupKey>`), and a duplicate counts as success, so redelivery makes no second execution. A failed `emit` commits what was delivered before it, ends the run, and the host restarts the listener |
| Dedup key | `perMessage` and `ordered`: `topic:partition:offset`. `batch`: `topic:partition:first-last` |
| Modes | `perMessage` (default): one execution per message, one commit per poll. `batch`: per partition, up to `batchSize` messages or `batchWaitMs` since the oldest, one execution with one item per message; both capped by `kafka.batchMaxMessages` / `kafka.batchMaxWait`. `ordered`: one message at a time, the listener waits for the execution to finish (`AwaitCompletion`, bounded by the execution sync timeout) and commits after each, so a message of a partition never starts before the previous one ended. It is slow by design |
| Item | `{topic, partition, offset, timestamp, key, value, headers}`. `valueFormat` json (default) parses the value, text keeps it as a string. Repeated headers become a list |
| Poison messages | A value that does not decode as JSON is copied (key, value, headers, plus `engi-error` and `engi-source`) to `deadLetterTopic` and skipped; if the copy fails nothing is committed. Without a dead-letter topic the message is kept: `value` is the raw text and `valueError` says why |
| Backpressure | `ListenInput.Overloaded` is a gate over the new `port.QueueStats.Depth` (SQLite queue implements it): overloaded above `kafka.pauseAboveDepth`, clear again below `kafka.resumeBelowDepth`, sampled at most every `kafka.depthCheckInterval`. The consumer pauses and resumes fetching of its topics; records already fetched are still processed |
| Producer | `engi.kafka.produce`: one record per item; JSON or text value, key, headers, `acks` (all/leader/none), `compression`, `idempotent` (default on, needs `acks` all). One client per credential version and option set via `clientcache`, flushed and closed on shutdown |
| Library | `franz-go` (BSD-3, pure Go) |
| Autoscaling (Phase 5) | Workers: KEDA on Engi queue depth. Trigger pods: KEDA Kafka scaler on consumer lag (max useful replicas = partitions) |

**Changed from the first design, and why.**
- *Batch dedup key is the offset range, not the first offset.* A batch redelivered after a crash can end at a different offset than the first attempt (time-based flush). Keyed on the first offset alone, the larger redelivered batch would be dropped as a duplicate and its extra messages lost. With the range the worst case is a repeated message, never a lost one.
- *Batches are per partition*, so the commit point and the key are well defined. While a batch is open the consumer does not allow a rebalance (`BlockRebalanceOnPoll`), which is why `kafka.batchMaxWait` must be shorter than `kafka.rebalanceTimeout`.
- *Ordered mode orders executions, not just starts*, by waiting for each; ordering of job enqueue alone would not hold with several workers. It is sequential per partition, a simplification of "per key".
- *Backpressure settings* live in the top-level `kafka` section (`kafka.pauseAboveDepth` etc., env `ENGI_KAFKA_*`), although the gate itself is generic.
- *Parameters are not expressions.* A listener has no input item, so the saved values are used as they are.
- *Credential changes* reach a running listener on the next activation or restart.
- *Not built:* Avro/Protobuf and schema registry, TLS client certificates, per-key parallelism inside a partition.

**Scaling.** Engi distributes group-based listeners across trigger pods so consumption scales horizontally.

## 8.10 Polling triggers and the IMAP trigger (as built)

**Host** (`internal/service/trigger/poll.go`). A node implementing `nodesdk.PollTrigger` is validated at activation (`CheckPoll` returns the interval, which must be at least `poll.minInterval`; credentials must exist). Pollers run **only on the leader**: they start when the workflow is activated while leading, when a new leader loads the active workflows, and stop when leadership ends or the workflow is deactivated. Each poll is: load the cursor from TriggerState, `Poll`, start **one execution per item** with dedup key `<node>:<item key>`, store the returned cursor, then call the result's `Done`. A crash before the cursor is stored re-delivers the items, and the dedup keys turn them into no-ops. A failed poll waits twice as long as before (up to `poll.maxBackoff`); `poll.maxFailures` failures in a row deactivate the workflow and record the last error (the activation error). The poll cursor and the schedule state share the TriggerState slot of a node, which is fine because a node is only ever one of them.

**IMAP trigger** (`engi.trigger.imap`, `internal/node/imaptrigger`, library `github.com/emersion/go-imap/v2` plus `go-message`, both MIT).
- Cursor `{uidValidity, lastUid}`. The first poll (or the first after UIDVALIDITY changed, which makes old UIDs meaningless) only records the newest UID unless `emitExisting` is on. After that each poll searches UIDs above `lastUid` with the server-side filters (unseen, `From`, `Subject` contain), verifies the filters locally, and emits at most `nodes.email.imapMaxMessages` mails in UID order. When everything fits, the cursor moves to the mailbox's next UID minus one, so filtered-out mails are not searched again.
- Dedup key `uidvalidity:uid`.
- `afterProcessing`: `markRead` sets `\Seen`, `move` moves the mail (`MOVE`, or `COPY`+delete when unsupported). It runs in `Done`, on a fresh connection, after the cursor is stored; if it fails the mails are not re-emitted (the cursor is already past them) and a warning is logged. If UIDVALIDITY changed in between nothing is touched.
- Mails are read with `BODY.PEEK`, so polling alone never marks anything read.
- Mails over `nodes.email.imapMaxMessageBytes` are emitted with headers only. Attachments are listed (name, type, size) but not stored: TODO until pollers can reach the blob store.

**Design.** The IMAP trigger polls on the leader with a UID cursor so a restart or failover cannot re-emit, and does not depend on the `\Seen` flag unless asked to. IDLE is a possible later listener mode (8.5 lists IMAP as a listener; the built version is a poll trigger).

## 8.11 Chat trigger (as built)

`engi.trigger.chat` is a webhook trigger with its own routes: `POST /chat/<path>` (a message, answered with
the workflow's reply), `GET /chat/<path>/widget` (hosted chat page) and the page's static files. The base path
is `chat.basePath`. It is served by the `webhook` role, outside API authentication, only while the workflow is active.

- Registration reuses the webhook route table under the pseudo method `CHAT`, so activation, the
  uniqueness check (a duplicate path fails activation with a conflict), deactivation and start-up
  restore are the webhook code paths, while `/hook/...` can never reach a chat trigger and chat paths never clash
  with webhook paths. Chat triggers have no test URL and are skipped when arming a test webhook.
- The item is `{sessionId, chatInput, metadata?}`; response modes are `lastNode` (reply field of the last
  node's first item) and `responseNode` (Respond to Webhook); authentication is none, basic or header; allowed
  origins drive CORS and the widget's `frame-ancestors`. Full contract, widget, embedding and the streaming
  design: [AI, chat trigger](14-ai.md#chat-trigger).

## 8.12 MCP server trigger (as built)

`engi.trigger.mcpServer` exposes workflows as tools of a Model Context Protocol server: `POST /mcp/<path>`
(`mcpServer.basePath`), served by the `webhook` role outside API authentication, only while the workflow is
active. Unlike every other trigger it starts no execution of its own workflow.

- Registration reuses the webhook route table under the pseudo method `MCP`, exactly as the chat trigger does
  with `CHAT`: activation, the uniqueness check (a duplicate path fails activation with a conflict),
  deactivation and start-up restore are the webhook code paths, while `/hook/...` can never reach an MCP trigger
  (`ResolveLive` refuses the method) and MCP paths never clash with webhook paths. MCP triggers have no test URL.
  Paths cannot have `:param` segments.
- Authentication is none, bearer token (`engi.http.bearer`) or header token (`engi.http.header`), checked in
  constant time before the body is read.
- A `tools/call` runs the tool's workflow synchronously with `RunSync`, in webhook mode, started at the first
  enabled Execute Workflow Trigger node of that workflow (the same rule as the Execute Workflow node), with the
  arguments as the single input item. Full contract: [AI, MCP](14-ai.md#1411-mcp-client-tool-and-mcp-server-trigger).

---
**← Previous** [7. Data Sources & Credentials](07-data-sources.md) · **Index** [README](README.md) · **Next →** [9. HTTP API](09-api.md)
