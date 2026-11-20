# Engi: progress and handoff

Engi is a self-hosted workflow automation platform written in Go. It runs as one binary
(SQLite, embedded editor) or scales out on Kubernetes or Docker with Postgres.

**Start here.** Read this file for the current state and the open work. Then read `CLAUDE.md`
(the binding rules and architecture), then `docs/README.md` as needed. For code questions use the
code graph (`graphify query`, see `CLAUDE.md`) before reading files.

Last updated: 2026-11-20. The repository is being prepared for public release.

## Current state

| Area | Status | Where |
|---|---|---|
| Phases 0 to 7 (foundation, engine, store and queue, cluster mode, data sources, editor, AI, plugins) | Done | `docs/12-roadmap.md` |
| Phase 8 (projects and RBAC, SSO, audit log, external secrets, Git sync) | Dropped | Decisions log |
| Launch hardening (see "Recent changes") | Done | `CHANGELOG.md`, `[Unreleased]` |
| Load test: no lost or duplicate executions under SIGKILLed workers | Done | `docs/11-deployment.md`, "Load test" |
| Bring your own infrastructure: Helm and Compose with your own Postgres and bucket | Done, verified on kubeadm and Docker | `docs/11-deployment.md`, "Bring your own infrastructure" |
| Community files, CI integration job (Docker) | Done | `CONTRIBUTING.md`, `SECURITY.md`, `.github/workflows/ci.yml` |
| Release workflow (signed images, SBOM, provenance, signed binaries) | Written, not yet run | `.github/workflows/release.yml` |

### What the product includes

- **Standalone:** one binary with an embedded SQLite store and durable job queue (pure Go, no cgo),
  versioned migrations, and the web editor embedded with `make build WEB=1`.
- **Cluster:** Postgres store, `SKIP LOCKED` job queue, `LISTEN/NOTIFY` event bus, advisory-lock leader
  lease (default outside Kubernetes) or Kubernetes Lease. Runtime roles `api`, `webhook`, `trigger` and
  `worker`, chosen with `engi serve --roles`.
- **Deployment:** Helm chart (standalone by default; `postgres.*` and `blobs.s3.*` for cluster mode, with KEDA
  autoscaling), Docker Compose, Docker Swarm stack, and a distroless non-root image.
- **Nodes:** triggers, flow, transform, code (JavaScript and Python), HTTP, databases, streaming, storage,
  email, chat, spreadsheets, and the AI nodes. The node reference is generated from the node descriptors
  (`make docs-nodes`). Credential types are listed in `docs/nodes/11-credentials.md`.
- **AI:** AI Agent with tool-calling, model, memory and tool sub-nodes; RAG on pgvector; Anthropic, OpenAI,
  Azure OpenAI and Ollama providers; MCP client tool and MCP server trigger; chat trigger with a widget.
- **Plugins in any language:** Engi Plugin Protocol v1 (JSON-RPC 2.0 over stdio or HTTP). SDKs for Go, Python,
  TypeScript and .NET. `engi plugin install | list | use | remove`. Spec in `docs/15-plugins.md`.
- **API and editor:** REST under `/api/v1` with RFC 9457 errors, OpenAPI 3.1 in `docs/api/openapi.yaml`,
  SSE for live executions. The editor is React and TypeScript, with a canvas, forms generated from node
  descriptors, live runs, a timeline, version history, pinned data and sticky notes.

### Recent changes (since 2026-10-24)

The user-facing wording is in `CHANGELOG.md` under `[Unreleased]`. In short:

- **Nodes and expressions:** IF, Filter and Switch convert typed literals (`12` works with a number operator);
  `$execution.resumeUrl` for Wait nodes; Code `$json` holds the first item in all-items mode; MCP client tool
  names are prefixed with the sub-node name, so duplicates no longer clash.
- **Triggers:** webhooks with `responseNode` are refused at activation when no Respond to Webhook node follows;
  armed test webhooks live in the shared store, so any webhook pod serves the test call.
- **Persistence and execution:** the runtime and the credential service share one database pool; OAuth2
  refreshes are serialised across replicas (`credentials.oauth2RefreshLockTimeout`); executions record
  `attempts`, and a worker whose run was taken over cannot overwrite the result; migrations stamp the
  injected clock; vector store re-inserts replace earlier chunks.
- **Plugins:** `log` lines carry an optional `callId`, so they appear in the node's log; a failed start includes
  the last stderr lines (`plugins.startErrorLines`).
- **Auth and config:** `auth.session.cookieSecure` accepts `always`, `auto` and `never`; `engi serve` warns about
  unknown `ENGI_*` variables (names only, never values).
- **Editor:** execution timeline and live list; workflow settings dialog (error workflow, timeout); binary
  preview; sticky notes; the chat path comes from the server (`editor.chatBasePath`).
- **Tests and refactors:** the worker-recovery test is deterministic; `aichat` uses `aikit`; `llmkit` lives at
  `internal/llmkit`, outside the adapters.

## How to work on Engi

### Commands

```bash
make build        # build ./bin/engi (add WEB=1 to embed the editor; needs Node.js 22+)
make run          # run the standalone server with a local config
make test         # go test -race ./...
make test-integration  # integration tests (build tag integration; needs Docker)
make lint         # golangci-lint v2 with .golangci.yml
make check        # fmt, vet, lint and test; run before every commit
make web-check    # editor tests and type checks (after changes under web/)
make docs-nodes   # regenerate the node reference from the descriptors
make helm-check   # render and validate the Helm chart
```

