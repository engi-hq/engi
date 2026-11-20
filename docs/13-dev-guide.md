# 13. Developer Guide

Read `CLAUDE.md` first: it is the binding rulebook. This page is the practical companion.

## 13.1 Repository layout

```
cmd/engi/            main: CLI dispatch (run, validate, serve, migrate, plugin, healthcheck, version)
internal/config/     typed config
internal/domain/     entities, value objects, domain errors (stdlib only)
internal/port/       interfaces: Store, Queue, Bus, BlobStore, LeaseProvider, Clock, Cipher
internal/service/    engine, expression, workflow, execution, credential, trigger
internal/node/       built-in nodes, one package per node
internal/adapter/    store/{sqlite,postgres}  queue/*  bus/*  blob/*  lease/*  http/*  plugin/
internal/app/        composition root, builds the object graph per role
pkg/nodesdk/         public SDK for node authors
migrations/          {sqlite,postgres}/NNNN_name.sql
web/                 editor UI (React + TS)
deploy/              helm/engi, compose
docs/                these pages; api/openapi.yaml; website/ (docs site config)
```

Full rationale and dependency rules: [page 2](02-architecture.md#21-code-architecture-clean--hexagonal).

### CLI commands

`cmd/engi/cli.go` dispatches these subcommands. There is no separate `worker` command: workers are a role of `serve`.

| Command | Does |
|---|---|
| `engi serve [--roles api,webhook,trigger,worker]` | Run the server: REST API, webhooks, triggers, workers and probes. Needs the credential key (`ENGI_ENCRYPTION_KEYS`). |
| `engi run <workflow.json>` | Run one workflow and print the execution result as JSON. `--persist` stores it in the SQLite database and runs it through the durable queue. |
| `engi validate <workflow.json>` | Check a workflow against the registered node types. |
| `engi migrate up\|down` | Apply all pending database migrations, or revert the latest one. |
| `engi plugin install\|list\|use\|remove` | Manage any-language plugins in `plugins.dir`. |
| `engi healthcheck [/path]` | Exit 0 when the local server answers 2xx on `/path` (default `/readyz`). Used by container HEALTHCHECKs. |
| `engi version` | Print the version. |

**Roles** select what a `serve` process runs (`--roles`, or `ENGI_ROLES`):

| Role | Runs |
|---|---|
| `api` | REST API and editor |
| `webhook` | Incoming webhook endpoints |
| `trigger` | Schedule and polling triggers |
| `worker` | Queue consumers that execute workflows |

With no roles set, one process runs all four (standalone mode). In cluster mode, run separate deployments with one or a few roles each, all sharing the Postgres store and queue.

## 13.2 Prerequisites

| Tool | Version |
|---|---|
| Go | as pinned in `go.mod` |
| Docker | for `integration` tests (testcontainers-go) |
| golangci-lint | config in `.golangci.yml` |
| Node.js + npm | Node.js 22 or newer (CI uses 24), only for `web/` |
| Python 3.9+ | only for the docs site (`make docs-site`) |
| graphify | code graph CLI (section 13.8) |

## 13.3 Make targets

| Target | Does |
|---|---|
| `make build` | Build `./bin/engi` |
| `make test` | `go test -race ./...` |
| `make lint` | `golangci-lint run` |
| `make check` | fmt + vet + lint + test; **required before declaring done** |
| `make run` | Build, then run `engi serve` with `engi.yaml` when that file exists (gitignored, local), otherwise with defaults. Fails with a hint if no credential key is set and there is no `engi.yaml` |
| `make web` | `npm ci` + build the editor into `web/dist`, then copy it to `internal/adapter/http/ui/dist` for `go:embed` |
| `make web-check` | Editor lint, typecheck and tests |
| `make build WEB=1` | Like `make build`, but builds the editor first and embeds it (`-tags webui`) |
| `make docs-site` | Build the docs website into `site/` (strict: broken links fail) |
| `make docs-serve` | Preview the docs website at <http://127.0.0.1:8000> with live reload |

| `make test-integration` | `go test -race -tags integration ./...`; needs Docker for testcontainers-go |
| `make docs-check` | Fail if `docs/nodes` is stale or a node lacks a section |

## 13.4 Testing strategy

| Level | Where | Notes |
|---|---|---|
| Unit | `_test.go` beside code | Table-driven; domain and services use **fakes of ports**, never real infrastructure |
| Golden engine | `internal/service/engine/testdata/<case>/` | `workflow.json` + `input.json` → `expected.json`; case list in [page 4](04-engine.md#49-golden-tests) |
| Contract | `internal/port/<port>test/` | One suite per port, run against **every** adapter (SQLite and Postgres store, memory and Postgres queue...). A new adapter must pass it unchanged |
| Integration | build tag `integration` | Real Postgres/Redis/NATS/Kafka via testcontainers-go. `make test-integration` |
| API | handler tests + OpenAPI conformance | Spec in `docs/api/openapi.yaml` |
| Memory guard | build tag `perf` | `go test -tags perf -run TestMemoryStaysBounded ./internal/app` boots the server with production argon2 settings and asserts retained heap after a login and 100 webhook runs |
| Race | always | `-race` is on by default in `make test` |

Bug fixes ship with a regression test. No sleeps for synchronisation: inject the `Clock`.

## 13.5 Adding a node

1. Create `internal/node/<group>/<name>/` (one package per node). Depend only on `pkg/nodesdk`.
2. Implement `Describe() Descriptor`: type `engi.<group>.<name>`, version, ports, properties, credential refs. The UI form is generated from this ([page 10](10-editor-ui.md)).
3. Implement `Execute(ctx, in)`. Read parameters via `in.Param`, use `in.HTTP()` / `in.Blob()` / `in.Logger()`; no globals, no direct env or clock.
4. If it needs auth, add a credential type with schema and `Test` ([page 7](07-data-sources.md)).
5. Register it in the registry built in `internal/app` (explicit, no `init()`).
6. Tests: table-driven unit tests with fake `Input`; for I/O nodes an `integration` test; if it affects flow semantics, a golden case.
7. Update docs: [page 6 catalogue](06-nodes-and-plugins.md#64-built-in-node-catalogue-v1-target) and the roadmap checkbox.

Trigger nodes additionally implement one trigger interface ([page 8](08-triggers.md)).

## 13.6 Adding an adapter

1. Confirm the port in `internal/port` fits; change the port only with a reviewed reason.
2. Create `internal/adapter/<kind>/<impl>/`. Import domain, ports, service only; never another adapter.
3. Take config as a narrowed struct from `internal/config`; add keys, defaults and validation there.
4. Run the port's contract suite against it (integration tag if it needs a container).
5. Wire it by config value in `internal/app`; the core must not change.
6. Document the config keys in [page 11](11-deployment.md) and the mode table in [page 2](02-architecture.md).

## 13.7 Docs rules

- Docs live in `docs/`, one topic per numbered page, listed in `docs/README.md`.
- Every page ends with the nav footer: `---` then `**← Previous** [..] · **Index** [README](README.md) · **Next →** [..]`.
- Docs move with code: behaviour, config or API changes update the matching page (and `openapi.yaml`) in the same change.
- Keep pages concise: tables and decisions over prose; no marketing wording.
- Config defaults are documented in [page 11](11-deployment.md); do not duplicate them elsewhere.
- A new page is also added to `nav` in `docs/website/mkdocs.yml`.

### Docs website

`docs/` is published to <https://engi-hq.github.io/docs/> with MkDocs Material. The pages are
used unchanged; `docs/website/` (excluded from the published pages) holds only the config, a build hook and the theme overrides.

- `make docs-site` builds into `site/` (git-ignored) in a local virtualenv (`.venv-docs/`), with
  the versions pinned in `docs/website/requirements.txt`. The build is strict: a broken link or a
  page missing from `nav` fails it.
- The hook in `docs/website/hooks.py` drops the nav footer, because the theme renders its own
  previous/next links. Keep writing the footer: it is what readers on GitHub use.
- Heading IDs use GitHub's slug rules, so `page.md#41-some-heading` links work in both places.
- `docs/api/reference.html` renders `openapi.yaml` with Redoc.
- `docs/website/overrides/main.html` adds Open Graph tags, so shared links preview with
  `docs/assets/brand/engi-social-banner.png`. Brand images and the intro video live in `docs/assets/brand/`.
- `.github/workflows/docs.yml` builds the site in strict mode on every pull request and push to `main`
  that touches `docs/`, so a broken page fails CI.
- Publishing lives in the [engi-hq/docs](https://github.com/engi-hq/docs) repository: its workflow
  checks out this repository, builds the same pages and deploys them to GitHub Pages. It runs on its
  own schedule and when started by hand. If the `DOCS_DISPATCH_TOKEN` secret is set here (a
  fine-grained token with *Actions: write* on `engi-hq/docs`), a docs change on `main` triggers it at once.

## 13.7a Profiling memory

1. Start the server with `ENGI_RUNTIME_PPROF_ADDR=127.0.0.1:6060` (off by default, separate from the public listener).
2. `curl 127.0.0.1:6060/debug/memstats` gives `heapInuse`, `heapIdle`, `heapReleased`, `sys` (add `?gc=1` to collect first). RSS is roughly `heapInuse + heapIdle - heapReleased` plus stacks and runtime overhead.
3. `go tool pprof -sample_index=alloc_space -top bin/engi http://127.0.0.1:6060/debug/pprof/allocs` finds big allocations (this is how the startup argon2 hash was found); `inuse_space` on `/heap?gc=1` shows what is live.
4. Garbage that the GC has not returned to the OS shows as `heapIdle - heapReleased`. `runtime.memoryLimitMib` (or `GOMEMLIMIT`) makes the runtime return it sooner. On macOS `ps` RSS keeps freed pages (MADV_FREE) until memory pressure, so judge by `heapReleased` there.

## 13.7b Observability

Metrics: set `ENGI_METRICS_ENABLED=true` and scrape `http://localhost:8080/metrics` (or `ENGI_METRICS_LISTEN=127.0.0.1:9090`
for a separate listener). Prometheus: `scrape_configs: [{job_name: engi, static_configs: [{targets: ["localhost:8080"]}]}]`.
Tracing with Jaeger: `docker run --rm -p 16686:16686 -p 4318:4318 jaegertracing/all-in-one`, then start Engi with
`ENGI_TRACING_ENABLED=true ENGI_TRACING_INSECURE=true` (endpoint defaults to `localhost:4318`) and open
`http://localhost:16686`. Tempo works the same through its OTLP/HTTP receiver. Send a `traceparent` header to a webhook to see
the execution join your trace. Instrumentation goes through `port.Metrics` / `port.Tracer`; never import Prometheus or
OpenTelemetry in a service, and never put item data or credentials in a label or span attribute.

## 13.8 Code graph (graphify)

From the repo root, before reading files:

```bash
graphify query "where are webhooks registered?"
graphify explain "Engine"
graphify path "WebhookTrigger" "Queue"
graphify update .          # after changing code
```

Study prior art for behaviour if useful, but never copy it.
Clean-room rule: never copy code, node implementations, UI, icons, text or tests from any other project, including source-available ones. Describe the behaviour in your own words, then implement.

## 13.9 AI agent workflow

- Coding subagents use `sonnet` (or `haiku` for small mechanical edits). No Opus subagents.
- One bounded task per subagent, with the relevant doc pages and acceptance criteria: build passes, tests pass, lint clean.
- Don't hardcode values; add config keys instead. Don't edit another agent's files concurrently.
- Run `make check` and report its real output before declaring done.

## 13.10 Commits and PRs

- [Conventional Commits](https://www.conventionalcommits.org): `feat(engine): ...`, `fix(store/sqlite): ...`, `docs: ...`, `refactor`, `test`, `chore`, `ci`. Scope is a package or area. Breaking changes use `!` and a `BREAKING CHANGE:` footer.
- Small, single-purpose commits; subject in imperative mood, under 72 characters.
- A PR description states what and why, links the doc pages changed, and shows `make check` passing. Code, tests and docs land together.
- Never commit secrets, keys or generated binaries.
- A change users can notice also adds a line to `CHANGELOG.md` (section 13.12).

## 13.11 Editor (web/) workflow

Needs Node.js 22 or newer (CI uses 24) and npm. Nothing in `web/` is required to build the Go code: without `-tags webui` the binary embeds a one-page placeholder, so `go build ./...` and `make check` work on a machine without Node. `web/dist` and `internal/adapter/http/ui/dist` are build output and git-ignored.

**Develop with hot reload**

```
ENGI_AUTH_SESSION_COOKIE_SECURE=false engi serve        # terminal 1 (needs the usual encryption key env)
cd web && npm ci && npm run dev                          # terminal 2: http://localhost:5173
```

Vite proxies `/api`, `/oauth2`, `/hook`, `/hook-test`, `/hook-wait`, `/healthz` and `/readyz` to `http://localhost:8080`; point it elsewhere with `ENGI_URL=http://host:port npm run dev`. The browser talks to Vite only, so cookies and CSRF behave as in production (same origin). On a fresh database the app opens the setup screen.

**Checks** (all run in CI): `npm run lint`, `npm run typecheck`, `npm test` (Vitest + Testing Library), `npm run build`.

**API types** are generated from the spec: after changing `docs/api/openapi.yaml` run `npm run gen:api` and commit `web/src/api/schema.d.ts`. Descriptor shapes the spec leaves open are refined by hand in `web/src/api/types.ts`.

**Layout**

```
web/src/api/        fetch wrapper (CSRF, problem+json, pagination), TanStack hooks, SSE
web/src/forms/      descriptor-driven form renderer, displayOptions, expression editor and preview
web/src/editor/     workflow model (pure functions), history, canvas, palette, node panel, run state
web/src/pages/      routes: workflows, editor, executions, credentials
web/src/ui/         design-system pieces (dialog, toast, switch, icons); tokens in src/styles
web/src/i18n/       message catalogue (English)
```

Rules: no hardcoded API URLs (`src/config.ts`), no node-specific code (forms come from descriptors), user-visible text goes in `src/i18n/en.ts`, workflow edits are pure functions in `editor/model.ts` with tests, and a dependency must be MIT/BSD/Apache and pinned exactly.

**Production build**: `make build WEB=1` (CI does this). The handler in `internal/adapter/http/ui` serves hashed `/assets/*` with `Cache-Control: immutable`, `index.html` with `no-cache`, falls back to `index.html` for client-side routes (never for missing files), gzips on demand and sends the CSP from `server.ui.contentSecurityPolicy`.


## 13.12 Changelog and releases

`CHANGELOG.md` at the repository root follows [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html); before 1.0.0 a minor
version may break compatibility.

**Every change users can notice** adds a line under `## [Unreleased]`, in the same commit as the code:

| Commit type | Changelog section |
|---|---|
| `feat` | Added (or Changed, if it alters existing behaviour) |
| `fix` | Fixed |
| `perf` | Changed |
| `!` / `BREAKING CHANGE:` | Changed or Removed, starting with **Breaking:** and saying what to do |
| security fix | Security |
| deprecation | Deprecated |
| `refactor`, `test`, `chore`, `ci`, docs typos | nothing |

Write each line for someone running Engi: what changed and what it means for them, not the commit
subject. Group lines under a short area heading (`#### Nodes`, `#### API`) when a section grows long.

**Cutting a release** (`vX.Y.Z`):

1. Pick the version: breaking change → minor before 1.0.0 (major after); new feature → minor; fixes only → patch.
2. In `CHANGELOG.md`, rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD` and add a new, empty
   `## [Unreleased]` above it.
3. Update the link references at the bottom:
   `[Unreleased]: https://github.com/engi-hq/engi/compare/vX.Y.Z...HEAD` and
   `[X.Y.Z]: https://github.com/engi-hq/engi/compare/vPREVIOUS...vX.Y.Z` (the first release links to
   `https://github.com/engi-hq/engi/releases/tag/vX.Y.Z`).
4. Commit as `chore(release): vX.Y.Z`, tag with `git tag -a vX.Y.Z -m "Engi X.Y.Z"`, and push the
   commit and tag together. `make build` reads the version from the tag.
5. Pushing the tag starts `.github/workflows/release.yml` (or run it by hand with `workflow_dispatch`,
   from the tag, giving the tag as input). Jobs: `meta` validates the tag (`vX.Y.Z` or `vX.Y.Z-rc.1`;
   a hyphen marks a prerelease, which gets no `latest` or `X.Y` image tags); `image` builds and pushes
   the multi-arch image to GHCR with BuildKit SBOM and provenance, writes an SPDX SBOM, then signs the
   digest and attests the SBOM with keyless cosign; `binaries` builds linux/darwin x amd64/arm64 with
   `make build WEB=1`; `release` writes `SHA256SUMS`, signs it with `cosign sign-blob`, and publishes the
   GitHub release. The release notes are the `## [X.Y.Z]` section of `CHANGELOG.md` (GitHub's
   generated notes if that section is missing), so step 2 must be committed before the tag.
6. After the run, spot-check with the commands in [11.1 Verifying images and binaries](11-deployment.md#verifying-images-and-binaries).
   Third-party actions are pinned to commit SHAs; bump them deliberately and keep the version comment.

---
**← Previous** [12. Feature Plan & Roadmap](12-roadmap.md) · **Index** [README](README.md) · **Next →** [14. AI](14-ai.md)
