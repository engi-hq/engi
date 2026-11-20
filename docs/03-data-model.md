# 3. Data Model

All types live in `internal/domain`. JSON shapes below are the **wire/file format** (the
adapters map between them and the domain).

## 3.1 Workflow

```json
{
  "id": "wf_01J...",
  "name": "Order sync",
  "active": true,
  "version": 7,
  "settings": { "timeoutSeconds": 300, "errorWorkflowId": "wf_...", "saveSuccessData": true },
  "nodes": [ /* Node */ ],
  "connections": [ /* Connection */ ],
  "pinData": { "Fetch orders": [ { "json": { "id": 1 } } ] },
  "tags": ["sales"],
  "notes": [ /* Sticky note, see 3.9 */ ]
}
```

- IDs are ULIDs with a type prefix (`wf_`, `ex_`, `cr_`, `nd_`).
- `settings.errorWorkflowId` names the workflow Engi starts when an execution of this one fails (it must contain an
  Error Trigger node; see [page 4](04-engine.md#411-error-workflows)). The editor has no field for it yet; set it
  through the API.
- Every save creates an immutable **WorkflowVersion**. Executions point to the version they ran.

## 3.2 Node

```json
{
  "id": "nd_01J...",
  "name": "Fetch orders",
  "type": "engi.http.request",
  "typeVersion": 1,
  "position": [320, 160],
  "parameters": { "method": "GET", "url": "={{ $vars.apiBase }}/orders" },
  "credentials": { "httpAuth": "cr_01J..." },
  "disabled": false,
  "settings": {
    "retry": { "maxAttempts": 3, "backoff": "exponential", "initialDelayMs": 1000 },
    "onError": "stop",
    "alwaysOutputData": false,
    "executeOnce": false,
    "timeoutMs": 0
  },
  "notes": ""
}
```

- `name` is unique within a workflow (expressions reference nodes by name).
- `type` is a namespaced identifier: `engi.<group>.<name>` for built-ins, `<vendor>.<name>` for plugins.
- A parameter value that starts with `=` is an **expression** (see [page 5](05-expressions.md)).
- `onError` is one of `stop` (fail the execution), `continue` (pass the error on as an item) or `errorOutput` (route to a dedicated error port).

## 3.3 Connection

Connections are a flat list. That makes them easy to diff, query and validate.

```json
{ "from": { "node": "IF", "port": "main", "index": 0 },
  "to":   { "node": "Send email", "port": "main", "index": 0 } }
```

- **Port kind** is `main` for data flow, or a typed sub-port for AI composition
  (`ai.model`, `ai.tool`, `ai.memory`, `ai.embedding`, `ai.vectorStore`, `ai.textSplitter`; any
  `ai.` name is valid). Sub-ports are pulled by the parent node, not executed as steps.
- `port` is the port **kind**. `index` selects the output or input number: IF's `true` output is `main/0` and `false` is `main/1`, Merge's inputs are `main/0`, `main/1`. Display names like "true" come from the node's Descriptor, not the connection.
- For a sub-port connection the direction is **sub-node output to parent input**, both of the same kind,
  and `index` is the ordinal among the ports *of that kind* (so a parent's first `ai.tool` input is
  `ai.tool/0` whatever its position in the descriptor). Order in the connection list is the order the
  parent receives the values. A sub-node may feed several parents. Sub-node results are not stored in
  `runData` (see [page 14](14-ai.md), Sub-ports).

## 3.4 Item

The unit of data flowing between nodes. A node receives `[]Item` per input and returns `[]Item` per output.

```json
{ "json": { "orderId": 42 },
  "binary": { "invoice": { "ref": "blob://...", "mime": "application/pdf", "name": "inv.pdf", "size": 18234 } },
  "pairedItem": [ { "input": 0, "item": 3 } ],
  "error": null }
```

- `binary` holds **references** to the blob store. Bytes never travel inside execution data.
- A reference is `blob://<executionId>/<id>`; both segments are restricted to `[A-Za-z0-9_-]`, so a reference can never address a path outside the store. Blobs belong to the execution that created them: deleting or pruning the execution deletes them (a sweep also removes blobs of executions that no longer exist, once older than `blob.orphanGrace`). Items passed between executions (sub-workflows) keep pointing at the creating execution's blobs.
- `port.BlobStore` has two adapters: `blob/fs` (directory; atomic temp-file-and-rename writes) and `blob/s3` (S3-compatible bucket; multipart upload beyond one part). `service/blob` enforces `blob.maxItemBytes` and `blob.maxExecutionBytes` and gives each node a `Blob()` access scoped to its execution.
- Writers: webhook multipart file parts (one binary field per file, named after the form field; text fields stay in `body`), the HTTP Request node (response format `binary`), the S3 node (download as binary) and the IMAP trigger (attachments as `attachment_0`, `attachment_1`, ...). Readers: the HTTP Request node (body type `binary`), Send Email attachments, the S3 node (upload) and the editor (`GET /executions/{id}/binary/{ref}`, page 9).
- Webhook and poll triggers learn the execution id before the execution exists (`StartRequest.ExecutionID`) so their uploads are stored under it; uploads of a call that did not start an execution are deleted.
- `pairedItem` records which input item(s) produced this item. It powers lineage and the `$("Node").item` expression.

## 3.5 Execution

```json
{
  "id": "ex_01J...",
  "workflowId": "wf_...", "workflowVersion": 7,
  "mode": "webhook | trigger | manual | retry | subworkflow | error",
  "status": "queued | running | waiting | success | error | canceled | crashed",
  "startedAt": "...", "finishedAt": "...",
  "waitUntil": null,
  "dedupKey": null,
  "parentExecutionId": null,
  "attempts": 1,
  "lastRecoveredAt": null,
  "runData": { "Fetch orders": [ /* NodeRun per attempt/run */ ] },
  "error": null
}
```

`NodeRun`:

```json
{ "runIndex": 0, "startedAt": "...", "durationMs": 120, "status": "success",
  "input":  { "main": [[ /* items */ ]] },
  "output": { "main": [[ /* items */ ]] },
  "error": null }
```

- `dedupKey` is optional. Triggers set it (for example webhook idempotency key or message offset) and a unique index on `(workflowId, dedupKey)` drops duplicates. See [page 8](08-triggers.md).
- `runData` is stored as a blob when it exceeds a configured size. Retention is configurable by status and age.
- `waiting` executions save the full engine state (see [page 4](04-engine.md#46-waiting--resume)).
- `attempts` counts the claims that started a run (migration `0008`). A retry after a dead worker or a failed
  infrastructure attempt adds one. `lastRecoveredAt` is set when a dead worker's run was taken over, and is
  null otherwise. The holder of a running execution (its queue job and claim attempt, `run_job_id` and
  `run_job_attempt`) is stored with it and is not exposed in the API. It fences stale claims: a requeue only
  applies to the claim that holds the run.

## 3.6 Credential

```json
{ "id": "cr_...", "name": "Prod Postgres", "type": "engi.postgres",
  "keyId": "k2", "data": "<nonce || AES-256-GCM ciphertext>", "createdAt": "...", "updatedAt": "..." }
```

- A credential *type* declares a schema (fields, secret flags, OAuth2 settings) and a test function.
- `keyId` names the encryption key used, which makes key rotation possible (see [page 7](07-data-sources.md#72-encryption-at-rest)).
- Decryption happens only inside the worker, right before node execution.

## 3.7 Tag and Variable

- **Tag** (`tg_`): `id`, `name` (unique, trimmed, at most 50 characters), `createdAt`. A workflow's `tags` array holds tag *names*. Saving a workflow registers any new name, so the tag list always covers every tag in use. Renaming a tag renames it on every workflow (a workflow that already carries the new name keeps one copy); deleting it removes it from every workflow. The tag tables are authoritative for a workflow's current tags; stored versions keep the tags they were saved with. Workflow tags are returned sorted.
- **Variable** (`vr_`): `id`, `key` (unique identifier `[A-Za-z_][A-Za-z0-9_]*`, at most 100 characters), `value` (string), `secret`, timestamps. Every variable is available as `$vars.KEY` in all executions. A secret value is encrypted at rest with the credential keyring (sealed with its ID as additional data) and API responses show the sentinel `__ENGI_SECRET__` instead. The flag cannot be turned off without sending a new value.

## 3.8 Other entities

| Entity | Purpose |
|---|---|
| User, ApiKey | Authentication (v1: owner + members, API keys) |
| Project | Ownership boundary for workflows and credentials (multi-team) |
| WebhookRegistration | Maps `method + path` to `workflow + node` for active workflows |
| ArmedTestWebhook | One-shot test URL (`/hook-test/`) armed from the editor, shared by all pods, expires after `webhook.testTimeout` |
| TriggerState | Poll cursors and listener offsets per trigger node |

## 3.9 Sticky notes

Sticky notes are annotations people draw on the canvas. They are never executed.

```json
{
  "id": "nt_4f1c9e0a7b2d3c5e6f80",
  "text": "## Retry policy\n\nSee the [runbook](https://example.com/runbook).",
  "position": [120, -40],
  "size": { "width": 320, "height": 200 },
  "color": "blue"
}
```

- `text` is markdown. The editor renders it without raw HTML; links open in a new tab with `rel="noopener noreferrer nofollow"` and only `http`, `https` and `mailto` links are live.
- `position` is the top-left corner in canvas coordinates; `size` is in the same units and must be positive. The editor keeps notes behind nodes and enforces a minimum size.
- `color` is a name from the fixed palette `yellow`, `blue`, `green`, `red`, `purple`, `gray` (omitted means `yellow`). The editor maps each name to `--note-<name>-bg` and `--note-<name>-border` theme tokens, so light and dark themes each get their own shade.
- `id` is chosen by the client (letters, digits, `-`, `_`; at most 64 characters) and unique within the workflow.
- Limits: `workflow.maxNotes` (default 200) and `workflow.maxNoteTextBytes` (default 16 KiB), see [page 11](11-deployment.md#115-configuration). Over the limit, a save answers 422 with the path of the offending note.

**Storage decision.** Notes are a separate `notes` array on the workflow (domain `Workflow.Notes []Note`), not a node. Engi keeps them apart because every place that walks `nodes` (validation against node descriptors, the node registry, port and connection checks, name uniqueness and `$("Node")` lookups, pin data, run data, partial-run destinations, node counts in the workflow list, the autolayout) would otherwise need a "but not notes" branch, and a note has no ports, parameters, credentials or run state. With a separate array the engine never sees notes, an old server simply rejects the unknown field on import instead of showing a broken node, and notes can have their own limits and shape. The workflow definition is already one JSON document per version, so no SQL migration is needed: notes ride along in `workflows.definition` and `workflow_versions.definition`, which means version history, restore, export and import carry them for free. Cost: one more list to copy in the few places that clone a document (copy and paste in the editor does it).

A note has `content`, `width` (default 240), `height` (default 160) and a named `color`. Notes have no show-on-hover or checkbox extras.

---
**← Previous** [2. Architecture](02-architecture.md) · **Index** [README](README.md) · **Next →** [4. Execution Engine](04-engine.md)
