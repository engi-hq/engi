# Engi — Engineering Guide for AI Agents and Contributors

Engi (縁起, えんぎ) is a lightweight, self-hosted workflow automation platform written in Go.
It runs as a single binary on its own, or scales out on Kubernetes. **Start every session by reading `PROGRESS.md`** (current state and handoff), then
`docs/README.md` only as needed.

## Ground rules

1. **Original work only.** Studying how other tools behave is fine; copying is not. Never copy or
   translate code, node implementations, UI components, icons, text or tests from another project
   whose license does not allow it. Write Engi's own design in idiomatic Go, and record design
   decisions in the matching `docs/` page, in your own words.
2. **Engineer, don't vibe-code.** Every change should be one a senior reviewer would
   approve: small, tested, named well, consistent with the architecture below.
3. **No hardcoding.** No magic numbers, URLs, ports, timeouts, paths, credentials, limits
   or feature flags in business logic. See [Configuration](#configuration).
4. **Docs move with code.** A change to behaviour, config or API updates the matching page
   in `docs/` in the same change.

## Architecture: Clean / Hexagonal

Dependencies point **inward only**:

```
cmd/  →  internal/app (wiring)  →  adapters  →  ports  →  core (domain + use cases)
```

| Layer | Path | May import | Must NOT import |
|---|---|---|---|
| Domain | `internal/domain/...` | stdlib only | anything else in `internal/` |
| Use cases / services | `internal/service/...` | domain, ports | adapters, `net/http`, SQL drivers |
| Ports (interfaces) | `internal/port/...` | domain | adapters |
| Adapters | `internal/adapter/<kind>/<impl>` (e.g. `store/sqlite`, `store/postgres`, `queue/memory`, `http/api`) | domain, ports, service, the shared infrastructure packages below | other adapters (one port implementation never imports another) |
| Composition root | `internal/app` | everything | — (only place that wires concrete types) |
| Entrypoints | `cmd/engi` | `internal/app`, `internal/config` | domain logic |
| Public SDK | `pkg/...` | stable, versioned API for plugin authors only | `internal/` |

Rules:
- **Interfaces belong to the consumer.** Define them in `port` or in the package that uses
  them, keep them small (1–5 methods), and return concrete types from constructors.
- **Constructor dependency injection** (`NewX(deps...) *X`). No package-level mutable state,
  no `init()` side effects, no service locators or globals (including loggers).
- The domain has **no knowledge** of HTTP, SQL, JSON tags for transport, or vendors.
  Map between domain and DTOs at the adapter boundary.
- A new storage, queue or bus backend is a new adapter that implements an existing port.
  The core does not change.
- Nodes (integrations) depend only on the node SDK contract, never on engine internals.
- **Adapters import each other only through the shared infrastructure packages:**
  `internal/adapter/pgdb` (Postgres pool, migrations, transactions), `internal/adapter/sqlitedb` (SQLite
  equivalent), `internal/adapter/jsoncodec` (JSON wire/file format) and `internal/adapter/plugin/pluginnode`
  (plugin protocol to node contract). Choosing between implementations (for example the LLM provider) is
  composition and belongs in `internal/app`. Test files may import other adapters to run contract and
  conformance suites. Enforced by the `adapter` depguard rule in `.golangci.yml`.

## Go standards

- Go version is pinned in `go.mod`. Code must pass `gofmt`, `go vet` and `golangci-lint run`
  using the config in `.golangci.yml`.
- **Layout:** `cmd/`, `internal/`, `pkg/`, `docs/`, `deploy/`, `migrations/`. No `utils`, `common`
  or `helpers` packages. Name packages by what they provide.
- **context.Context** is the first parameter of anything that does I/O, blocks or can be
  cancelled. Never store a context in a struct. Respect cancellation in loops.
- **Errors:** wrap with `fmt.Errorf("doing x: %w", err)`. Expose sentinel or typed errors from
  the domain (`ErrNotFound`, `*ValidationError`) and check them with `errors.Is/As`. Never panic
  for expected failures. Never swallow an error. Log it **or** return it, not both.
- **Logging:** `log/slog`, structured, with the logger injected. No `fmt.Println` or `log.Printf`
  in library code. Never log secrets or credential payloads.
- **Concurrency:** every goroutine has an owner and a shutdown path (`errgroup`, context).
  No unbounded goroutine fan-out. Concurrency limits come from config.
- **Naming:** idiomatic Go (`ID`, `URL`, `HTTP`), no stutter (`workflow.Workflow` is fine,
  `workflow.WorkflowService` is not, use `workflow.Service`). Comment exported identifiers.
- **Dependencies:** prefer stdlib. Each third-party module must be justified (maintained,
  license compatible: MIT/BSD/Apache-2.0/MPL-2.0). No cgo in the default build (static binary).

## Configuration

- All settings live in `internal/config` as one typed struct, loaded in this order: defaults →
  config file (optional) → environment variables (`ENGI_*`) → CLI flags.
- Defaults are declared **once**, in the config package, and documented in `docs/11-deployment.md`.
- Config is validated at startup (fail fast with a clear message) and passed down as values or
  narrowed sub-structs. Packages never read `os.Getenv` directly.
- Secrets (encryption key, DB password) come from env/files/secret stores and are never
  committed, logged or given defaults.

## Testing

- Table-driven unit tests next to the code (`_test.go`). Domain and service layers are tested
  with fakes of the ports, never real infrastructure.
- Adapters have integration tests behind the build tag `integration`, using Docker
  (testcontainers-go) for Postgres, Redis and similar.
- The engine has golden tests: workflow JSON in, expected execution result out
  (`internal/service/engine/testdata`).
- `go test -race ./...` must pass. New code ships with tests, and bug fixes ship with a regression test.

## Data and persistence

- Schema changes go through versioned SQL migrations in `migrations/<driver>/`. Never
  auto-migrate from structs.
- SQLite (pure Go, `modernc.org/sqlite`) for standalone mode, Postgres (`pgx`) for cluster mode.
  Both sit behind the same store ports and pass the same contract test suite.
- Credentials are encrypted at rest (AES-256-GCM, key from config). Plaintext never reaches
  logs, API responses or execution data.

## API

- REST + JSON, versioned under `/api/v1`. Handlers are thin: decode → call a service → encode.
- Errors use one problem format (RFC 9457 `application/problem+json`).
- The OpenAPI spec in `docs/api/openapi.yaml` is the source of truth and must be updated with
  every endpoint change.

## Workflow for AI agents

- **Navigate with the code graph, not grep.** To find where something lives, how it is wired
  or who calls it, ask the graph first, from the repo root:
  - `graphify query "<question>"`: where a feature or behaviour is implemented
  - `graphify explain "<Symbol>"`: a symbol and its neighbours (callers, callees, owners)
  - `graphify path "A" "B"`: how two pieces connect (e.g. a route to a store)
  - `graphify affected "<Symbol>"`: what a change to it can break, **before** editing it

  Then read only the files and line ranges the graph points to. `grep`/`find` are for exact
  strings the graph cannot answer (an error message, a config key, a literal), not for exploring.
- **The graph comes first, every time.** This includes one-off questions such as "how do I start
  the server", "which env vars does it need", "where is the Makefile target" or "which files
  are .NET projects". Start with `graphify query`/`explain` (or `graphify-out/GRAPH_REPORT.md`
  for an overview). Do not run `find`, `ls -R`, `grep -r` or chains of `cat` to discover
  structure. Fall back to a targeted `grep` only after the graph has no answer, and say so.
