# 6. Nodes & Plugins

## 6.1 Node contract (Go, `pkg/nodesdk`)

```go
// Descriptor is static metadata. It drives validation, the UI form and docs.
type Descriptor struct {
    Type        string        // "engi.http.request"
    Version     int
    DisplayName string
    Description string
    Group       Group         // Trigger | Action | Transform | Flow | AI
    Icon        string        // icon key from our own icon set
    Inputs      []PortSpec
    Outputs     []PortSpec
    Properties  []Property    // typed parameter schema
    Credentials []CredentialRef
    MergePolicy MergePolicy   // for multi-input nodes
}

type Node interface {
    Describe() Descriptor
    Execute(ctx context.Context, in Input) (Output, error)
}

// Input gives lazy, per-item access to parameters (expressions are resolved here).
type Input interface {
    Items(port int) []Item                                                // main inputs only
    SubPort(ctx context.Context, kind PortKind, itemIndex int) ([]Supplied, error) // pull ai.* sub-nodes (see below)
    Param(ctx context.Context, name string, itemIndex int) (Value, error)  // ctx: may evaluate an expression
    Credential(ctx context.Context, name string) (Credential, error)      // ctx: may call the credential store
    Blob() BlobAccess
    HTTP() HTTPClient          // shared client: timeouts, proxy and TLS from config
    Logger() *slog.Logger
    ExecutionInfo() ExecutionInfo
}

type Output struct {
    Ports [][]Item             // one slice per output port
    Wait  *Wait                // optional: pause the execution; Ports are delivered on resume
}

type Wait struct {
    Until       time.Time      // resume at this time ...
    ResumeToken string         // ... or when a resume webhook presents this token (first wins)
}
```

Trigger nodes implement one of these:

```go
type WebhookTrigger interface { Node; Webhook(params map[string]any) (WebhookSpec, error) } // the host builds the trigger item from the HTTP call
type PollTrigger    interface { Node; CheckPoll(params) (PollSpec, error); Poll(ctx, in PollInput, cursor []byte) (PollResult, error) } // IMAP; leader only (see 8.10)
type ListenTrigger  interface { Node; CheckListen(params) error; Listen(ctx, in ListenInput, emit func(ctx, ListenEvent) error) error } // Kafka, later NATS; runs on every trigger instance (see 8.9)
type ScheduleTrigger interface { Node; Schedule(params map[string]any) (ScheduleSpec, error) }
```

Nodes register through a `Registry` (`internal/node/registry`) built in `internal/app` from
constructors (`Register(func() nodesdk.Node)`). There is no global registration in `init()`.
Lookup is by `(type, version)`; version `0` selects the latest.

Notes on the contract as implemented:

- `Input.Items`, `Param` results and credentials are copies: a node may mutate them freely.
- `Param` falls back to the `Property.Default`, and returns `ErrParamMissing` for a required
  property without a value. `nodesdk.ParamString/ParamBool/ParamInt` are typed helpers.
- A node returns `*nodesdk.NodeError` to give the engine a description, HTTP status and item
  index; any other error is wrapped automatically.
- `ExecutionInfo.Attempt` is the one-based retry attempt.
- An `errorOutput` node gets one extra output port after its declared ones.
- `MergePolicy` is the only place that decides partial-input behaviour (see page 4).

Sub-ports (AI composition, see [page 14](14-ai.md)):

```go
const ( PortMain = "main"; PortAIModel = "ai.model"; PortAITool = "ai.tool"; PortAIMemory = "ai.memory"
        PortAIEmbedding = "ai.embedding"; PortAIVectorStore = "ai.vectorStore"; PortAITextSplitter = "ai.textSplitter" )
type PortSpec struct { Name string; Kind PortKind; DisplayName string
                       Required bool; MaxConnections int } // MaxConnections: sub-port inputs only, 0 = unlimited
type Supplier interface { Supply(ctx context.Context, in Input) (any, error) }  // implemented by sub-nodes
type Supplied struct { Node string; Value any }
```

