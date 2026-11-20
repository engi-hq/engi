# Contributing to Engi

Engi is a self-hosted workflow automation platform written in Go, shipped as a single binary.
It runs with an embedded SQLite database, or scales out on Postgres and Kubernetes.

Thank you for helping. This page covers the basics. The details live in
[CLAUDE.md](CLAUDE.md) (the engineering rules) and the [Developer Guide](docs/13-dev-guide.md).

## Prerequisites

| Tool | Version |
|---|---|
| Go | the version on the `go` line of [`go.mod`](go.mod) |
| golangci-lint | v2.15.0 (the version CI uses); config in [`.golangci.yml`](.golangci.yml) |
| Node.js and npm | Node.js 22 or newer (CI uses 24), only for `web/` |
| Docker | only for integration tests |
| Python 3.9+ | only for the docs website (`make docs-site`) |

## Build and run

```bash
make build                       # ./bin/engi without the editor (no Node needed)
make build WEB=1                 # also builds and embeds the editor (needs Node)

export ENGI_ENCRYPTION_KEYS="k1:$(openssl rand -base64 32)"
export ENGI_ENCRYPTION_ACTIVE_KEY_ID=k1
export ENGI_AUTH_SESSION_COOKIE_SECURE=false   # plain HTTP, local development only
./bin/engi serve
```

To work on the editor with hot reload, run `engi serve` as above, then in a second terminal:
`cd web && npm ci && npm run dev` (see [section 13.11](docs/13-dev-guide.md#1311-editor-web-workflow)).

## Checks

Run these before you open a pull request:

```bash
make check        # gofmt, go vet, golangci-lint, go test -race ./...
make web-check    # editor lint, typecheck and tests (when you change web/)
```

Other checks:

```bash
go test -tags integration ./...   # adapter integration tests; needs Docker
make docs-nodes                   # regenerate docs/nodes after adding or changing a node
make docs-check                   # fail if docs/nodes is stale
make helm-check                   # render the Helm chart (needs helm)
```

## Architecture

Dependencies point inward only:

```
cmd/  ->  internal/app (wiring)  ->  adapters  ->  ports  ->  domain and services
```

- Domain and service code never imports adapters, `net/http` or SQL drivers.
- Interfaces belong to the code that uses them. Constructors take their dependencies; there are no globals and no `init()` side effects.
- A new storage, queue or bus backend is a new adapter that implements an existing port. The core does not change.
- Limits, timeouts, URLs and paths belong in `internal/config`, with defaults documented in [docs/11](docs/11-deployment.md). No magic numbers in business logic.
- Wrap errors with `%w`. Use `log/slog` with an injected logger. Never log secrets.

Full rules: [CLAUDE.md](CLAUDE.md) and [docs/02-architecture.md](docs/02-architecture.md#21-code-architecture-clean--hexagonal).

## Tests

- Table-driven unit tests live next to the code (`_test.go`). Domain and service tests use fakes of the ports, not real infrastructure.
- Adapters have integration tests behind the `integration` build tag. Both store adapters (SQLite and Postgres) must pass the same contract suite.
- Engine golden tests live in `internal/service/engine/testdata`.
- Every bug fix ships with a regression test that fails before the fix.

## Branches and commits

- Branch from an up-to-date `main`. Use a short name such as `fix/<slug>` or `docs/<slug>`.
- Use [Conventional Commits](https://www.conventionalcommits.org) with a scope where it helps:
  `feat(engine): ...`, `fix(store/sqlite): ...`, `docs: ...`. Common types are `feat`, `fix`, `perf`,
  `refactor`, `test`, `docs`, `chore` and `ci`. Mark breaking changes with `!` and a `BREAKING CHANGE:` footer.
- Keep each commit to one logical change, with the subject in the imperative mood and under 72 characters.
- Code, tests and docs for a change land together, in the same pull request.
- Never commit secrets, `.env` files, local databases, `bin/` or generated binaries.

If you use the code graph (graphify), run `graphify update .` after code changes and commit the
refreshed `graphify-out/` with them.

## Changelog

A change users can notice (`feat`, `fix`, `perf`, breaking changes, security fixes, deprecations)
adds one line under `## [Unreleased]` in [CHANGELOG.md](CHANGELOG.md), in the same commit as the code.
Use the matching section: Added, Changed, Deprecated, Removed, Fixed or Security. Write the line for
someone running Engi, not as the commit subject. Skip internal-only changes such as `refactor`, `test`,
`chore` and `ci`. See [section 13.12](docs/13-dev-guide.md#1312-changelog-and-releases).

## Adding a node

A node is one package under `internal/node/<group>/<name>/`. It depends only on `pkg/nodesdk`, is
registered explicitly in `internal/app`, and ships with tests and a docs page. The full checklist is in
[section 13.5](docs/13-dev-guide.md#135-adding-a-node).

## Writing a plugin

A plugin adds node types without changing the Engi binary. It can be written in any language that speaks
Engi Plugin Protocol v1 over stdio or HTTP. SDKs exist for Go (`pkg/pluginsdk`), Python (`sdk/python`),
TypeScript (`sdk/typescript`) and .NET (`sdk/dotnet`). The protocol and operator guide is
[docs/15-plugins.md](docs/15-plugins.md).

## Original work

Engi's design is its own. Prior art may be studied for behaviour if useful: what a feature does, its edge cases and its data shapes. Read it, then write Engi's own implementation in idiomatic Go, and describe the behaviour in the matching `docs/` page in your own words.

Contributions must be your own original work. Do not copy code, text, icons, UI components or tests from
other projects unless their licence allows it, and say so in the pull request if you do.

## Licence

Engi is licensed under the [Apache License 2.0](LICENSE). By intentionally submitting a contribution, you
agree that it is licensed under the same terms (Apache License 2.0, Section 5). No contributor licence
agreement or sign-off is required.

## Conduct and security

Follow the [Code of Conduct](CODE_OF_CONDUCT.md). Report security vulnerabilities privately, as described in
[SECURITY.md](SECURITY.md). Do not open a public issue for a vulnerability.
