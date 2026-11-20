# 1. Vision & Principles

## Why Engi

Engi is a lightweight, self-hosted workflow automation platform. It has a small footprint, and the features that production needs, such as high availability and scaling, are included.

**One sentence:** connect your data sources, wire nodes together, and the workflows run,
on one machine or a thousand pods.

## Principles

1. **Runs alone.** One static binary with no required dependencies. `engi serve` starts with
   embedded SQLite and an in-process queue. Postgres, a queue and object storage are only
   needed when you scale out.
2. **Light.** Target under 50 MB RSS when idle and under 100 ms cold start. One worker should
   handle thousands of concurrent I/O-bound executions.
3. **Scales horizontally.** Every role is stateless. Kubernetes HA is built in and free.
4. **Any language.** Built-in nodes are Go. Custom nodes can be written in any language
   through a small, versioned plugin protocol (see [page 6](06-nodes-and-plugins.md)).
5. **Familiar.** Workflows are made of items, expressions, triggers and a canvas of connected nodes. Engi implements these concepts itself.
6. **Secure by default.** Credentials are encrypted at rest, user code is sandboxed, there is
   no phone-home, and it works air-gapped.
7. **Engineered.** Clean architecture, typed config, tests and docs with every change (see `CLAUDE.md`).

## Non-goals (for now)

- A hosted SaaS offering. Engi is self-hosted first.
- Running nodes or code from other workflow tools. A workflow-JSON *importer* may come later as a separate tool.
- 400+ integrations on day one. We ship a strong core and a generic HTTP node, and grow the catalogue.

## Targets

| Metric | Target |
|---|---|
| Idle memory (standalone) | < 50 MB RSS, also after login and webhook executions (measured ~30 MB idle, ~55 MB on macOS after a login and 100 executions; macOS reports freed heap in RSS until the OS needs it, Linux drops it at once) |
| Binary size | < 60 MB |
| Engine overhead per node | < 1 ms (excluding node I/O) |
| Webhook → execution start (standalone) | < 10 ms p50 |
| Concurrent executions per worker | configurable, default from config |

---
**← Previous** [Quick Start](00-quick-start.md) · **Index** [README](README.md) · **Next →** [2. Architecture](02-architecture.md)
