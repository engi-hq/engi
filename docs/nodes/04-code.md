# 4. Code

Run your own JavaScript or Python.

## Code (JavaScript)

Runs your own JavaScript on the incoming items. Use it when the other transform nodes cannot express what you need: loops, grouping, custom parsing. Prefer Edit Fields, Filter or Sort for simple jobs; they are easier to read and need no code.

**Modes**

- **Run once for all items** (default): the code runs one time. `$input.all()` returns every item as `{ json, binary }`. `$json` and `$binary` hold the **first** item's data, so `$json` is `{}` when there is no input. Return an array of items, or of plain objects.
- **Run once for each item**: the code runs per item with `$json` set to that item's data. Return an object, an array of objects (the item becomes several), or `null` to drop it.

The code is a function body, so end it with `return`. An item is either `{ json: {...}, binary: {...} }` or a plain object, which becomes the item's JSON. Anything else fails with `returned item N must be an object`.

**In scope**

| Name | What it is |
|---|---|
| `$json`, `$binary` | Current item's data and binary references. In all-items mode, the first item's (`{}` when there is no input). |
| `$input` | `all()`, `first()`, `last()`, and `item` (per-item mode only). |
| `$("Node Name")` | Another node's output: `.item`, `.all()`, `.first()`, `.last()`, `.isExecuted`. |
| `$node`, `$vars` | Same as in [expressions](../05-expressions.md). Read-only. |
| `$env` | Only the variables in `expression.envAllowList`; others read as undefined. |
| `$fn` | Helpers: `sha256`, `hmacSha256`, `base64Encode`, `base64Decode`, `formatDate`, and more. |
| `$itemIndex`, `$runIndex`, `$execution`, `$workflow`, `$now`, `$today` | Run context and time. |
| `console.log/info/warn/error` | Written to the server log, not to the item. |

**Sandbox**: strict-mode JavaScript with no `require`, `import`, timers, `process`, filesystem or network access; use an [HTTP Request](05-http.md) node for I/O. Built-ins are frozen and every run gets a fresh runtime, so nothing persists between runs. `$vars`, `$env` and `$("Node")` are lookups through the engine and see exactly what an expression in the same node would.

**Output**: the items you return, in order. In per-item mode, each result is linked to its source item.

```json
{ "email": "ada@example.com", "domain": "example.com" }
```

**Example**: per-item mode, adding a domain field and dropping items with no email.

```json
{
  "mode": "runOnceForEachItem",
  "jsCode": "if (!$json.email) { return null; }\nreturn { ...$json, domain: $json.email.split('@')[1], id: $fn.sha256($json.email) };"
}
```

**Notes**

