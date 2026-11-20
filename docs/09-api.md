# 9. HTTP API

REST + JSON under `/api/v1`, served by the `api` role. Handlers are thin: decode → call a
service → encode. The contract is **`docs/api/openapi.yaml`**, the source of truth; this page
explains the conventions. Any endpoint change updates the spec in the same change.

## 9.1 Resources

All paths are under `/api/v1` unless noted. The spec lists every route; a test fails if a registered route is missing from it or the reverse.

| Resource | Endpoints |
|---|---|
| Auth | `GET/POST /auth/setup` (first owner), `POST /auth/login`, `POST /auth/logout`, `GET /me` (the user, plus `editor` settings the UI needs: `chatBasePath`, `maxNoteTextBytes`) |
| Users | `GET/POST /users`, `GET/PATCH/DELETE /users/{id}` (owner invites members) |
| API keys | `GET/POST /api-keys` (token shown once), `DELETE /api-keys/{id}` (revoke) |
| Workflows | `GET/POST /workflows`, `POST /workflows/import`, `GET/PUT/DELETE /workflows/{id}`, `GET /workflows/{id}/export` |
| Workflow document | Besides `nodes`, `connections`, `settings`, `pinData` and `tags`, it carries optional `notes` (sticky notes, [page 3 section 3.9](03-data-model.md#39-sticky-notes)); limits `workflow.maxNotes` and `workflow.maxNoteTextBytes` answer 422 |
| Versions | `GET /workflows/{id}/versions`, `GET .../versions/{v}`, `POST .../versions/{v}/restore` |
| Activation | `POST /workflows/{id}/activate`, `.../deactivate`, `.../test-webhook` (arms the test URL) |
| Run | `POST /workflows/{id}/run` (manual; body `{startNode?, items?, destination?, pinData?, workflow?}`, answers 202) |
| Expression preview | `POST /expressions/preview` (read scope; body `{expression, node, itemIndex, runIndex, executionId? \| workflowId?, workflow?}`, answers `{value}`; a bad expression is 422 `expression-error`) |
| Executions | `GET /executions`, `GET/DELETE /executions/{id}`, `POST .../cancel`, `POST .../retry`, `GET .../stream` (SSE), `GET .../binary/{ref}` (binary data, see below) |
| Credentials | `GET/POST /credentials`, `GET/PUT/DELETE /credentials/{id}`, `POST .../test`, `POST .../authorize` (OAuth2 start) |
| Tags | `GET/POST /tags` (`{name}`), `PATCH/DELETE /tags/{id}`; rename and delete apply to every workflow carrying the tag; a taken name is 409; `GET /workflows?tag=NAME` filters |
| Variables | `GET/POST /variables` (`{key, value, secret?}`), `PATCH/DELETE /variables/{id}` (any of `key`, `value`, `secret`); key conflict is 409, invalid key 422; secret values are returned as `__ENGI_SECRET__` |
| Catalogs | `GET /credential-types[/{name}]`, `GET /node-types[/{type}]`, `GET /plugins` (installed plugins and their load state, [page 15](15-plugins.md)) |
| Ops (outside `/api/v1`) | `GET /healthz`, `GET /readyz` |
| OAuth2 | `GET /oauth2/callback` (public; the sealed `state` authenticates it) |

**Binary data.** `GET /executions/{id}/binary/{ref}` streams one binary value of an execution; `ref` is the id part of the item's `blob://<executionId>/<ref>` reference. It needs a session or API key like any read. The response carries the stored `Content-Type`, `Content-Disposition: attachment; filename=...` (`?inline=true` serves images, PDF, plain text, audio and video in place; anything else, SVG and HTML included, is always a download), `X-Content-Type-Options: nosniff` and a sandbox `Content-Security-Policy`. A malformed or unknown reference, or one that belongs to another execution, is 404. Range requests are not supported yet.

Not built yet: `sort`, API rate limiting (section 9.6). Prometheus metrics are served outside `/api/v1` (see [Deployment](11-deployment.md)).

Every other path (anything not under `/api/`, `/oauth2/`, `/healthz`, `/readyz` or the hook prefixes) is served by the embedded editor when `server.ui.enabled` is on ([page 10](10-editor-ui.md)).

Non-API routes on the same server: `/hook/*`, `/hook-test/*`, `/hook-wait/*` ([page 8](08-triggers.md)) `/chat/*` for Chat triggers ([page 14](14-ai.md#chat-trigger)) and `/mcp/*` for MCP Server triggers ([page 14](14-ai.md#1411-mcp-client-tool-and-mcp-server-trigger)). They are mounted next to the API and never pass through its authentication or CSRF checks. Roles: without the `api` role only the probes are served; webhook routes need the `webhook` role, activation needs the `trigger` role (otherwise 501).

### Partial runs and unsaved documents

`destination: {node, mode}` runs only the nodes needed to compute `node`; `mode` is `inclusive` (default, the node runs, its children do not) or `exclusive` (it stops before the node). `pinData: {"<node>": [{...}]}` replaces those nodes' output for this run only. `workflow` is an unsaved document the editor runs without saving: it is validated like a save, never stored as a version, and the execution is recorded against the latest saved version (mode `manual`). These extras live in the execution's saved input, so an execution that later waits and resumes continues as an ordinary run of the saved version, and a retry reruns the saved version.

The preview evaluates with the real expression evaluator (same timeout and size limits as `expression.*` config) on the recorded input of `node` in a past execution: the one in `executionId`, or the newest of `workflowId` (or `workflow.id`) that ran the node.

## 9.2 Authentication

| Client | Mechanism |
|---|---|
| Editor UI | Session cookie: `HttpOnly`, `Secure` (by default; see below), `SameSite=Lax`, opaque server-side session ID. State-changing requests require a CSRF token header |
| Automation | `Authorization: Bearer <api-key>`. Keys are shown once, stored as a hash, carry a name, optional expiry and scopes (`read`, `write`) |

Cookie auth: the login response and `GET /me` return a `csrfToken`; send it in the header `auth.session.csrfHeader` (default `X-CSRF-Token`) on every non-GET request. API keys are not ambient credentials and need no CSRF token; a `read` key may only use GET/HEAD, `write` allows everything, and a key cannot create keys. Logout needs no token.

**Cookie `Secure` and plain HTTP.** Browsers and most HTTP clients drop a `Secure` cookie that
arrived over plain HTTP, so the session cookie does not work on an `http://` URL unless the
setting allows it. `auth.session.cookieSecure` (env `ENGI_AUTH_SESSION_COOKIE_SECURE`) takes:

- `always` (default): always `Secure`. Use over HTTPS only.
- `auto`: `Secure` when Engi got the request over TLS, or, behind a proxy trusted with
  `server.trustForwardedFor`, when the proxy's `X-Forwarded-Proto` says `https`. Use it for
  TLS-terminating ingress or load balancers.
- `never`: never `Secure`. For plain-HTTP development and for in-cluster clients that keep
  the cookie themselves.

`SameSite=None` needs `always` or `auto`. The boolean forms still work (`true` is `always`,
`false` is `never`).

**Machine clients: use an API key, not the cookie.** Scripts, workers, other services and
in-cluster jobs should send `Authorization: Bearer <api-key>` (create the key in the editor's
Settings, or `POST /api-keys`, with the narrowest scope: `read` if it only reads). Keys need no
CSRF token and no cookie, so they work over plain HTTP inside a cluster without changing the
cookie setting. Keep the key in a secret, and revoke it with `DELETE /api-keys/{id}` when it is
no longer used.

Passwords use argon2id. Login attempts are rate limited. Authorization is per project and
role once Projects land (Roadmap phase 8); until then an owner/member split applies. Secret
credential fields are never returned.

## 9.3 Pagination and filtering

Cursor pagination on every list:

```
GET /api/v1/executions?limit=50&cursor=eyJpZCI6...&status=error&workflowId=wf_01J...&from=2026-10-01T00:00:00Z
```
```json
{ "data": [ ... ], "nextCursor": "eyJpZCI6...", "hasMore": true }
```

- `limit` default and max come from config (`api.pagination.*`). Cursors are opaque, stable under inserts (keyset on `(sortKey, id)`).
- Filters are plain query parameters per resource (`status`, `workflowId`, `tag`, `mode`, `from`, `to`, `q` for name search). Unknown filters return `400`. Workflows filter by `active`, `tag`, `q`; executions by `status` (repeatable), `workflowId`, `from`, `to`.
- `sort=-startedAt` style, from an allow-list per resource.

## 9.4 Errors (RFC 9457)

All errors are `application/problem+json`:

```json
{ "type": "https://engi.dev/problems/validation", "title": "Validation failed", "status": 422,
  "detail": "node 'Fetch orders' has no url", "instance": "/api/v1/workflows/wf_01J...",
  "errors": [ { "pointer": "/nodes/0/parameters/url", "message": "required" } ],
  "requestId": "01J..." }
```

Domain errors map once, in the adapter: `ErrNotFound`→404, `*ValidationError`→422, conflict→409,
auth→401/403, rate limit→429 (with `Retry-After`). Internal errors return 500 with a request
ID and no internals. Writes accept an `If-Match` version for optimistic concurrency (workflow `version`).

## 9.5 Live execution stream (SSE)

`GET /executions/{id}/stream` returns `text/event-stream`. In cluster mode the `api` pod
subscribes to the bus topic for that execution; any replica can serve it. `Last-Event-ID`
resumes; a heartbeat comment is sent at a config interval. The in-process bus keeps no history, so `Last-Event-ID` cannot replay: a reconnect starts from the current state, and the stream of an already finished execution answers with its final event and closes. Item payloads are never streamed (only counts per output port).

| Event | Payload |
|---|---|
| `execution.started` | execution id, workflow version, mode |
| `node.started` | node name, runIndex |
| `node.finished` | node name, `NodeRun` (item payloads truncated; fetch full via `GET /executions/{id}`) |
| `execution.waiting` | `waitUntil` or resume info |
| `execution.finished` | final status, error |

For test webhooks and manual runs the editor opens the stream right after `POST .../run`.

## 9.6 Rate limiting and limits

Token bucket per API key / session / IP, in-memory standalone and shared via the store or
bus in cluster mode. Config: `api.rateLimit.enabled`, `.requestsPerMinute`, `.burst`;
separate stricter bucket for `/auth/*`. Responses carry `RateLimit-*` headers. Request body
limit: `api.maxBodyBytes`. Webhook limits are separate ([page 8](08-triggers.md#82-webhooks)).

## 9.7 Versioning

`/api/v1` is additive-only: new fields and endpoints are allowed, removals or meaning
changes require `/api/v2`. Deprecations are marked in the OpenAPI spec first.

---
**← Previous** [8. Triggers & Webhooks](08-triggers.md) · **Index** [README](README.md) · **Next →** [10. Editor UI](10-editor-ui.md)
