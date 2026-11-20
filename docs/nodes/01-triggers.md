# 1. Triggers

Nodes that start a workflow.

## Manual Trigger

**What it does.** Starts a workflow when you run it yourself, from the editor or the CLI. Use it while building, or for workflows that only ever run on demand.

**How it behaves**
- It has no activation step: it is never registered, and running it does not need the workflow to be active.
- It forwards the items the run was started with. A run started without input produces one empty item.
- A manual run uses mode `manual`, so pinned node data is honoured (see [Engine](../04-engine.md)).

**Output**

```json
{ "json": {} }
```

**Example.** No parameters. Connect its output to the first node.

**Notes**
- It is not started by schedules, webhooks or other events; those need their own trigger node. A workflow may hold several triggers.

<!-- BEGIN GENERATED: engi.trigger.manual -->
**Type:** `engi.trigger.manual` · **Version:** 1 · **Group:** `trigger` · **Display name:** Manual Trigger

Starts the workflow when you run it by hand.

**Inputs**

None.

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

None.

**Parameters**

None.
<!-- END GENERATED: engi.trigger.manual -->

## Webhook

**What it does.** Starts the workflow when an HTTP request arrives at its URL. Use it for callbacks from other systems, form posts and any "call this to run that" integration.

**How it behaves**
- **Activation.** Production URLs are `<publicUrl>/hook/<path>` and exist only while the workflow is active. Activation registers `method + path`; a second workflow with the same method and path shape (`orders/:id` and `orders/:order` count as the same) makes activation fail with a conflict. An empty `path` becomes `<workflow id>/<node name>`.
- **Test URL.** `/hook-test/<path>` is armed from the editor ("Listen for event"), answers one call, then expires after `webhook.testTimeout`. It runs in manual mode and needs no active workflow. Armed tests live in one process, so in a cluster the editor must reach the instance that armed it.
- **Path parameters.** A segment starting with `:` (for example `orders/:id`) is captured into `$json.params`. Static paths win over parameterised ones.
- **Every instance.** Webhooks are served by any instance running the `webhook` role; they do not need the leader.
- **Request body.** JSON is parsed (invalid JSON gives 400); form and multipart text fields become an object; anything else is text (base64 if not UTF-8). `rawBody` always gives text. Multipart files go to the blob store and appear as binary fields named after the form field. Bodies over `webhook.maxBodyBytes` get 413, as do files over `blob.maxItemBytes`.
- **Authentication.** `basic` and `header` read the secret from the credential in the `basicAuth` / `headerAuth` slot. The check runs before the body is read and before any execution exists; every failure is a bare 401.
- **Response modes.**
  - `immediately`: replies at once (202 by default) with `{"executionId": ...}` or your `responseBody`; the workflow runs afterwards.
  - `lastNode`: holds the connection until the run finishes and answers with the first entry, all entries or nothing from the last node (`responseData`). A failed run answers 500 with only the execution id. A run that ends up waiting answers 202.
  - `responseNode`: holds the connection until a [Respond to Webhook](02-flow.md#respond-to-webhook) node runs; the rest of the workflow keeps going after the reply. Activation (and "Listen for event") is refused unless an enabled Respond to Webhook node is reachable from this trigger through its connections; disabled nodes are passed through. If the run ends without one anyway, the answer is 500 (202 if waiting).
  - If no answer comes within `webhook.responseTimeout`, the caller gets 504 and the execution continues.
- **Dedup.** A call carrying the `Idempotency-Key` header (name from `webhook.idempotencyHeader`) is deduplicated per node. A repeat returns the original execution id (`immediately`) or the original last-node output; it does not start a second run. Test calls skip this.

**Output.** One item per call. Header names are lower-case; `Authorization`, `Proxy-Authorization`, `Cookie` and the header used for header authentication are removed, because execution data is stored.

```json
{
  "json": {
    "headers": { "content-type": "application/json" },
    "params": { "id": "42" },
    "query": { "dry": "1" },
    "body": { "status": "paid" }
  }
}
```

**Example**

```json
{
  "httpMethod": "POST",
  "path": "orders/:id",
  "responseMode": "lastNode",
  "responseData": "firstEntryJson",
  "authentication": "header"
}
```

**Notes**
- Limits: `webhook.maxBodyBytes` (default 16 MiB), `webhook.responseTimeout` (120 s), `blob.maxItemBytes`. See [Deployment, configuration](../11-deployment.md#115-configuration).
- JWT and IP allow-list authentication are not built yet.
- Notes: Engi uses resume and test URLs. Failed `lastNode` runs return a generic error body, not node error text, and path clashes are detected on the route shape.

<!-- BEGIN GENERATED: engi.trigger.webhook -->
**Type:** `engi.trigger.webhook` · **Version:** 1 · **Group:** `trigger` · **Display name:** Webhook

Starts the workflow when its URL is called.

**Inputs**

None.

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

| Slot | Accepted credential types | Required |
|---|---|---|
| `basicAuth` | `engi.http.basic` | no |
| `headerAuth` | `engi.http.header` | no |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `httpMethod` | HTTP method | `options` | `GET` | no | `GET`<br>`POST`<br>`PUT`<br>`PATCH`<br>`DELETE`<br>`HEAD` | always | — |
| `path` | Path | `string` | — | no | — | always | Relative to the webhook base path. A segment starting with : captures a value into $json.params. Empty uses <workflow id>/<node name>. |
| `responseMode` | Respond | `options` | `immediately` | no | `immediately` (Immediately)<br>`lastNode` (When the last node finishes)<br>`responseNode` (Using a Respond to Webhook node) | always | — |
| `authentication` | Authentication | `options` | `none` | no | `none` (None)<br>`basic` (Basic auth)<br>`header` (Header token) | always | — |
| `responseCode` | Response code | `number` | — | no | — | shown when `responseMode` = `immediately` or `lastNode` | Status of the answer. Default 202 for immediate answers, 200 otherwise. |
| `responseBody` | Response body | `string` | — | no | — | shown when `responseMode` = `immediately` | Immediate answer body. Empty answers {"executionId": ...}. |
| `responseData` | Response data | `options` | `firstEntryJson` | no | `firstEntryJson` (First entry (JSON))<br>`allEntries` (All entries)<br>`noData` (No data) | shown when `responseMode` = `lastNode` | — |
| `rawBody` | Raw body | `boolean` | `false` | no | — | always | Deliver the body as text in $json.body instead of parsing it. |
<!-- END GENERATED: engi.trigger.webhook -->

## Schedule Trigger

**What it does.** Starts the workflow on a fixed interval or a cron schedule. Use it for periodic jobs: nightly syncs, hourly reports, polling an API you cannot subscribe to.

**How it behaves**
- **Leader only.** Schedules run on the leader `trigger` instance (always the local one in standalone mode). Activating the workflow adds the schedule; deactivating removes it.
- **Interval** schedules stay on a grid anchored at activation. **Cron** takes five fields, or six with leading seconds, evaluated in `timezone` (default from `schedule.defaultTimezone`). A time skipped by daylight saving fires at the next valid instant; a repeated one fires once.
- **Restart.** The next fire time is saved before the next tick, so a restart or failover continues the grid instead of resetting it.
- **Missed runs.** A run overdue by more than `schedule.missedGrace` is missed. With `schedule.missedPolicy` `skip` it is dropped; with `runOnce` one catch-up run starts. Either way the schedule continues at its next future time.
- **Dedup.** Each fire has the key `<node>:<scheduled time>`, so a fire repeated after a crash does not start a second execution.
- Precision is `schedule.tick` (default 1 s).

**Output.** One item per fire.

```json
{ "json": { "timestamp": "2026-10-18T09:00:00+02:00", "timezone": "Europe/Berlin" } }
```

**Example**

```json
{ "rule": "cron", "cronExpression": "0 9 * * 1-5", "timezone": "Europe/Berlin" }
```

**Notes**
- Overlap is not prevented: a new run starts even if the previous one is still going (`allowOverlap` is not built).
- `runAll` catch-up is not built.
- Config: `schedule.defaultTimezone`, `schedule.tick`, `schedule.missedPolicy`, `schedule.missedGrace` ([Deployment](../11-deployment.md#115-configuration)).
- Notes: missed-run handling is explicit and configurable.

<!-- BEGIN GENERATED: engi.trigger.schedule -->
**Type:** `engi.trigger.schedule` · **Version:** 1 · **Group:** `trigger` · **Display name:** Schedule Trigger

Starts the workflow on an interval or a cron expression.

**Inputs**

None.

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

None.

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `rule` | Rule | `options` | `interval` | no | `interval` (Every interval)<br>`cron` (Cron expression) | always | — |
| `interval` | Every | `number` | `1` | no | — | shown when `rule` = `interval` | — |
| `unit` | Unit | `options` | `minutes` | no | `seconds` (Seconds)<br>`minutes` (Minutes)<br>`hours` (Hours)<br>`days` (Days)<br>`weeks` (Weeks) | shown when `rule` = `interval` | — |
| `cronExpression` | Cron expression | `string` | — | no | — | shown when `rule` = `cron` | Five fields (minute hour day month weekday) or six with leading seconds. |
| `timezone` | Timezone | `string` | `UTC` | no | — | always | IANA zone, for example Europe/Berlin. Cron expressions are evaluated in it. |
<!-- END GENERATED: engi.trigger.schedule -->

## Execute Workflow Trigger

**What it does.** The entry point of a workflow that other workflows call with an [Execute Workflow](02-flow.md#execute-workflow) node. Use it to build reusable sub-workflows.

**How it behaves**
- Nothing is registered at activation; the calling node starts the workflow by id.
- It forwards the items the caller passed in unchanged.
- The Execute Workflow node requires the target workflow to start with this trigger.
- Nesting depth is limited by the caller (see Execute Workflow).

**Output.** The caller's input items.

```json
{ "json": { "orderId": 42 } }
```

**Example.** No parameters. The last node of the sub-workflow supplies the result the caller receives.

**Notes**
- Notes: no input-schema declaration; the items are passed through as they are.

<!-- BEGIN GENERATED: engi.trigger.executeWorkflow -->
**Type:** `engi.trigger.executeWorkflow` · **Version:** 1 · **Group:** `trigger` · **Display name:** Execute Workflow Trigger

Starts the workflow when another workflow calls it.

**Inputs**

None.

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

None.

**Parameters**

None.
<!-- END GENERATED: engi.trigger.executeWorkflow -->

## Error Trigger

**What it does.** The entry point of an error workflow. When an execution of another workflow ends in `error` or `crashed` and that workflow names this one as its error workflow (`settings.errorWorkflowId`), Engi queues an execution of this workflow, in mode `error`, that starts here. Use it to send an alert, open a ticket or clean up.

**How it behaves**
- Nothing is registered at activation; Engi starts the workflow by id, so the error workflow does not need to be active.
- It forwards the single failure item it was started with.
- The error workflow must contain an enabled Error Trigger. If it does not exist or has none, Engi logs a warning and the failed execution is not affected.
- Only failures of executions started by a webhook, a trigger, a retry or a parent workflow start an error workflow. Manual runs do not, and neither do failures of error-workflow executions or of their sub-workflows, so an error workflow that fails never starts another one.
- A workflow can be its own error workflow: give it an Error Trigger and set `errorWorkflowId` to its own id.
- Each failed execution starts at most one error execution, even if the hook runs twice.

**Output.** One item describing the failure.

```json
{
  "json": {
    "execution": {
      "id": "ex_01J...",
      "mode": "webhook",
      "status": "error",
      "error": { "message": "nope", "description": "", "node": "Boom", "kind": "node" },
      "lastNodeExecuted": "Boom"
    },
    "workflow": { "id": "wf_01J...", "name": "Orders" }
  }
}
```

**Example.** Create the alert workflow with an Error Trigger followed by a Slack node, then set `settings.errorWorkflowId` of the workflows to watch to the alert workflow's id.

**Notes**
- Notes: no `url` field (Engi has no public base URL setting), no `retryOf`, and no sample item when run manually; a workflow with an Error Trigger does not handle its own errors unless it names itself. See [Error workflows](../04-engine.md#411-error-workflows).

<!-- BEGIN GENERATED: engi.trigger.error -->
**Type:** `engi.trigger.error` · **Version:** 1 · **Group:** `trigger` · **Display name:** Error Trigger

Starts the workflow when an execution of another workflow fails and this workflow is its error workflow.

**Inputs**

None.

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

None.

**Parameters**

None.
<!-- END GENERATED: engi.trigger.error -->


## Kafka Trigger

**What it does.** Consumes messages from one or more Kafka topics and starts an execution per message (or batch). Use it for event streams where you need horizontal scale-out.

**How it behaves**
- **Every instance.** Unlike schedules and pollers, Kafka listeners run on every `trigger` instance, not just the leader. Instances sharing a consumer group split the partitions, so parallelism scales up to the partition count. The default group is `engi-<workflowId>-<node name>`. `startOffset` applies only while the group has no committed offset.
- **Delivery is at-least-once.** Offsets are committed only after the execution has been created. A crash repeats messages rather than losing them, and the dedup key makes a repeat a no-op: `topic:partition:offset` (per message and ordered) or `topic:partition:first-last` (batch).
- **Modes.** `perMessage`: one execution per message. `batch`: per partition, up to `batchSize` messages or `batchWaitMs` since the oldest, one execution with one item per message. `ordered`: one message at a time; the listener waits for each execution to finish before committing, so it is slow by design.
- **Poison messages.** With `valueFormat` `json`, a value that does not decode is copied to `deadLetterTopic` and skipped. With no dead-letter topic the message is kept: `value` is the raw text and `valueError` says why.
- **Backpressure.** Fetching pauses while the job queue holds more than `kafka.pauseAboveDepth` jobs and resumes below `kafka.resumeBelowDepth`.
- A failing listener restarts after `listen.retryDelay`. Credential changes take effect on the next activation or restart.

**Output**

```json
{
  "json": {
    "topic": "orders", "partition": 3, "offset": 1042,
    "timestamp": "2026-10-18T09:00:00Z", "key": "42",
    "value": { "status": "paid" }, "headers": {}
  }
}
```

**Example**

```json
{ "topics": "orders,refunds", "groupId": "billing", "mode": "batch", "batchSize": 50, "batchWaitMs": 2000, "valueFormat": "json" }
```

**Notes**
- `batchSize` and `batchWaitMs` above `kafka.batchMaxMessages` / `kafka.batchMaxWait` fail activation. Other keys: `kafka.sessionTimeout`, `kafka.maxPollRecords`, `nodes.kafka.connectTimeout` ([Deployment](../11-deployment.md#115-configuration)).
- Parameters are not expressions: a listener has no input item.
- Not built: Avro/Protobuf, schema registry, TLS client certificates.
- Notes: Engi spreads consumers over trigger instances.

<!-- BEGIN GENERATED: engi.trigger.kafka -->
**Type:** `engi.trigger.kafka` · **Version:** 1 · **Group:** `trigger` · **Display name:** Kafka Trigger

Starts the workflow for messages (or batches) of Kafka topics. Runs on every trigger instance; Kafka balances the partitions.

**Inputs**

None.

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

| Slot | Accepted credential types | Required |
|---|---|---|
| `engi.kafka` | `engi.kafka` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `topics` | Topics | `string` | — | yes | — | always | Comma-separated topic names. |
| `groupId` | Consumer group | `string` | — | no | — | always | Empty uses engi-<workflowId>-<node name>. Instances sharing a group split the partitions. |
| `startOffset` | Start from | `options` | `latest` | no | `latest` (Latest)<br>`earliest` (Earliest) | always | Only used while the group has no committed offset. |
| `mode` | Mode | `options` | `perMessage` | no | `perMessage` (One execution per message)<br>`batch` (One execution per batch)<br>`ordered` (Ordered (one at a time, waits for each execution)) | always | — |
| `batchSize` | Max messages per batch | `number` | `100` | no | — | shown when `mode` = `batch` | — |
| `batchWaitMs` | Max wait per batch (ms) | `number` | `1000` | no | — | shown when `mode` = `batch` | — |
| `valueFormat` | Value format | `options` | `json` | no | `json` (JSON)<br>`text` (Text) | always | — |
| `deadLetterTopic` | Dead-letter topic | `string` | — | no | — | always | Messages whose value cannot be decoded are copied here and skipped. Empty keeps them: the item carries the raw text and valueError. |
<!-- END GENERATED: engi.trigger.kafka -->

## IMAP Trigger

**What it does.** Starts the workflow for each new mail in an IMAP mailbox. Use it for inbox-driven automations such as support triage or invoice intake.

**How it behaves**
- **Polling on the leader.** The leader polls every `intervalSeconds`. Activation fails if that is below `poll.minInterval` (default 15 s).
- **Cursor, not flags.** The node stores `{uidValidity, lastUid}` and searches only UIDs above it, so a restart or failover does not re-emit mails. The first poll only records the newest mail, unless `emitExisting` is on. If the server changes UIDVALIDITY, the cursor is reset the same way.
- **One execution per mail**, in UID order, at most `nodes.email.imapMaxMessages` per poll (default 50); the rest follow on later polls. Dedup key is `uidvalidity:uid`.
- **Filters.** `unseenOnly`, `from` and `subject` ("contains") are applied on the server and checked again locally.
- **After processing.** `markRead` or `move` run after the cursor is stored, on a new connection. If that fails, the mail is not re-emitted; a warning is logged. Mails are read with `BODY.PEEK`, so polling alone never marks anything read.
- **Failures.** A failed poll backs off (doubling, up to `poll.maxBackoff`). After `poll.maxFailures` failures in a row the workflow is deactivated and the last error recorded.

**Output.** One item per mail (fields shortened here).

```json
{
  "json": {
    "from": [{ "address": "ana@example.com" }], "subject": "Invoice 17",
    "messageId": "abc@example.com", "text": "Please find ...", "html": "...",
    "attachments": [{ "name": "inv.pdf", "type": "application/pdf", "size": 20481 }]
  }
}
```

**Example**

```json
{ "mailbox": "INBOX", "intervalSeconds": 120, "unseenOnly": true, "subject": "Invoice", "afterProcessing": "move", "moveTo": "Processed" }
```

**Notes**
- Mails over `nodes.email.imapMaxMessageBytes` (10 MiB) are emitted with headers only (`truncated: true`). Other keys: `nodes.email.imapTimeout`, `poll.*` ([Deployment](../11-deployment.md#115-configuration)).
- Attachments are listed (name, type, size) but their contents are not stored yet.
- Notes: Engi polls the mailbox, so mails arrive up to one interval late, but delivery survives restarts.

<!-- BEGIN GENERATED: engi.trigger.imap -->
**Type:** `engi.trigger.imap` · **Version:** 1 · **Group:** `trigger` · **Display name:** IMAP Trigger

Starts the workflow for every new mail of an IMAP mailbox. Polls on the leader instance.

**Inputs**

None.

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

| Slot | Accepted credential types | Required |
|---|---|---|
| `engi.imap` | `engi.imap` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `mailbox` | Mailbox | `string` | `INBOX` | no | — | always | — |
| `intervalSeconds` | Poll interval (seconds) | `number` | `60` | no | — | always | Not below the server's poll.minInterval. |
| `unseenOnly` | Only unread mails | `boolean` | `true` | no | — | always | — |
| `from` | From contains | `string` | — | no | — | always | — |
| `subject` | Subject contains | `string` | — | no | — | always | — |
| `afterProcessing` | After processing | `options` | `nothing` | no | `nothing` (Leave the mail as it is)<br>`markRead` (Mark as read)<br>`move` (Move to another mailbox) | always | — |
| `moveTo` | Move to mailbox | `string` | — | no | — | shown when `afterProcessing` = `move` | — |
| `emitExisting` | Emit existing mails on activation | `boolean` | `false` | no | — | always | Off: the first poll only remembers the newest mail, so only later mails start executions. |
<!-- END GENERATED: engi.trigger.imap -->

## Chat Trigger

**What it does.** Starts the workflow for each message sent to a public chat endpoint, and answers with the workflow's reply. It also serves a hosted chat page (the widget) for the same endpoint. Use it to put an AI Agent behind a chat window. Full contract: [AI, chat trigger](../14-ai.md#chat-trigger).

**How it behaves**
- **Routes.** While the workflow is active: `POST /chat/<path>` (messages), `GET /chat/<path>/widget` (hosted page). Chat paths are their own namespace; two chat triggers cannot share one, but a chat path never clashes with a webhook path.
- **Message.** Body `{"sessionId": "...", "message": "..."}`. `chatInput` is accepted as an alias of `message`. A missing `sessionId` is generated and returned in the reply and in the `X-Chat-Session-Id` header.
- **Reply.** `lastNode`: `{sessionId, output}` where `output` is the `replyField` (default `output`, falling back to `text`) of the last node's first item. `responseNode`: whatever a [Respond to Webhook](02-flow.md#respond-to-webhook) node sends. Activation is refused unless an enabled Respond to Webhook node is reachable from the trigger.
- **Auth.** None, basic or header token, with the same credential types as the Webhook. The hosted widget is only served when no authentication is set.

**Output.** One item per message.

```json
{ "json": { "sessionId": "9f2c...", "chatInput": "Where is my order?", "metadata": {} } }
```

**Example**

```json
{ "path": "support", "responseMode": "lastNode", "allowedOrigins": "https://shop.example.com", "title": "Support", "welcomeMessage": "Hi! How can we help?" }
```

**Notes**
- No streaming yet: the reply arrives in one piece. See the follow-up design in [AI](../14-ai.md#chat-trigger).
- Notes: the wire format uses `message`; there is no "load previous session" action.

<!-- BEGIN GENERATED: engi.trigger.chat -->
**Type:** `engi.trigger.chat` · **Version:** 1 · **Group:** `trigger` · **Display name:** Chat Trigger

Starts the workflow for each chat message and answers with the workflow's reply.

**Inputs**

None.

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

| Slot | Accepted credential types | Required |
|---|---|---|
| `basicAuth` | `engi.http.basic` | no |
| `headerAuth` | `engi.http.header` | no |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `path` | Path | `string` | — | no | — | always | Relative to the chat base path. Empty uses <workflow id>/<node name>. The widget is served at <path>/widget. |
| `responseMode` | Respond | `options` | `lastNode` | no | `lastNode` (When the last node finishes)<br>`responseNode` (Using a Respond to Webhook node) | always | — |
| `replyField` | Reply field | `string` | `output` | no | — | shown when `responseMode` = `lastNode` | Field of the last node's first item that holds the reply. Falls back to "text". |
| `authentication` | Authentication | `options` | `none` | no | `none` (None)<br>`basic` (Basic auth)<br>`header` (Header token) | always | The hosted widget is only available without authentication. |
| `allowedOrigins` | Allowed origins | `string` | — | no | — | always | Comma-separated sites that may call the chat from a browser and embed the widget. * allows any. Empty allows this server only. |
| `title` | Title | `string` | `Chat` | no | — | always | — |
| `welcomeMessage` | Welcome message | `string` | — | no | — | always | Shown at the top of the widget before the first message. |
| `inputPlaceholder` | Input placeholder | `string` | `Type a message` | no | — | always | — |
<!-- END GENERATED: engi.trigger.chat -->

## MCP Server Trigger

**What it does.** Turns a workflow into a Model Context Protocol server while it is active: the node lists other workflows as tools, and every `tools/call` runs one of them and returns its last node's output. The trigger starts no run of its own workflow. Full contract: [AI, MCP](../14-ai.md#1411-mcp-client-tool-and-mcp-server-trigger).

**How it behaves**
- **Route.** `POST /mcp/<path>` (Streamable HTTP, JSON replies). The path shares the webhook uniqueness rule: activation fails when another active workflow uses it. The route is outside the API's authentication; the node's own authentication applies.
- **Tools.** Each tool needs a name, a workflow ID and optionally a description and an input JSON Schema. The workflow must start with an [Execute Workflow Trigger](#execute-workflow-trigger) node; its input is one item holding the arguments.
- **Auth.** None, bearer token (`engi.http.bearer`) or header token (`engi.http.header`).

<!-- BEGIN GENERATED: engi.trigger.mcpServer -->
**Type:** `engi.trigger.mcpServer` · **Version:** 1 · **Group:** `trigger` · **Display name:** MCP Server Trigger

Exposes workflows as tools of a Model Context Protocol server while this workflow is active.

**Inputs**

None.

**Outputs**

None.

**Credentials**

| Slot | Accepted credential types | Required |
|---|---|---|
| `bearerAuth` | `engi.http.bearer` | no |
| `headerAuth` | `engi.http.header` | no |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `path` | Path | `string` | — | no | — | always | The server answers at <mcp base path>/<path>. Empty uses <workflow id>/<node name>. |
| `authentication` | Authentication | `options` | `none` | no | `none` (None)<br>`bearer` (Bearer token)<br>`header` (Header token) | always | — |
| `tools` | Tools | `fixedCollection` | — | yes | — | always | One tool per workflow. The workflow must start with an Execute Workflow Trigger node; its input item is the tool's arguments and its last node's output is the result. |
| ↳ `tools.name` | Tool name | `string` | — | yes | — | always | Letters, digits, _ and - (at most 64). |
| ↳ `tools.description` | Description | `string` | — | no | — | always | Tells the client's model when to use the tool. |
| ↳ `tools.workflowId` | Workflow ID | `string` | — | yes | — | always | — |
| ↳ `tools.inputSchema` | Input schema | `json` | — | no | — | always | JSON Schema (type object) of the arguments. Empty accepts any object. |
<!-- END GENERATED: engi.trigger.mcpServer -->

---
**← Previous** [Node Reference](README.md) · **Index** [README](README.md) · **Next →** [2. Flow](02-flow.md)
