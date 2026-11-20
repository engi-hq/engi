## What

<!-- What does this change do? -->

## Why

<!-- Why is it needed? Link the issue if there is one: Fixes #123 -->

## Docs pages changed

<!-- List the docs/ pages you updated, or write "none" with a reason. Behaviour, config and API changes need docs. -->

## Checklist

- [ ] `make check` passes (fmt, vet, lint, race tests)
- [ ] `make web-check` passes, if `web/` changed
- [ ] New code has table-driven tests; a bug fix has a regression test that fails before the fix
- [ ] Docs under `docs/` are updated in this PR (and `docs/api/openapi.yaml` for API changes)
- [ ] `CHANGELOG.md` has a line under `## [Unreleased]`, if users can notice the change
- [ ] Code graph updated with `graphify update .`, with `graphify-out/` committed, if code changed
- [ ] No secrets, keys, `.env` files, databases or binaries are committed
- [ ] Works in standalone (SQLite) and cluster (Postgres) modes, if the change touches storage, queues or roles
