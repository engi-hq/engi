# Changelog

All notable changes to Engi are recorded in this file.

The format is based on [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/), and Engi
uses [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html) from its first tagged
release. Until then, every change is listed under **Unreleased**. Before 1.0.0, a minor version
may include breaking changes.

## [Unreleased]

Development started on 2026-10-17. Everything below is new since then.

### Added

- Editor: a Timeline panel for any shown execution (summary plus one bar per node run, click to open the node), and the Executions page now refreshes itself with a Live toggle.

#### Engine and expressions

- Executions record how many times a worker started them (`attempts`) and when a dead worker's run was last
  taken over (`lastRecoveredAt`), in the API and the editor timeline.

- `credentials.oauth2RefreshLockTimeout` (`ENGI_CREDENTIALS_OAUTH2_REFRESH_LOCK_TIMEOUT`, default 30s) bounds how
  long a replica waits for another replica's OAuth2 refresh.

- `$execution.resumeUrl` in expressions: an absolute link that resumes the execution's webhook Wait node, the
  same in every node, so a workflow can send it before it waits. It is a bearer link.

- `server.publicUrl` (`ENGI_SERVER_PUBLIC_URL`, Helm `publicUrl`): the absolute address of the server, used to
  build links such as `$execution.resumeUrl`.

- Workers now log the end of every execution they run (`execution finished` / `execution waiting`, with
  status, duration, nodes run and, on failure, the failing node and message), so an execution can be
  followed in the logs from `execution queued` to its result.

- Jobs reclaimed from a dead worker are now logged (job, execution, previous worker id, attempts) and
  counted in `engi_queue_jobs_reclaimed_total{outcome="requeued|failed"}`.

- Error workflows: set `settings.errorWorkflowId` on a workflow and Engi starts that workflow, from its new
  Error Trigger node, whenever an execution of the first one ends in error or crashes (not for manual runs).
- Workflow engine with merge policies, retries, per-node error handling (`onError`), loops,
  wait and resume, and partial runs. Golden tests run workflow JSON and compare the result.
- Item lineage (paired items) across nodes. Edge cases are documented on the engine and expression pages.
- Expressions: `{{ }}` templates in any field, evaluated in a sandboxed, pooled JavaScript
  runtime, with `$json`, `$("Node")`, `$node`, `$vars`, `$env` and helper functions.
- Sub-workflows: the Execute Workflow node and the Execute Workflow Trigger.
- AI sub-ports: model, memory and tool sub-nodes plug into a parent node and are supplied
  only when the parent asks for them.

#### Nodes

- Triggers: Manual, Webhook, Schedule (interval or cron, with time zones and DST), Execute
  Workflow Trigger, Kafka Trigger, IMAP Trigger, Chat Trigger and MCP Server Trigger.
- Flow: IF, Switch, Merge, Wait, Execute Workflow, Respond to Webhook and No Operation.
- Transform: Edit Fields (Set), Filter, Sort, Limit, Split Out, Aggregate and Code (JavaScript).
- Code (Python): runs Python 3 in a fresh process per run, with the same modes as the
  JavaScript Code node, `expr()` for workflow expressions and an import allow-list
  (`nodes.code.python.*`).
- HTTP Request, with authentication, JSON, form and binary bodies, and binary responses.
- Databases: Postgres and MySQL (parameterized queries, insert, update, upsert, delete and
  transaction modes), Redis and MongoDB.
- Streaming: Kafka producer, and a Kafka Trigger with consumer groups, offsets committed only
  after the execution is stored, a dead-letter topic and backpressure.
- Storage: S3 Storage for AWS S3 and MinIO (list, get, put, delete, copy and presigned URLs).
- Email: Send Email over SMTP with attachments, and the IMAP Trigger.
- Chat: Slack, Telegram and Microsoft Teams.
- Google Sheets node: append, read, update (by row number or key column), clear, create tab and list tabs, with
  header-row mapping. Sign in with OAuth2 or a Google service account key (new credential type).
- Airtable node: list (formula, view, sort, paging), get, create, update, upsert and delete, written ten records per
  request, with a new Airtable personal access token credential.
- AI: AI Chat, AI Agent (tool-calling loop with limits and a steps view), AI Model, Window
  Memory, Workflow Tool, HTTP Tool, Code Tool, MCP Client Tool, Vector Search Tool,
  Embeddings, Text Splitter and Vector Store (pgvector). Providers: Anthropic, OpenAI,
  Azure OpenAI and Ollama.

#### Plugins