A node with an `ai.*` output is a **sub-node**: it implements `Supplier`, declares no main ports (checked by
`Descriptor.Validate`) and never runs as a step. A parent declares `ai.*` inputs and calls `in.SubPort`.
`Descriptor.PortCount`, `PortOfKind` and `IsSubNode` help with the per-kind ordinals. In tests,
`nodetest.Input.Subs` stands in for the engine and `nodetest.SupplyFrom` runs a real sub-node.

## 6.2 Property schema

Property types: `string, number, boolean, options, multiOptions, json, code, dateTime,
collection, fixedCollection, resourceLocator, credentialsSelect, notice`.

Each property supports `default`, `required`, `placeholder`, `description` and
`displayOptions` (show/hide by other values). The UI renders forms from this schema alone.

## 6.3 Any-language plugins

Custom nodes can be written in any language. The plugin is an executable or container that
speaks the **Engi Plugin Protocol v1**:

| Transport | Use |
|---|---|
| stdio + JSON-RPC 2.0 (line-delimited) | Local executables; the runner manages the process lifecycle |
| HTTP/JSON | Remote plugin service (a sidecar or separate Deployment) |

Methods: `describe` → `[]Descriptor`, `execute` → `Output`, `poll`, `webhook`, `health`.
Messages mirror the Go types above. Credentials are passed decrypted only for the call
and never logged. Binary data is passed as short-lived blob URLs, never inline.

The full specification is [page 15](15-plugins.md). Thin SDKs will be published for **Python, TypeScript, C#/.NET and Go**. A plugin is installed
from a manifest (`engi-plugin.yaml`: name, version, command or image, node types). The
host enforces timeouts, memory limits and, in cluster mode, runs plugins in the `runner` pool.

## 6.4 Built-in node catalogue (v1 target)

| Group | Nodes |
|---|---|
| Triggers | Manual, Webhook, Schedule (cron/interval), Execute-by-Workflow, Error Trigger, Kafka, Postgres (LISTEN), IMAP email, RSS poll |
| Flow | IF, Switch, Merge, Loop (batches), Wait, Execute Workflow, Stop & Error, No-Op, Respond to Webhook |
| Transform | Set (edit fields), Filter, Sort, Limit, Remove Duplicates, Split Out, Aggregate, Rename Keys, Date & Time, Crypto, XML, HTML extract, Compare Datasets |
| Code | JavaScript (in-process sandbox), Python (runner) |
| Core I/O | HTTP Request, GraphQL, Send Email (SMTP), Read/Write file (blob), FTP/SFTP |
| Data sources | Postgres, MySQL, SQL Server, MongoDB, Redis, Kafka, S3, Google Sheets, Airtable, Elasticsearch |
| Messaging | Slack, Microsoft Teams, Telegram, Discord |
| AI | LLM Chat (Anthropic, OpenAI, Ollama), AI Agent (tools via `ai.tool` ports), Embeddings, Vector store, MCP client |

Parameter-level reference for every node that exists today is in the generated [Node Reference](nodes/README.md).

## 6.5 Built-in nodes as implemented (Phase 1)

Packages live under `internal/node/<name>`; type ids are `engi.<group>.<name>`. Nodes depend
only on `pkg/nodesdk` and the shared helpers in `internal/node/nodekit` (dot paths, value
coercion, typed conditions). Limits arrive through constructors, filled from `internal/config`
by `internal/app`. Parameters are resolved per item, so any value may be an expression
(`"={{ $json.x }}"`). Every output item is linked to its input item (`pairedItem`).

