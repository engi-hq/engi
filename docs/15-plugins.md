# 15. Plugins (any language)

A plugin adds node types to Engi without touching the Engi binary. It is a program in any
language that speaks the **Engi Plugin Protocol v1**, plus a manifest. This page is the
protocol specification and the operator guide. The Go contract it mirrors is on
[page 6](06-nodes-and-plugins.md).

## 15.1 Design choices

Plugins run out of process, so a plugin crash fails only the calls in flight, and plugins can be written in any language. Python Code nodes use the same out-of-process model, with a fresh process per run, timeouts, and an import allow-list.

| Topic | Engi |
|---|---|
| Plugin language | Any; SDKs for Go, Python, TypeScript, .NET |
| Where a plugin runs | Its own process (stdio) or its own service (HTTP) |
| A plugin crashes | Fails the calls in flight; restarted on the next call |
| Install | `engi plugin install <dir>`, versioned on disk, rollback with `engi plugin use` |
| Wire format | JSON-RPC 2.0, one JSON object per line |
| Parameters | The host resolves every parameter for every item up front, so a plugin needs no callback to read them |
| Python Code node | A fresh `python3` process per run, speaking the same protocol |

## 15.2 Transports

| Transport | When | Callbacks to the host |
|---|---|---|
| **stdio** | The plugin is a local executable. The host starts it, owns its lifetime and talks to it over stdin/stdout. stderr is captured as plugin logs. | Yes: `log`, `blob.read`, `blob.write`, `expression.eval` |
| **HTTP** | The plugin is a service (a sidecar, a Deployment, another host). The host sends each JSON-RPC request as `POST <url>/rpc` and reads the JSON-RPC response as the body. | No. `log` is ignored; the other callbacks are not available and the SDKs raise "unsupported" |

Over stdio each message is one JSON object followed by `\n`. Messages may not contain a raw
newline (JSON escapes it), and a message larger than `plugins.maxMessageBytes` closes the
connection. Both sides may send requests at any time and handle them concurrently; responses
are matched by `id`.

## 15.3 Messages