- **Keep the graph current in every change.** After changing code, run `graphify update .` and
  commit the refreshed `graphify-out/` **in the same commit** as the code, so the graph on every
  commit matches its code. No separate "refresh graph" commits. When subagents run in parallel,
  only the orchestrator runs `graphify update`, right before each commit.
- In your report, say which design choices you made and why.
- **Subagents:** coding subagents use `sonnet` (or `haiku` for small mechanical edits).
  Do not spawn Opus subagents. Give each subagent one bounded task, the relevant doc pages,
  and the acceptance criteria: build passes, tests pass, lint is clean.
- Before declaring a task done, run `make check` (fmt, vet, lint, test) and report the real output.

## Commands

```bash
make build      # build ./bin/engi
make test       # go test -race ./...
make lint       # golangci-lint run
make check      # fmt + vet + lint + test
make run        # run standalone server with local config
```

## Git

- Repo has an `origin` remote. **Never push unless the user asks.** Use Conventional Commits (`feat:`, `fix:`, `docs:`,
  `refactor:`, `test:`, `chore:`) with a scope where useful (`feat(engine): ...`).
- **One branch per phase.** Before starting a phase, branch from an up-to-date `main`: `phase-<N>-<slug>`
  (e.g. `phase-7-ai`). All work for that phase is committed on its branch. When the phase is complete and `make check`
  (and `make web-check` if `web/` changed) passes on the branch, merge into `main` with `git merge --no-ff` and keep the branch.
  Small non-phase work (docs, fixes) may use its own short-lived branch (`docs/<slug>`, `fix/<slug>`), merged the same way.
- One logical change per commit. Before every commit: `make check` passes, and for a code change
  `graphify update .` has run and `graphify-out/` is staged with it.
- Subagents do not commit. The orchestrating session reviews their work, then commits.
- Never commit secrets, `bin/`, local databases or `.env` files.
- **Changelog.** A change users can notice (`feat`, `fix`, `perf`, breaking, security, deprecation) adds a line under
  `## [Unreleased]` in `CHANGELOG.md` in the same commit, in the matching Keep a Changelog section (`Added`, `Changed`,
  `Deprecated`, `Removed`, `Fixed`, `Security`). Write for users, not from the commit subject. Skip internal-only
  changes (`refactor`, `test`, `chore`, `ci`). Releases follow [docs/13 §13.12](docs/13-dev-guide.md#1312-changelog-and-releases).

## Docs

Docs are paginated in `docs/`, numbered `NN-topic.md`. `docs/README.md` is the index.
Every page ends with a nav footer: `← Previous | Index | Next →`.