| Type | Parameters (names) | Behaviour and decisions |
|---|---|---|
| `engi.trigger.manual` | none | Emits the start items it was run with. |
| `engi.flow.noop` | none | Passes items through. |
| `engi.transform.set` | `mode` (manual/json), `fields` [{name,type,value}], `jsonOutput`, `keepOtherFields` (true), `dotNotation` (true) | Field types coerce (string, number, boolean, array, object; JSON text becomes arrays/objects). Dot paths create nested objects. No binary-field assignment yet. |
| `engi.flow.if` | `conditions`, `combinator` (and/or), `ignoreCase`, `looseTypes` | Outputs `true`, `false`. Typed literals (text typed into a field) convert to the operator's type (`"5"` to 5); a literal that cannot convert is an item error naming the condition and value. Expression results are strict unless `looseTypes` converts them. No conditions means true. |
| `engi.flow.switch` | `mode` (rules/expression), `rules` [{conditions, combinator}], `outputIndex`, `fallback` (none/fallback), `allMatching`, `ignoreCase`, `looseTypes` | Descriptors are static, so the node declares `nodes.switch.maxRules` rule outputs (default 8) and the fallback output right after them (index = maxRules). First matching rule wins unless `allMatching`. |
| `engi.flow.merge` | `mode` (append/combineByFields/combineByPosition/chooseBranch), `matchFields` [{field1,field2}], `joinMode` (keepMatches, keepNonMatches, keepEverything, enrichInput1, enrichInput2), `multipleMatches`, `onClash` (preferInput1/2, addSuffix), `includeUnpaired`, `chooseInput` | `MergeWaitAll`: runs when every connected input has delivered (so a branch that produces no items stalls it, same as the engine rule in page 4). Declares `nodes.merge.maxInputs` inputs (default 2); combine-by-fields joins inputs 1 and 2. Join keys compare by JSON value, type-sensitive (`"1"` does not match 1); items missing a key never match. Shallow merge only (no deep merge yet). |
| `engi.transform.filter` | conditions as IF, `keepDiscarded` | Outputs `kept` and `discarded` (empty unless `keepDiscarded`). |
| `engi.transform.limit` | `maxItems` (1), `keep` (first/last) | |
| `engi.transform.sort` | `keys` [{field, order, type, ignoreCase}] | Stable, multi-key. Types: auto, string, number, dateTime. Missing or null values sort last in both directions. |
| `engi.transform.splitOut` | `fields` (comma list), `destinationField`, `include` (none/all/selected), `fieldsToInclude` | Several fields zip by position. Object elements of a single field spread into the item unless `destinationField` is set. A non-array counts as a one-element array; a missing field is an error. |
| `engi.transform.aggregate` | `mode` (individualFields/allItemData), `fields` [{field,outputName}], `mergeLists`, `keepMissing`, `destinationField` (data), `include` (all/selected/except), `fieldList` | Always one output item. |
| `engi.flow.wait` | `resume` (interval/specificTime/webhook), `amount`, `unit`, `dateTime` | Returns `Output.Wait{Until}`; a time already passed continues at once. `engi run` sleeps until due and resumes. The `webhook` mode sets a resume token `"<executionId>.<secret>"` (`nodesdk.NewResumeToken`), where the secret is the execution's resume secret (`nodesdk.ResumeSecreter` on the Input). The engine draws it once, 256 random bits from `crypto/rand` (`nodesdk.NewResumeSecret`), when the execution starts, so every webhook Wait node of one execution shares it and `$execution.resumeUrl` can be sent before the node runs. A node without that capability cannot ask for a resume token. The token is saved with the waiting state and compared in constant time by `waiter.ResumeByToken`; it is served by `/hook-wait/<token>` and handed to authorized callers by `waiter.PendingToken`. The items of the resume call become the node's output. |
| `engi.code.javascript` | `mode` (runOnceForAllItems/runOnceForEachItem), `jsCode` | goja sandbox (no host objects, frozen built-ins, fresh runtime per run). Limits: `nodes.code.*`. Scope: `$json`, `$binary`, `$input` (all/first/last/item), `$itemIndex`, `$runIndex`, `$execution`, `$workflow`, `$now`, `$today`, `console.log`. Workflow scope: `$("Node")` (`item`, `all()`, `first()`, `last()`, `isExecuted`), `$node["Name"]`, `$vars`, `$env` (allow-list `expression.envAllowList`) and `$fn`. Each access goes through `Input.EvalExpression`, so the code sees exactly what an expression in the same node sees (same lineage rules, same env allow-list); lookups are lazy and a failed lookup throws a JavaScript error. Return items as `{json, binary}` or plain objects; per-item mode may return one object, an array, or null to drop. |
| `engi.code.python` | `mode` (runOnceForAllItems/runOnceForEachItem), `pythonCode` | A fresh `nodes.code.python.interpreter` process per run (`-I`, empty environment) driven by `internal/adapter/plugin/pyrunner` over the plugin protocol; the node itself only knows the `codepython.Runner` port. Code is parsed first and refused for imports outside `nodes.code.python.allowedModules` (an entry allows the module and its submodules), relative imports, `eval`/`exec`/`open`/`getattr`-style names and dunder attributes. Scope: `items`, `item`, `json`, `item_index`, `run_index`, `execution`, `workflow`, `expr(expression, item_index)` (via `Input.EvalExpression`), `print` (to the log). Same return shapes as the JavaScript node. Limits: `nodes.code.timeout` (process killed), `nodes.code.maxOutputBytes`. A guard against mistakes, not a security boundary; see [15.8](15-plugins.md). |
| `engi.flow.executeWorkflow` | `workflowId`, `waitForResult` (true) | Starts a child execution of another workflow (mode `subworkflow`, `parentExecutionId` set) with all input items, through a `nodesdk.SubworkflowRunner` injected at construction. The child must contain an Execute Workflow Trigger; the call fails if it does not. With `waitForResult` the child runs **inline in the parent's worker slot** (no queue hop, so parents waiting on children cannot starve the pool) and the node outputs the first output of the child's last finished node; a child that does not end in `success` fails the node with the child's error. Without it the child is queued like any execution and the input items continue unchanged. Nesting deeper than `engine.maxSubworkflowDepth` fails the node (`ExecutionInfo.Depth`). |
| `engi.trigger.executeWorkflow` | none | Start node of sub-workflows: emits the items the calling node passed in. |
| `engi.trigger.webhook` | `httpMethod` (GET), `path` (default `<workflowId>/<node slug>`; `:name` segments capture), `responseMode` (immediately/lastNode/responseNode), `authentication` (none/basic/header; credentials `basicAuth` = `engi.http.basic`, `headerAuth` = `engi.http.header`), `responseCode`, `responseBody`, `responseData` (firstEntryJson/allEntries/noData), `rawBody` | Declares a route through `nodesdk.WebhookTrigger`; the trigger service registers it on activation and the webhook handler builds the item `{headers, params, query, body}` (Authorization, Cookie and the auth header are not kept). See [page 8](08-triggers.md). |
| `engi.flow.respondToWebhook` | `respondWith` (firstItem/allItems/text/json/noData), `responseBody`, `responseCode` (200), `headers` [{name,value}] | Sets `Output.Response`; the worker publishes it with the node run on the bus and the held webhook call answers with it. Only the first response of an execution is used. Items pass through, so the workflow continues after the reply. |
| `engi.trigger.kafka` | `topics` (comma list), `groupId` (default `engi-<workflowId>-<node name>`), `startOffset` (latest/earliest), `mode` (perMessage/batch/ordered), `batchSize`, `batchWaitMs` (batch, capped by `kafka.batchMaxMessages` / `kafka.batchMaxWait`), `valueFormat` (json/text), `deadLetterTopic` | Implements `nodesdk.ListenTrigger`; runs on every trigger instance and Kafka balances the partitions. Item: `{topic, partition, offset, timestamp, key, value, headers}`; a value that fails to decode goes to the dead-letter topic and is skipped, or without one is kept as raw text with `valueError`. Credential `engi.kafka`. Parameters are static (no expressions). See [8.9](08-triggers.md#89-kafka-trigger-and-producer-as-built). |
| `engi.kafka.produce` | `topic`, `key`, `value`, `format` (json/text), `headers` (object), `acks` (all/leader/none), `compression` (none/gzip/snappy/lz4/zstd), `idempotent` (default true; needs `acks` all) | One record per input item, all validated before the first is sent. Shared producer per credential version and option set (`internal/adapter/kafkapool`), flushed and closed on shutdown. Output per item: `{topic, partition, offset, timestamp}`. Records over `nodes.kafka.maxRecordBytes` are refused. Credential `engi.kafka`. |
| `engi.email.send` | `from` (default: credential's `fromAddress`), `to`, `cc`, `bcc`, `replyTo` (comma list or array), `subject`, `text`, `html`, `attachments` (comma list of binary field names) | One message per input item, all rendered and validated before the connection opens, then sent over **one SMTP connection** for the whole run. Text and HTML together become `multipart/alternative`; attachments wrap it in `multipart/mixed`. Header text is RFC 2047 encoded, and any CR/LF in an address, subject, name or type is refused (header injection). Bcc only enters the envelope. Output per item: `{messageId, from, accepted, rejected}`; a refused recipient is listed in `rejected`, the item fails only if every recipient is refused. Limits `nodes.email.*`. Credential `engi.smtp`. Uses the stdlib `net/smtp`. |
| `engi.trigger.imap` | `mailbox` (INBOX), `intervalSeconds` (60), `unseenOnly` (true), `from`, `subject` (contains, case-insensitive), `afterProcessing` (nothing/markRead/move), `moveTo`, `emitExisting` (false) | `nodesdk.PollTrigger`, leader only. One execution per mail; item `{uid, uidValidity, messageId, from, to, cc, replyTo, subject, date, text, html, headers, attachments, size}`. `attachments` lists name, type and size only (TODO: store bytes as binary fields once pollers get blob access). Credential `engi.imap`. Details in [8.10](08-triggers.md#810-polling-triggers-and-the-imap-trigger-as-built). |
| `engi.storage.s3` | `operation` (listBuckets/list/get/put/delete/copy/presign), `bucket`, `key`; list: `prefix`, `delimiter`, `maxKeys`; get: `as` (text/json/binary), `binaryField`; put: `source` (text/json/binary), `value`, `binaryField`, `contentType`; copy: `destBucket`, `destKey`; presign: `method` (get/put), `expiresIn` seconds | Works with AWS S3 and S3-compatible stores (MinIO, R2, ...) on `aws-sdk-go-v2` (Apache-2.0; chosen over minio-go for first-class SigV4 presigning, AWS credential semantics and the same API against every vendor). One request per input item, output paired to it. List returns one item per object (`key,size,etag,lastModified,storageClass`) then one per common prefix (`prefix`), at most `min(maxKeys, nodes.s3.maxKeys)` objects. Get as text/json puts the body in `data` and refuses objects over `nodes.s3.maxObjectBytes`; as binary it streams into the blob store under `binaryField`. Put buffers the body (cap `nodes.s3.maxUploadBytes`) so it can be signed. Presign refuses `expiresIn` above `nodes.s3.presignMaxExpiry`. Shared client per credential version (`internal/adapter/s3pool`). Credential `engi.s3`. |
| `engi.chat.slack` | `operation` (postMessage/updateMessage/deleteMessage/uploadFile/listChannels/getUserByEmail), `channel`, `text`, `blocks`, `threadTs`, `ts`, `content`, `filename`, `email` | Credential `engi.slack` (bot token). Plain HTTP over the shared client, no SDK. Slack's `ok:false` envelope becomes an error carrying Slack's code. A 429 waits `Retry-After` (context-aware), capped by `nodes.chat.maxRetryWait` and `maxRetries`. File upload uses Slack's reserve, send, complete flow. Channel listing follows cursors up to `nodes.chat.maxListResults` and reports `truncated`. |
| `engi.chat.telegram` | `operation` (sendMessage/editMessage/sendDocument/getChat), `chatId`, `text`, `parseMode`, `disablePreview`, `replyTo`, `messageId`, `documentUrl` | Credential `engi.telegram`; the token is placed in the request path only when the credential is applied and is scrubbed from every error. Base URL from `nodes.chat.telegramBaseUrl`. `ok:false` becomes an error with Telegram's description; 429 handled as for Slack. |
| `engi.chat.teams` | `mode` (webhook/graph), `text`, `card`, `teamId`, `channelId`, `contentType` | Webhook mode (credential `engi.teamsWebhook`, the URL is the secret) posts a MessageCard, or wraps an Adaptive Card in a message attachment. Graph mode posts a channel message with an `engi.oauth2` credential. Graph is kept to that one operation. |
| `engi.google.sheets` | `operation` (appendRows/readRows/updateRows/clearRange/createSheet/listSheets), `authentication` (oauth2/serviceAccount), `spreadsheetId`, `sheet`, `headerRow`, `range`, `columns`, `matchBy`/`matchColumn`/`matchValue`/`rowNumber`, `ifNotFound` | Credential `engi.oauth2` (Sheets scope) or `engi.googleServiceAccount` (JWT exchanged for a one-hour token per run). Sheets API v4 over the shared HTTP client, no SDK. Append sends all items in one request; header row maps item keys to columns; reads are capped by `nodes.google.maxReadRows`; 429 handled as for Slack with `nodes.google.*`. |
| `engi.airtable` | `operation` (listRecords/getRecord/createRecords/updateRecords/upsertRecords/deleteRecords), `baseId`, `table`, `recordId`, `fields`, `typecast`, `mergeOn`, `filterByFormula`, `view`, `returnFields`, `sort`, `limit` | Credential `engi.airtable` (personal access token, Bearer). Writes are batched `nodes.airtable.batchSize` (max 10) records per request; list follows `offset` up to `nodes.airtable.maxListResults`; 429 handled as for Slack with `nodes.airtable.*`. |
| `engi.trigger.schedule` | `rule` (interval/cron), `interval`, `unit` (seconds..weeks), `cronExpression` (5 or 6 fields), `timezone` (`schedule.defaultTimezone`) | Declares a `nodesdk.ScheduleSpec`; the trigger service fires it on the leader. The item is `{timestamp, timezone}` of the scheduled time. Cron parsing is `internal/cronexpr` (own implementation, DST-aware). |
| `engi.http.request` | `method`, `url`, `query`, `headers`, `bodyType` (none/json/form/raw), `jsonBody`, `formFields`, `rawBody`, `rawContentType`, `responseFormat` (auto/json/text/base64), `fullResponse`, `failOnError` (true), `followRedirects` (true), `timeoutMs` | One request per input item through `Input.HTTP()`. An object response is one item, an array becomes one item per element, anything else is `{data}`. `fullResponse` gives `{statusCode, statusMessage, headers, body}`. Non-2xx is a `NodeError` with `HTTPStatus` and a truncated body unless `failOnError` is off. Redirects are switched off per request through `nodesdk.WithHTTPCallOptions`. TODO(phase 2): credentials-based authentication, pagination, binary through the blob store. |
| `engi.db.postgres` | `schema` (public), `operation` (executeQuery/select/insert/update/upsert/delete), `table`, `query` + `queryParams` (array bound to placeholders), `columns`, `where` [{column, operator, value}] + `combinator`, `sortColumn`/`sortDirection`, `limit`, `dataMode` (autoMap/define) + `values`, `matchColumns`, `runMode` (perItem/once), `transaction` (none/all/perItem), `numbersAsString` | Pooled connections from `port.DBPool` (see [7.5](07-data-sources.md#75-connection-pooling-database-data-sources)). Values are always bound parameters; table and column names are quoted identifiers, never raw text. Execute Query decides row-returning vs not by the leading keyword or a `RETURNING` clause; others report `{affectedRows}`. Select returns rows; Delete requires at least one condition. `runMode: once` (query/select only) runs a single time with the first item. `transaction: all` is one transaction for the whole run and rolls back on the first error; `perItem` commits each item separately. Timestamps are RFC 3339 UTC; JSON columns become objects; `numbersAsString` returns BIGINT/NUMERIC as strings. Insert always writes one row per item, with an explicit per-item error that carries the item index. Insert/Update/Upsert use `RETURNING *` and output the written rows; Upsert is `ON CONFLICT (matchColumns) DO UPDATE`. Credential `engi.postgres`. |
| `engi.db.redis` | `operation` (get/set/delete/incr/decr/keys/hashGet/hashSet/listPush/listPop/publish), `key`, `value`, `ttl` (seconds, set), `amount` (incr/decr), `pattern` (keys), `field` (hashGet; empty returns the whole hash), `fields` (hashSet object), `side` (left/right, list ops), `channel` + `message` (publish) | Shared client from the Redis manager (see [7.5](07-data-sources.md#75-connection-pooling-database-data-sources)); one command per input item, all items validated before the first runs. Non-string values are stored as JSON text. A missing key or empty list returns `value: null`. `keys` uses `SCAN` and returns `{pattern, keys, truncated}`, capped by `nodes.database.redisMaxKeys`. Outputs: get/listPop `{key, value}`, set `{key, ok}`, delete `{key, deleted}`, incr/decr `{key, value}`, listPush `{key, length}`, hashSet `{key, fieldsSet}`, publish `{channel, receivers}`. Credential `engi.redis` (host, port, db, username, password*, tls). |
| `engi.db.mongodb` | `operation` (find/findOneAndUpdate/insert/update/delete/aggregate), `collection`, `filter`, `projection`, `sort`, `limit` (find), `update` (operators or pipeline), `upsert`, `returnDocument` (after/before), `multiple` (update/delete: all matches, default first only), `documents` (insert; empty inserts the input item), `pipeline` | Shared client from the MongoDB manager. JSON parameters may be text (key order kept, so `sort` and pipeline stages are exact) or objects, and accept extended JSON such as `{"$oid": "..."}`; output documents use relaxed extended JSON, so `_id` is `{"$oid": "..."}` and can be fed back into a filter. Update and Delete refuse an empty filter. Find and Aggregate are capped by `nodes.database.mongoMaxResults` (aggregate appends a `$limit` stage). Find/aggregate yield one item per document; findOneAndUpdate yields the document or nothing; writes yield counts (`insertedCount/insertedIds`, `matchedCount/modifiedCount/upsertedCount/upsertedId`, `deletedCount`). Database comes from the credential. Credential `engi.mongodb` (connectionString*, host, port, user, password*, database, tls). |
| `engi.db.mysql` | `operation` (executeQuery/select/insert/update/upsert/delete), `table`, `query` + `queryParams` (array bound to placeholders), `columns`, `where` [{column, operator, value}] + `combinator`, `sortColumn`/`sortDirection`, `limit`, `dataMode` (autoMap/define) + `values`, `matchColumns`, `runMode` (perItem/once), `transaction` (none/all/perItem), `numbersAsString` | Pooled connections from `port.DBPool` (see [7.5](07-data-sources.md#75-connection-pooling-database-data-sources)). Values are always bound parameters; table and column names are quoted identifiers, never raw text. Execute Query decides row-returning vs not by the leading keyword or a `RETURNING` clause; others report `{affectedRows}`. Select returns rows; Delete requires at least one condition. `runMode: once` (query/select only) runs a single time with the first item. `transaction: all` is one transaction for the whole run and rolls back on the first error; `perItem` commits each item separately. Timestamps are RFC 3339 UTC; JSON columns become objects; `numbersAsString` returns BIGINT/NUMERIC as strings. Insert always writes one row per item, with an explicit per-item error that carries the item index. Writes output `{affectedRows, lastInsertId}`; Upsert is `ON DUPLICATE KEY UPDATE` (the match columns document intent; the colliding unique key decides). `ilike` maps to `LIKE`. Credential `engi.mysql` (host, port, database, user, password*, tlsMode, caCertificate*). |

The shared client (`internal/adapter/httpclient`) applies `httpClient.*` from config: request
timeout, response size cap (reading past it fails), proxy, TLS settings and a default
`User-Agent`.

---
**← Previous** [5. Expressions](05-expressions.md) · **Index** [README](README.md) · **Next →** [7. Data Sources & Credentials](07-data-sources.md)