All messages are [JSON-RPC 2.0](https://www.jsonrpc.org/specification). Field names are
camelCase. Unknown fields must be ignored, so the protocol can grow without a new version.

### Host → plugin

| Method | Kind | Params | Result |
|---|---|---|---|
| `initialize` | request | `{protocolVersion, host: {name, version}}` | `{protocolVersion, plugin: {name, version}, nodes: [Descriptor]}` |
| `execute` | request | `ExecuteParams` (below) | `{outputs: [[Item]], wait?: {until?, resumeToken?}}` |
| `health` | request | `{}` | `{}` |
| `$/cancel` | notification | `{id}` | the host gave up on request `id` (cancelled or timed out) |
| `shutdown` | notification | `{}` | finish in-flight work and exit; stdin closes next |

`initialize` is the first message. The plugin answers with the highest protocol version it
supports that is not above the host's (an error `-32602` if there is none); v1 hosts refuse
anything other than `1`.

Rules every SDK follows:

- `execute` names a node by type **and** version; an unknown pair is `-32602`.
- The concurrency cap applies to `execute` only; above it the answer is `-32004` (busy).
  `initialize` and `health` are always served.
- After `shutdown` a plugin accepts no new requests, finishes the calls in flight and exits.
  End of input cancels the calls in flight.
- `$/cancel` may be sent by either side, for any request it gave up on (a plugin cancels its own
  callbacks this way). It is not used over HTTP, where the request timeout is the bound.
- A parameter in `paramErrors` is raised as a node error (`-32001`) when the plugin reads it, so
  the user sees the host's message.

### Plugin → host (stdio only)

| Method | Kind | Params | Result |
|---|---|---|---|
| `log` | notification | `{level: "debug"\|"info"\|"warn"\|"error", message, attrs?: {}, callId?}` | |
| `blob.read` | request | `{callId, ref}` | `{data}` (base64), at most `plugins.maxBlobBytes` |
| `blob.write` | request | `{callId, name, mime, data}` | `BinaryRef` |
| `expression.eval` | request | `{callId, expression, itemIndex}` | `{value}` |

`callId` is the `callId` of the `execute` request the callback belongs to (an opaque string
chosen by the host); the host refuses a callback for a call that is no longer in flight. That is how a plugin reaches the run's blob
store and expression scope without any ambient authority.

A `log` line with a `callId` is logged with the node logger of that execute call, so it appears
wherever the run's own node logs appear and is tied to the run that produced it. The host
matches the id against the calls in flight. A line without a `callId`, or with the id of a call
that has already finished, goes to the server log (the latter with the `callId` attribute). The
message is cut at `plugins.maxLogLineBytes` either way. The SDKs send the `callId` of the current
execute call automatically when a handler logs through its node logger (Go `Input.Logger()`,
Python `ctx.log`, TypeScript `ctx.log`, .NET `ctx.Logger`). Over HTTP, `log` is ignored.

### ExecuteParams

```json
{
  "callId": "c1f3...",
  "node": {"type": "acme.text.reverse", "version": 1},
  "inputs": [[{"json": {"name": "Ada"}}]],
  "params": [{"field": "name"}],
  "paramErrors": [{}],
  "credentials": {"api": {"type": "acme.apiKey", "id": "cr_...", "version": 3, "data": {"key": "..."}}},
  "credentialErrors": {},
  "execution": {"executionId": "ex_...", "workflowId": "wf_...", "workflowName": "Demo",
                "mode": "manual", "nodeName": "Reverse", "runIndex": 0, "attempt": 1, "depth": 0}
}
```

- `inputs` has one array per main input port.
- `params[i]` holds every property of the node resolved against item `i` of input 0 (with
  defaults applied and expressions evaluated). There is always at least one entry, so a node
  with no input items can still read its parameters. A property whose resolution failed is
  missing from `params[i]` and its message is in `paramErrors[i]`; the SDKs raise that message
  when the property is read, so an expression error in a property the plugin never reads does
  no harm. A required property with no value is reported the same way.
- `credentials` holds only the credentials the descriptor declares **and** the node has bound.
  They are decrypted for this call only. A plugin must not log or persist them.

### Items and descriptors

The JSON shapes mirror the Go types of `pkg/nodesdk`:

```json
Item       {"json": {}, "binary": {"<field>": BinaryRef}, "pairedItem": [{"input": 0, "item": 0}],
            "error": {"message": "", "description": "", "httpStatus": 0}}
BinaryRef  {"ref": "", "mime": "", "name": "", "size": 0}
Descriptor {"type", "version", "displayName", "description", "group", "icon",
            "inputs": [PortSpec], "outputs": [PortSpec], "properties": [Property],
            "credentials": [{"name", "type", "required"}], "mergePolicy"}
PortSpec   {"name", "kind": "main", "displayName", "required"}
Property   {"name", "displayName", "type", "default", "required", "placeholder", "description",
            "options": [{"name", "value", "description"}], "children": [Property],
            "displayOptions": {"show": {"param": [values]}, "hide": {...}}}
```

Bytes never travel inside items, only `BinaryRef`s; a plugin reads and writes the bytes with
`blob.read` / `blob.write`.

### Errors

A failed request returns a JSON-RPC error. Engi uses the standard codes plus:

| Code | Meaning | `data` |
|---|---|---|
| `-32001` | Node error: shown to the user like a built-in node's error | `{description?, httpStatus?, itemIndex?}` |
| `-32002` | Cancelled | |
| `-32003` | Unsupported (for example a callback over HTTP) | |
| `-32004` | Busy: more `execute` calls than the plugin's cap | |

Any other error code fails the node with the message as given.

## 15.4 What a plugin may declare (v1)

The host checks every descriptor at load and skips a plugin that breaks a rule:

- The type must be listed in the manifest's `nodes`, and must not start with `engi.` (reserved
  for built-ins). Types are namespaced by convention: `<vendor>.<area>.<name>`.
- Only `action`, `transform` and `flow` groups. Triggers (poll, webhook, listen) and AI sub-nodes
  (`ai.*` ports) are not part of v1, because they need long-lived Go objects in the host.
- Only `main` ports. `Wait` is allowed; a webhook `Response` is not.
- The descriptor passes the same `Validate()` as a built-in node.

## 15.5 Manifest and installation

A plugin directory holds the program and an `engi-plugin.yaml`:

```yaml
name: acme-text            # lower-case letters, digits and dashes
version: 1.2.0             # semantic version
description: Text helpers from Acme.
runtime:
  command: [python3, main.py]   # stdio: run from the plugin directory ...
  env: {ACME_MODE: fast}        # ... with only these variables plus plugins.inheritEnv
  # url: https://acme-text.internal:8443   # ... or HTTP instead of command
  # tokenFile: /run/secrets/acme-text      # bearer token for the HTTP plugin
nodes: [acme.text.reverse, acme.text.slugify]
```

Installed plugins live under `plugins.dir`:

```
plugins/
  acme-text/
    active          # the active version, e.g. "1.2.0"
    1.1.0/          # a copy of the plugin directory
    1.2.0/
```

| Command | Effect |
|---|---|
| `engi plugin install <dir> [--force]` | Validates the manifest, copies the directory to `<plugins.dir>/<name>/<version>/`, starts **the copy** once to check its descriptors, then makes it active. `--force` replaces an installed copy of the same version. |
| `engi plugin list` | Every installed plugin, its versions and the active one |
| `engi plugin use <name> <version>` | Makes another installed version active (upgrade or rollback) |
| `engi plugin remove <name> [--version v]` | Removes one version, or the whole plugin; the active version can only be removed with the whole plugin |