- Limits ([configuration](../11-deployment.md)): `nodes.code.timeout` (10s, shared by all items of one run), `nodes.code.maxCallStackDepth` (256) and `nodes.code.maxOutputBytes` (8 MiB serialized result).
- Common errors: `JavaScript syntax error` (the message carries the position), `JavaScript error: ...` for a thrown error, `code ran longer than 10s`, `the code returned N bytes, the limit is M`, and `data that cannot be serialized`.
- Numbers are plain floats and dates you return become ISO 8601 strings.
- **Notes:** no `require` or npm modules, Python runs as a separate node ([Code (Python)](#code-python)), and no `$helpers` HTTP calls; `$fn` replaces the built-in helpers. `$input.item` exists only in per-item mode.

<!-- BEGIN GENERATED: engi.code.javascript -->
**Type:** `engi.code.javascript` · **Version:** 1 · **Group:** `transform` · **Display name:** Code (JavaScript)

Run JavaScript in a sandbox to transform items.

**Inputs**

| Index | Kind | Display name | Required |
|---|---|---|---|
| 0 | `main` | main | no |

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

None.

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `mode` | Mode | `options` | `runOnceForAllItems` | no | `runOnceForAllItems` (Run once for all items)<br>`runOnceForEachItem` (Run once for each item) | always | — |
| `jsCode` | JavaScript | `code` | `// $input.all() returns every item. Return an array of items. return $input.all();` | yes | — | always | Function body. In scope: $json, $binary, $input (all/first/last/item), $("Node"), $node, $vars, $env, $fn, $itemIndex, $runIndex, $execution, $workflow, $now, $today, console.log. |
<!-- END GENERATED: engi.code.javascript -->

---
**← Previous** [3. Transform](03-transform.md) · **Index** [README](README.md) · **Next →** [5. HTTP](05-http.md)

## Code (Python)

Runs your own Python on the incoming items, with the same two modes and return shapes as [Code (JavaScript)](#code-javascript). Each run starts a fresh `python3` process, so nothing persists between runs. It needs Python 3.9 or newer on the machine that runs Engi; if `nodes.code.python.interpreter` cannot be found the node fails with an error that names it.

**In scope**

| Name | What it is |
|---|---|
| `items` | Every input item as a `{"json": ..., "binary": ...}` dict. |
| `item` | The current item (per-item mode; `None` in all-items mode). |
| `json` | The current item's data in per-item mode; the first item's data in all-items mode (`{}` when there is no input). |
| `item_index`, `run_index` | Index of the current item (0 in all-items mode) and of this node run. |
| `execution`, `workflow` | Dicts: `{"id", "mode"}` and `{"id", "name"}`. |
| `expr("$('Node').item.json.id")` | Evaluates an [expression](../05-expressions.md) in the scope a parameter of this node would see; `expr(e, item_index)` picks another item. Raises `ExprError` on failure. |
| `print(...)` | Written to the server log, not to the item. |

The code is a function body, so end it with `return`. A plain dict becomes the item's JSON; a dict with a `json` key is an item. Per-item mode may return one dict, a list of dicts, or `None` to drop the item. Dates, `Decimal`, `UUID` and sets are converted to JSON values; `NaN` is an error. Importing a module named `json` replaces the `json` variable of per-item mode from that line on.

**Restrictions**: before running, the code is parsed and refused when it imports a module outside `nodes.code.python.allowedModules`, uses a relative import, names `__import__`, `importlib`, `eval`, `exec`, `compile`, `open`, `globals`, `locals`, `vars`, `getattr`, `setattr`, `delattr`, `breakpoint` or `input`, or touches an attribute starting with `__` (or a frame attribute such as `gi_frame`). The refusal lists every offending line. An allow-list entry permits that module and its submodules: `json` permits `json.decoder`, and `urllib.parse` permits `urllib.parse` but not `urllib` (`from urllib import parse` is accepted because `urllib.parse` is allowed). The process has an empty environment, a reduced set of builtins and a timeout.

**This is a guard against mistakes, not a security boundary.** Python cannot be sandboxed in-process; anyone who can edit workflows should be trusted like a developer on the host. For untrusted authors, run workers in a container with no network and a read-only filesystem, or set `nodes.code.python.enabled` to `false`.

**Example**: per-item mode, adding a domain field and dropping items with no email.

```json
{
  "mode": "runOnceForEachItem",
  "pythonCode": "if not json.get('email'):\n    return None\nreturn {**json, 'domain': json['email'].split('@')[1]}"
}
```

**Notes**

- Limits ([configuration](../11-deployment.md)): `nodes.code.timeout` (10s, shared by all items of one run; the process is killed when it expires) and `nodes.code.maxOutputBytes` (8 MiB serialized result), shared with the JavaScript node.
- Common errors: `ValueError: ...` (the exception type and message, with the failing lines of your code in the description), `ForbiddenCode: line 3: import of 'os' is not allowed`, `SyntaxError: ... (line N)`, `Python code ran longer than 10s`, `the code returned N bytes, the limit is M`, and `returned item N must be a dict`.
- Numbers come back as plain floats, as in the JavaScript node.
- **Notes:** no `_input`, `_items` or `_node` objects (use `items`, `item` and `expr`), no `numpy`/`pandas`, and the module allow-list is a plain list. `json` matches the JavaScript node's `$json` (first item in all-items mode).

<!-- BEGIN GENERATED: engi.code.python -->
**Type:** `engi.code.python` · **Version:** 1 · **Group:** `transform` · **Display name:** Code (Python)

Run Python in a separate process to transform items.

**Inputs**

| Index | Kind | Display name | Required |
|---|---|---|---|
| 0 | `main` | main | no |

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

None.

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `mode` | Mode | `options` | `runOnceForAllItems` | no | `runOnceForAllItems` (Run once for all items)<br>`runOnceForEachItem` (Run once for each item) | always | — |
| `pythonCode` | Python | `code` | `# items is a list of {"json": ..., "binary": ...} dicts. Return a list of items. return items` | yes | — | always | Function body. In scope: items, item, json, item_index, run_index, execution, workflow, expr(expression), print. |
<!-- END GENERATED: engi.code.python -->
