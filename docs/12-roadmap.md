# 12. Feature Plan & Roadmap

Each phase ends with a usable product and passes `make check`. Status: ☐ todo · ◐ in progress · ☑ done.

## Phase 0: Foundation
- ☑ Repo, `CLAUDE.md` standards, paginated docs
- ☑ Go module, Makefile, `.golangci.yml`, CI workflow (lint, test -race, build)
- ☑ `internal/config` (defaults → file → env → flags, validation)
- ☑ graphify graph of the Engi repo (`graphify-out/`)

## Phase 1: Core engine (headless)
- ☑ Domain model (page 3) and workflow JSON validation
- ☑ Engine (page 4) with golden tests
- ☑ Expressions with goja (page 5)
- ☑ Node SDK (`pkg/nodesdk`) and registry
- ☑ Core nodes: Manual Trigger, Set, IF, Switch, Merge, Filter, No-Op, Code (JS), HTTP Request, Wait (time), Split Out, Aggregate, Limit, Sort (see [page 6, section 6.5](06-nodes-and-plugins.md))
- ☑ Shared outbound HTTP client adapter (timeouts, size cap, proxy, TLS) and the `internal/app` composition root
- ☑ CLI: `engi run workflow.json [--input file] [--start node] [--config file]` prints the execution result; `engi validate`; runnable `examples/`
- **Exit:** a multi-branch workflow with HTTP + IF + Merge + Code runs from the CLI, and golden tests pass.

## Phase 2: Standalone server
- ☑ Store ports + SQLite adapter + migrations (durable SQLite job queue included)
- ☑ In-process bus (`internal/adapter/bus/memory`), standalone runtime (`internal/app.Runtime`) and local lease adapter (`internal/adapter/lease/local`)
- ☑ Execution service (start, get, list, cancel, retry, sync run), worker (concurrent slots, heartbeats, lease reclaim, graceful shutdown), wait scheduler, `engi run --persist`
- ☑ REST API v1 (`internal/adapter/http/api`: workflows, versions, import/export, executions + SSE stream, credentials, catalogs, users, API keys, OAuth2 callback), OpenAPI spec `docs/api/openapi.yaml` checked against the routes, `engi serve`, `engi migrate`
- ☑ Webhook trigger + Respond to Webhook, Schedule trigger (cron/interval, timezone, missed-run policy), activation lifecycle (`internal/service/trigger`), webhook handler (`internal/adapter/http/webhook`: live, test and wait-resume routes)
- ☑ Credentials: AES-256-GCM encryption + key rotation, credential types, test, OAuth2 token handling, HTTP node auth (HTTP routes in the REST API)
- ☑ Auth: owner account (first-run setup), argon2id passwords, server-side sessions + CSRF, scoped API keys, login rate limit
- ☑ Execution history, age-based retention/pruning (`internal/service/retention`), retry failed execution (`POST /executions/{id}/retry`); max-count-per-workflow retention is a follow-up
- ☑ Wait/resume via timer and via an unguessable token (`/hook-wait/<token>`); error workflows (`settings.errorWorkflowId` plus the Error Trigger node, see engine page 4.11), set from the editor's workflow settings dialog
- ☑ Sub-workflows: Execute Workflow + Execute Workflow Trigger nodes, depth limit
- **Exit:** `engi serve` alone, create a workflow via the API, it runs on a webhook and on a schedule.

## Phase 3: Editor UI
- ☑ React + TypeScript + React Flow canvas (our own design), embedded with `go:embed`
- ☑ Node palette and search, parameter forms generated from descriptors
- ☑ Run view: per-node input/output (table/JSON), live progress via SSE
- ☑ Expression editor with autocomplete and preview (server preview `POST /expressions/preview`), pin data, partial runs, version history, tags and variables (milestone 3b)
- ☐ Leftovers: resourceLocator property UI (sticky notes and binary data preview are done; the workflow settings dialog sets the error workflow)
- ☑ Credentials UI, execution list, workflow import/export
- **Exit:** a user builds and debugs a workflow in the browser without touching JSON.

## Phase 4: Data sources & integrations
- ☑ Postgres, MySQL, Redis, MongoDB, S3/MinIO, Send Email (SMTP) and IMAP trigger, integration-tested against containers
- ☑ Kafka trigger (group-based listener on every trigger instance; per-message, batch and ordered modes; dead-letter topic; queue-depth backpressure) and producer node, integration-tested against Redpanda
- ☑ Generic OAuth2 credential: client credentials, token refresh, auth-code/PKCE with the `/oauth2/callback` and `/credentials/{id}/authorize` routes
- ☑ Slack, Microsoft Teams, Telegram, Google Sheets and Airtable (httptest-covered; Sheets signs in with OAuth2 or a service account)
- ☑ Polling triggers with cursors (IMAP, leader only) and streaming listener triggers (Kafka)
- **Exit:** the integrations above are covered by integration tests against containers.