- Plugins in any language: the Engi Plugin Protocol v1 (JSON-RPC 2.0 over stdio or HTTP) adds
  node types without changing the Engi binary. A plugin runs in its own process or service, is
  restarted when it crashes, and gets no Engi secrets. See the Plugins page.
- SDKs for Go, Python, TypeScript and .NET, each with an example plugin.
- `engi plugin install`, `list`, `use` and `remove` manage versioned plugins in `plugins.dir`;
  `GET /api/v1/plugins` shows which plugins loaded and why one did not.

#### Triggers and webhooks

- Workflow activation and deactivation, with activation errors recorded on the workflow.
- Schedules run on the leader only, with a missed-run policy and one run per scheduled time.
- Webhooks at `/hook`, `/hook-test` and `/hook-wait`, with body size limits, per-node
  authentication, an idempotency header and three response modes.
- A chat endpoint at `/chat/{path}` with an embeddable widget, and an MCP server endpoint at
  `/mcp/{path}` that exposes workflows as tools.

#### Persistence and execution

- Standalone mode: an embedded SQLite store and durable job queue (pure Go, no cgo), with
  versioned SQL migrations.
- Executions: start with deduplication, cancel, retry and synchronous runs. Workers claim jobs
  under a lease with heartbeats, and jobs from a worker that died go back to the queue.
- Waiting executions resume on a timer or through a secret resume token.
- Workflow version history, tags, and variables (secret variables are encrypted).
- Binary data in a blob store (local filesystem or S3), webhook file uploads and a binary
  download API.
- Retention: old executions and their files are pruned by age.

#### Credentials and security

- Credentials encrypted at rest with AES-256-GCM, using a keyring with key IDs and rotation.
  Secrets never appear in API responses, logs or execution data.
- Credential types: HTTP Basic Auth, HTTP Header Auth, Bearer Token, API Key in Query, OAuth2
  (with PKCE and token refresh), Postgres, MySQL, MongoDB, Redis, Kafka, S3, SMTP, IMAP, Slack,
  Telegram, Microsoft Teams webhook, MCP Server, Anthropic, OpenAI, Azure OpenAI and Ollama.
- Accounts: owner setup, members, argon2id passwords, sessions, scoped API keys, login rate
  limiting and CSRF protection.

#### API

- REST API under `/api/v1` with RFC 9457 problem responses, cursor pagination and a live
  execution stream over Server-Sent Events.
- An OpenAPI 3.1 spec, checked against the registered routes by a test.
- Partial runs (run up to a node, with pinned data or an unsaved workflow) and expression
  preview against a past execution.

#### Editor

- Web editor (React and TypeScript) embedded in the binary: workflows, canvas, forms generated
  from node descriptors, expression editor, live runs, input and output viewer, execution
  history, and credentials with OAuth2 sign-in.
- Version history, pinned data, tidy-up auto-layout, settings (profile, users and API keys),
  tags and variables, and AI sub-port wiring.
- Workflow settings dialog (sliders button in the editor bar, or Ctrl/Cmd+K): pick the error workflow
  from a searchable list of your workflows ("None" and "This workflow" included; it warns when the
  chosen workflow has no enabled Error Trigger node) and set the execution timeout.
- Binary data in the run view: items with files list the name, type, size and a download link, and
  preview images, audio, video and text (text up to 512 KB, shown as plain text).
- Saving a workflow with an error workflow that does not exist now fails with a validation error on
  `settings.errorWorkflowId` (an unchanged value is never re-checked).
- Sticky notes on the canvas: markdown text (headings, lists, code, links; no raw HTML, links open in a new
  tab), six colours, resizable, drawn behind nodes. Add one with the note button, the `N` key or Ctrl/Cmd+K;
  double-click to edit. They select, move, copy, paste, duplicate, delete and undo like nodes, and are kept
  in saved versions, restores, exports and imports. Notes are stored in a separate `notes` list on the
  workflow, so runs never see them; `workflow.maxNotes` (200) and `workflow.maxNoteTextBytes` (16 KiB)
  bound them.

#### Scale-out and operations

- Cluster mode on Postgres: store, `SKIP LOCKED` job queue, `LISTEN/NOTIFY` event bus and an
  advisory-lock leader lease.
- Runtime roles (`api`, `webhook`, `trigger` and `worker`), chosen with `engi serve --roles`.
- Kubernetes Lease leader election, a distroless non-root container image, a Helm chart with
  KEDA autoscaling, a Docker Swarm stack and a Docker Compose file.