`make help` lists every target.

### Rules

- `CLAUDE.md` is binding: dependency direction (domain, service, port, adapter, composition root), constructor
  injection with no globals, config only in `internal/config` (no magic numbers or `os.Getenv` in logic),
  context first, wrapped errors, `slog` loggers passed in.
- Every behaviour change ships with a table-driven test next to the code. A bug fix ships with a regression test
  that fails before the fix. Adapters are tested against real infrastructure with the `integration` build tag.
- Docs move with code. A change to behaviour, config or the API updates the matching page in `docs/` (and
  `docs/api/openapi.yaml`, for endpoints) in the same change. User-visible changes get a line under
  `[Unreleased]` in `CHANGELOG.md`.
- Study prior art for behaviour if useful, never copy code, UI or text from other projects. Note what was matched or changed in the docs page.
- Standalone (SQLite, one process) and cluster (Postgres, several pods) must both work. Never keep state in
  process memory that another pod needs.

### Git

- One branch per phase (`phase-<N>-<slug>`) or fix (`fix/<slug>`, `docs/<slug>`), from an up-to-date `main`.
  Merge with `git merge --no-ff`.
- Conventional Commits with a scope where useful (`feat(engine): ...`). One logical change per commit.
- Code commits run `graphify update .` and stage `graphify-out/` in the same commit.
- Never commit secrets, `bin/`, local databases or `.env` files. Never push unless a maintainer asks.

### Working with parallel agents

Shared contracts land first, alone. Each agent owns its packages exclusively; shared files get add-only edits.
Migration numbers are assigned up front. Only the orchestrating session commits and runs `graphify update`, with
explicit paths.

### Resuming an interrupted session

1. `git status`. Uncommitted files come from an interrupted agent: run `make check`. If it passes, review and
   commit them; if not, finish the task that owns them.
2. Run `graphify update .` from the repository root.
3. Continue with the first item in "Open work".

## Open work

Each item below will be tracked as a GitHub issue. Check the issue tracker before you start one, and say which
item you are taking.

**Chat and MCP**

- Chat trigger streaming. Design: `Input.Stream()` publishes `EventNodeChunk` on the bus and the chat endpoint
  sends it over SSE (`docs/14-ai.md`, chat trigger section). Not in the code yet.
- MCP server trigger: a test or listen mode, sessions and server-initiated SSE, resources and prompts.
- MCP client: OAuth. It opens a session for each list or call. `ResolveMCP` loads the workflow more than once per request.

**Editor**

- `resourceLocator` property UI. It needs an endpoint that loads options.
- Sticky-note resize drag has not been tested in a real browser.

**API, triggers and operations**

- API rate limiting. Login attempts are already limited; the general limit is not built (`docs/09-api.md`).
- HTTP `Range` requests for the binary download API (`docs/09-api.md`).
- Retention by execution count per workflow. Age-based retention is built.
- Schedules: `allowOverlap`, `runAll` and `maxCatchUp` are documented but not built (`docs/08-triggers.md`).
- Switch and Merge ports are fixed by `nodes.switch.maxRules` and `nodes.merge.maxInputs`. Dynamic ports need
  descriptor support.
- Exactly-once side effects. Runs are fenced (one run per checkpoint, results written only by the current
  claim), but the nodes of a run interrupted by a dead worker run again (at least once; see `docs/02`).
  Idempotency keys per node would close that.
- Queue adapters beyond SQLite and Postgres (Redis, NATS and Kafka are candidates; the core does not change).

**Plugins**

- Plugin v2: poll, webhook and listen triggers, and AI sub-nodes. Also a `runner` role or pool; today every process
  that loads plugins starts them, so `api` pods start them too. Descriptor-only loading for non-worker roles.
- Install from an archive or a URL (today: a directory). Hot reload (today: restart the server).
- Python Code node: `str.format` can bypass the dunder check. The check is a guard, not a security boundary.
- Publish the SDKs: `@engi/plugin-sdk` to npm and `engi_plugin` to PyPI (today: vendored or `npm pack`).

**Nodes and launch**

- Google Sheets and Airtable are tested with httptest only. Run them against real accounts.
- The "Getting the details" section of `docs/nodes/11-credentials.md` (IAM actions, Slack scopes, Kafka ACLs)
  is general guidance. Verify it per provider before launch.
- Cut `v0.1.0-rc.1` to exercise the release workflow (signing, SBOM, provenance). It has not run yet.

## Decisions log

- 2026-10-23: Phase 8 (projects and RBAC, SSO, audit log, external secrets, Git sync) dropped. Keep the
  owner and member model. Do not start it again unless the maintainers ask.
- 2026-10-17: Name **Engi** (縁起). Go, clean and hexagonal architecture. Standalone single binary
  (SQLite and a durable SQLite job queue), with Kubernetes scale-out where Postgres is the only required
  dependency (SKIP LOCKED queue, LISTEN/NOTIFY bus).
- 2026-10-17: Binary size (about 56 MB without the UI) accepted. Resource needs scale with the workload; size is not a goal.
- 2026-10-17: Expressions and the JavaScript Code node run in goja. Other languages run through the plugin protocol.
- 2026-10-17: Module path `github.com/engi-hq/engi` (placeholder, can be renamed). Licensed Apache-2.0, with a `NOTICE` file.