**A plugin directory must be self-contained**: install copies it, so nothing outside it is
reachable afterwards (only relative links that stay inside are kept). Vendor the SDK next to the
entry point (Python: copy `engi_plugin/` beside `main.py`; TypeScript: bundle, or ship
`node_modules`), `pip`/`npm` install it on the server, or publish a self-contained build (.NET,
Go). Because install starts the copy, a plugin that only works from its source directory is
refused at install time, not at the next server start.

The server loads the active version of every plugin at start. A plugin that fails to start or
breaks a rule is logged and skipped; the server still starts, and workflows using its nodes fail
validation with "unknown node type". `GET /api/v1/plugins` shows each plugin's status and error.
Installing or switching versions takes effect at the next server start.

## 15.6 Process management (stdio)

- One process per plugin, started at server start (to read its descriptors) and restarted on
  the next call after it exits. Calls in flight when it exits fail. Restarts are at least
  `plugins.restartBackoff` apart.
- Concurrent calls share the process and are capped by `plugins.maxConcurrentCalls`.
- Each call is bounded by `plugins.callTimeout` and by the execution's context; when either ends,
  the host sends `$/cancel` and stops waiting.
- With `plugins.idleTimeout` above zero, a process that had no call for that long is stopped and
  started again on demand.
- Shutdown sends `shutdown`, closes stdin, waits `plugins.shutdownGrace`, then kills the process.
- The process gets an empty environment except the variables named in `plugins.inheritEnv`
  and the manifest's `env`. Engi's own secrets (`ENGI_*`) are never passed on.
- stderr lines are logged at `info` with the plugin name, cut at `plugins.maxLogLineBytes`.
- A plugin that fails to start (it exits or breaks the handshake) reports its last
  `plugins.startErrorLines` stderr lines after the error, each cut at `plugins.maxLogLineBytes`.
  The same text is the plugin's `lastError` in `GET /api/v1/plugins`. Stderr is the plugin's own
  output and is shown as written; the plugin never receives Engi's settings or secrets, but a
  plugin may print what it was given, so keep secrets out of stderr.

Engi does not sandbox plugins beyond this: a plugin is code you chose to install and it runs with
the server's OS user. For isolation run untrusted plugins as HTTP services in their own containers
(with a `runtimeClassName` such as gVisor and no network egress, see [page 11](11-deployment.md)).

## 15.7 SDKs

| Language | Location | Entry point |
|---|---|---|
| Go | `pkg/pluginsdk` | `pluginsdk.Serve(ctx, info, nodes...)` serves any `nodesdk.Node`; `pluginsdk.Handler` serves HTTP |
| Python 3.9+ | `sdk/python` (`engi_plugin`, stdlib only) | `@plugin.node(...)` then `plugin.serve()` |
| TypeScript / Node 18+ | `sdk/typescript` (`@engi/plugin-sdk`, no runtime deps) | `definePlugin({...}).serve()` |
| C# / .NET 8+ | `sdk/dotnet` (`Engi.Plugin`) | `new Plugin(...).AddNode(...).ServeAsync()` |

SDK defaults match the host's: 32 MiB per message (`plugins.maxMessageBytes`) and 16 calls in
flight (`plugins.maxConcurrentCalls`). An SDK given no ports declares one `main` input and output.
Each SDK ships an example plugin under `examples/` and is tested against the real host by
`go test -tags integration ./internal/adapter/plugin/...` (skipped when the toolchain is missing).

## 15.8 Python Code node

`engi.code.python` runs user Python the way the JavaScript Code node runs JavaScript, with the
same modes (`runOnceForAllItems`, `runOnceForEachItem`) and return shapes. Each run starts a fresh
`nodes.code.python.interpreter` process (`python3 -I`) that speaks the plugin protocol, so the
run's expression scope is reachable through `expression.eval`:

| Name in Python | Meaning |
|---|---|
| `items` | all input items as `{"json": ..., "binary": ...}` dicts |
| `item`, `json` | the current item and its `json` (per-item mode) |
| `item_index`, `run_index` | indexes, as in JavaScript |
| `execution`, `workflow` | the execution info |
| `expr("$('Node').item.json.id")` | evaluates an expression in the same scope as a parameter |
| `print(...)` | goes to the node's log |

Before the code runs, the runner parses it and refuses imports outside
`nodes.code.python.allowedModules` (an entry allows that module and its submodules, so `urllib.parse` does not allow `urllib`; relative and dynamic imports are refused too), plus access to dunder
attributes such as `__subclasses__` or `__globals__`. This is a guard against mistakes, **not a
security boundary**: Python cannot be sandboxed in-process. The process gets an empty environment,
runs under `nodes.code.timeout` and its output is capped by `nodes.code.maxOutputBytes`. For
untrusted authors, run workers in a container with no network and a read-only filesystem.

---
**← Previous** [14. AI](14-ai.md) · **Index** [README](README.md) · **Next →** [Index](README.md)