- Prometheus metrics, OpenTelemetry traces (one span per run and one per node), health
  probes and opt-in profiling. About 30 MB of memory when idle.
- CLI commands: `engi serve`, `engi worker`, `engi run`, `engi validate`, `engi migrate` and
  `engi healthcheck`.

#### Deployment

- Release workflow for version tags: multi-arch container image on GHCR signed with keyless cosign, with SBOM and provenance attestations, plus release binaries (linux and macOS, amd64 and arm64) with signed SHA256 checksums. Verification steps are in the deployment guide.
- The Helm chart works out of the box: with no values it runs Engi standalone (SQLite and binary
  data on a PersistentVolume, encryption key generated once and kept). Point `postgres.*` at your
  Postgres and `blobs.s3.*` at your bucket for cluster mode; passwords and keys come from Secrets
  you already have. The chart refuses values that cannot work, with a message saying why.
- The Docker Compose file runs Engi alone and takes your Postgres and bucket from `.env`; a local
  Postgres overlay is included for trying it.
- S3 binary storage can use the cloud identity of the pod or host (IRSA, workload identity,
  instance profile) instead of access keys.
- Jobs of a worker that died are retried after about 40 seconds in Kubernetes (chart lease 30s).

#### Documentation and project

- Documentation site at <https://engi-hq.github.io/docs/>, with a quick start, a node
  reference generated from the node descriptors, an OpenAPI reference and an architecture
  diagram.
- Brand banners, images and a 10-second intro video.
- Licensed under the Apache License 2.0, with a `NOTICE` file crediting the original author.
- A plugin that fails to start reports its last stderr lines in the start error and in `GET /api/v1/plugins`
  (`plugins.startErrorLines`, default 20).
- Sticky notes: the version-conflict dialog lists note changes, tidy-up moves a note with the node it covers,
  and the editor counts note text in UTF-8 bytes against `workflow.maxNoteTextBytes`.
- `engi serve` warns about unknown `ENGI_*` environment variables (names only, never values), so a misspelled
  setting is no longer silently ignored.

### Fixed

- Activating a webhook or chat workflow whose trigger answers with a Respond to Webhook node is now refused
  with a validation error when no enabled Respond to Webhook node follows it, instead of failing every call
  with a 500 or 504.
- IF, Filter and Switch convert a typed value to the condition's type, so `12` works with a number
  operator without turning on loose type validation. A value that cannot be converted fails the item and names
  the condition. Values produced by expressions stay strictly typed unless loose type validation is on.
- The server no longer opens a second connection pool on the database for credentials: the credential service
  and the runtime share one, so `store.maxOpenConns` holds and SQLite lock contention drops.
- OAuth2 credentials no longer break in cluster mode when two replicas refresh the same token at once: each
  refresh runs under a per-credential database lock, and a replica that waited reuses the stored token.
- Test webhooks armed from the editor (`/hook-test/`) work in cluster mode: any webhook pod serves the test
  call, not only the pod that armed it. Each test still answers exactly one call.
- A worker that died after saving a Wait node's state no longer resumes the wait early on retry; the wait
  continues only when its time is due or its resume link is called.
- An execution runs once per checkpoint: a stale job reclaimed from a dead worker and a fresh job for the same
  execution can no longer both run it, and a worker whose run was taken over can no longer overwrite the result.

### Changed

- Plugin log lines written during a node run appear in that run's node log (protocol v1 `log` takes an optional
  `callId`; the Go, Python, TypeScript and .NET SDKs send it automatically). Lines outside a run still go to the server log.
- Code (JavaScript and Python), run once for all items: `$json` (`json` in Python) holds the first item's
  data instead of an empty object.
- MCP Client tool: tool names offered to the model are prefixed with the sub-node name (`<node>__<tool>`), so
  two MCP servers exposing the same tool no longer clash; duplicate names across tool sub-nodes fail the item and
  name both sub-nodes.
- Vector Store insert: re-inserting a document with an `id` replaces its earlier chunks, so a shorter document no
  longer leaves stale chunks behind.
- `auth.session.cookieSecure` accepts `always` (default, unchanged), `auto` (Secure on TLS, or on a trusted
  `X-Forwarded-Proto: https`) and `never`; `true`/`false` still work. Machine clients on plain HTTP should use an API key.
- The editor reads the chat base path from the server (`GET /me` → `editor.chatBasePath`), so "Open chat"
  follows `chat.basePath` without rebuilding the editor.
- A node named like `note:<id>` no longer collides with a sticky note on the canvas.

[Unreleased]: https://github.com/engi-hq/engi/commits/main