## Phase 5: Scale-out & Kubernetes
- ☑ Postgres store + `SKIP LOCKED` queue + `LISTEN/NOTIFY` bus, passing the same port contract suites as SQLite
- ☑ Role split (`--roles`), Kubernetes Lease leader election for triggers, and a Postgres advisory-lock lease (the default outside Kubernetes)
- ☑ Blob store port with filesystem and S3 adapters, streamed binary data (webhook multipart uploads, S3 node, binary download API; no HTTP Range yet)
- ☑ Health/readiness (`/healthz`, `/readyz`, `engi healthcheck`), graceful drain, Prometheus metrics, OpenTelemetry tracing
- ◐ Helm chart (standalone by default, cluster mode on your Postgres and S3, KEDA for workers), compose and Swarm, multi-arch images (`make image PLATFORM=...`); installed and smoke-tested on a live cluster (`deploy/smoke.sh`, `make helm-check`). Image signing, SBOM and provenance are wired in `.github/workflows/release.yml` (cosign keyless, binaries with signed checksums) but the workflow has not run yet; no release has been cut
- ☐ Optional queue adapters: Redis Streams, NATS JetStream, Kafka (not built; Postgres is the cluster queue)
- **Exit:** ☑ killing a pod loses no executions, verified on a live cluster with `deploy/loadtest` (docs/11 "Load test"): 2000 to 3000 webhook executions per run across 1, 2 and 4 workers, with worker pods SIGKILLed, the trigger leader and the webhook pod deleted mid-run; every accepted call ended in exactly one successful execution (none missing, no duplicates), throughput scaled about linearly with workers (19.5, 38.6, 67.4 executions/s on a single-node Docker Desktop cluster).

## Phase 6: Any-language plugins
- ☑ Engi Plugin Protocol v1 spec ([Plugins](15-plugins.md)) + host (stdio supervisor and HTTP client)
- ☑ SDKs: Python, TypeScript, C#/.NET, Go, each with an example plugin
- ☑ Python Code node (`engi.code.python`), a fresh `python3` process per run speaking the protocol
- ☑ Plugin manifest, `engi plugin install/list/use/remove`, versions on disk, `GET /api/v1/plugins`
- ☐ Follow-ups: plugin triggers and AI sub-nodes (v2), `runner` role pool, install from an archive or URL

## Phase 7: AI
- ☑ LLM chat foundation: `port.LLM`, Anthropic / OpenAI / Azure OpenAI / Ollama adapters, tool contract, token metrics, `engi.ai.chat` ([AI](14-ai.md))
- ☑ Sub-port contract: `ai.*` port kinds and cardinality, `nodesdk.Supplier`, `Input.SubPort`, engine validation and lazy supply ([AI](14-ai.md), Sub-ports)
- ☑ AI Agent (`engi.ai.agent`: parallel tool calls, limits, steps output), AI Model sub-node, Window Memory with `port.ChatMemoryStore` (SQLite and Postgres, migration 0006), Workflow / HTTP / Code tool sub-nodes, editor sub-port wiring and agent steps view ([AI](14-ai.md#1412-ai-agent))
- ☑ RAG: `port.Embedder` (OpenAI / Azure OpenAI / Ollama), Text Splitter, Embeddings, pgvector Vector Store and Vector Search tool ([AI](14-ai.md#1410-rag-embeddings-text-splitter-and-pgvector))
- ☑ MCP: client tool `engi.ai.tool.mcp` (Streamable HTTP with SSE fallback, `engi.mcp` credential) and MCP Server trigger `engi.trigger.mcpServer` exposing workflows as tools at `/mcp/<path>` ([AI](14-ai.md#1411-mcp-client-tool-and-mcp-server-trigger)); sessions, server-initiated messages and a test listener are documented follow-ups
- ☑ Chat trigger: `engi.trigger.chat`, public `/chat/<path>` endpoint, hosted widget and embed loader, session id, CORS ([AI](14-ai.md#chat-trigger)); streaming is a documented follow-up

## Phase 8: Teams & enterprise (dropped)
Dropped on 2026-10-23. Engi stays a free tool that anyone can integrate as they need; the
owner/member model with API keys is enough. Projects, per-project roles, SSO, audit log,
external secrets and Git source control are not planned. Workflow version history already
shipped in Phase 3b.

## Concept checklist

| Concept | Engi phase |
|---|---|
| Items, paired items, expressions | 1 |
| Manual / webhook / schedule triggers | 1–2 |
| Error workflows, retries, continue-on-fail | 1–2 |
| Wait / resume | 2 |
| Sub-workflows | 2 |
| Pin data, partial executions | 1, 3 |
| Credentials + OAuth2 | 2, 4 |
| Queue mode / workers | 5 (default-capable) |
| Multi-main HA | 5 (free) |
| Community nodes | 6 (any language) |
| AI agents & tools | 7 |
| RBAC, SSO, source control | not planned (Phase 8 dropped) |

---
**← Previous** [11. Deployment](11-deployment.md) · **Index** [README](README.md) · **Next →** [13. Developer Guide](13-dev-guide.md)
