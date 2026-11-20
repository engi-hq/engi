# 5. Expressions

Package: `internal/service/expression`. Engine: [goja](https://github.com/dop251/goja)
(pure-Go ECMAScript 5.1+ with most ES6), so users write familiar JavaScript.

## 5.1 Syntax

- A parameter whose string value starts with `=` is an expression template.
- Inside the template, `{{ ... }}` blocks are evaluated as JS. Everything else is literal text.
- If the whole template is a single `{{ }}` block, the **raw value** keeps its type (number,
  object, array). Otherwise the result is string interpolation.

```
"=Hello {{ $json.name }}"          → "Hello Ada"
"={{ $json.items.length > 0 }}"    → true (boolean)
```

## 5.2 Variables in scope

| Name | Meaning |
|---|---|
| `$json` | JSON of the current input item |
| `$binary` | Binary metadata of the current item |
| `$input.item`, `$input.all()`, `$input.first()`, `$input.last()` | Current input |
| `$("Node Name").item` | The item in that node's output paired with the current item (via `pairedItem`) |
| `$("Node Name").all()`, `.first()`, `.last()` | That node's output (last run) |
| `$("Node Name").isExecuted` | `true` if that node has run |
| `$node["Node Name"].json` | Shorthand for the paired item's json |
| `$vars.NAME` | Workflow/project variables |
| `$env.NAME` | Only variables on the allow-list from config (no env access by default) |
| `$execution.id`, `$execution.mode`, `$workflow.id`, `$workflow.name` | Metadata |
| `$execution.resumeUrl` | Absolute URL that resumes the execution's `Wait` node set to "On webhook call" (see below). Same in every node of the execution |
| `$now`, `$today` | Time (from the injected Clock) |
| `$itemIndex`, `$runIndex` | Position |

**`$execution.resumeUrl`** is `server.publicUrl` plus `webhook.waitBasePath` plus `/<executionId>.<secret>`, for example `https://engi.example.com/hook-wait/<executionId>.<secret>`. Send it before the Wait node runs (an email node placed earlier, for instance) and the person who opens it resumes the execution. It is built from configuration, never from the request, and it is the same in every node of the execution, including after a Wait node. The secret is drawn once when the execution starts, so the URL works for every webhook Wait node the execution reaches.

> **Security.** The resume URL is a bearer link: whoever holds it can resume any waiting webhook Wait node of that execution. Every node of the execution can read `$execution.resumeUrl`, so a node that logs, returns or forwards its input data hands the link on too. Send it only to the party that should resume the run, and do not put it in logs or in a response to someone else. An unknown, wrong or already used token answers `404`.

Numbers in results are always `float64`, matching items decoded from JSON. `$input.item` and
`$("Node").item` are `{json, binary}` objects. Data is copied into the VM as native JavaScript
objects, so an expression cannot mutate engine data.

### Behaviour notes

- Numbers: JavaScript has one number type, and items are decoded from JSON, so integers and floats are both `float64`. `6 / 2` is `3`, which serializes as `3`.
- `$now` and `$today` are JavaScript `Date` objects (UTC; `$today` is midnight UTC), and there is no workflow timezone setting yet. Use `$fn.formatDate`. A whole-template `$now` result serializes as an RFC 3339 string.
- Built-in helper methods on strings, arrays, numbers and dates, and `$jmespath`, are not provided.
- `$node["Name"]` and `$("Name").item` resolve the paired item and raise an error when pairing is missing or ambiguous (see [page 4](04-engine.md)).

Helper functions are provided in our own `$fn` namespace. Implemented so far: `sha256`,
`hmacSha256`, `base64Encode`, `base64Decode`, `formatDate(date, "YYYY-MM-DD HH:mm:ss")` (UTC).

## 5.3 Sandbox and limits

- No `require`, no filesystem, no network, no `process`, and no timers that outlive the call.
- Built-ins are frozen when a pooled VM is created and expressions run in strict mode, so one
  evaluation cannot leave state behind for the next. Go cannot cap a JavaScript heap, so
  the evaluation timeout is the guard against runaway allocation.
- Limits come from config: max execution time per evaluation, max output size, max template length, max call-stack depth.
- VMs are **pooled** per worker (`sync.Pool`). Compiled programs are cached by template hash (LRU, size from config).
- Errors return a `domain.ExpressionError` with the node, parameter, item index and a JS stack excerpt.

## 5.4 Code node

`engi.code.javascript` runs a user script with the same scope (`$input`, `$json`, ...) in a
goja VM with stricter limits. Other languages (Python, ...) run in the `runner` role through
the plugin protocol ([page 6](06-nodes-and-plugins.md)), never in-process.

---
**← Previous** [4. Execution Engine](04-engine.md) · **Index** [README](README.md) · **Next →** [6. Nodes & Plugins](06-nodes-and-plugins.md)
