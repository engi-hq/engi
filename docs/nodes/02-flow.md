# 2. Flow

Nodes that route, join, pause or call other workflows.

## IF

**What it does.** Splits items into two branches by testing conditions. Use it for simple yes/no routing; use Switch for more than two outcomes.

**How it behaves**
- Each item is tested on its own; conditions are evaluated per item. Matching items go to output 0 (True), the rest to output 1 (False). Order within each output is preserved.
- `combinator` joins the conditions with AND or OR. Operators are typed (`string:`, number, boolean, date and so on). A value typed into a field is a literal and is converted to the operator's type: "12" becomes 12 for a number operator, and "true" or "false" become booleans. A literal that cannot convert, such as "abc" for a number operator, fails the item and names the condition and value. A value produced by an expression keeps its type, so a mismatch is an error unless `looseTypes` converts it (for example "5" to 5).
- An item with no conditions configured goes to True.
- An evaluation error fails the node and names the item index.
- An output with no items does not run the nodes behind it.

**Output.** The input item, unchanged, on one of the two outputs.

```json
{ "json": { "orderId": 42, "total": 120 } }
```

**Example**

```json
{
  "conditions": [
    { "left": "={{ $json.total }}", "operator": "number:gt", "right": "100" }
  ],
  "combinator": "and"
}
```

**Notes**
- `ignoreCase` applies to string comparisons.
- Notes: Engi's IF has no multi-group condition builder; use `combinator` or chain nodes.

<!-- BEGIN GENERATED: engi.flow.if -->
**Type:** `engi.flow.if` · **Version:** 1 · **Group:** `flow` · **Display name:** IF

Route items to the true or false output depending on conditions.

**Inputs**

| Index | Kind | Display name | Required |
|---|---|---|---|
| 0 | `main` | main | no |

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | True |
| 1 | `main` | False |

**Credentials**

None.

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `conditions` | Conditions | `fixedCollection` | — | no | — | always | Each condition compares a left value with a right value using a typed operator. |
| ↳ `conditions.left` | Value 1 | `string` | — | no | — | always | — |
| ↳ `conditions.operator` | Operator | `options` | `string:equals` | no | `string:equals` (String: equals)<br>`string:notEquals` (String: does not equal)<br>`string:contains` (String: contains)<br>`string:notContains` (String: does not contain)<br>`string:startsWith` (String: starts with)<br>`string:endsWith` (String: ends with)<br>`string:regex` (String: matches regex)<br>`string:notRegex` (String: does not match regex)<br>`string:isEmpty` (String: is empty)<br>`string:isNotEmpty` (String: is not empty)<br>`number:equals` (Number: equals)<br>`number:notEquals` (Number: does not equal)<br>`number:gt` (Number: is greater than)<br>`number:gte` (Number: is greater than or equal to)<br>`number:lt` (Number: is less than)<br>`number:lte` (Number: is less than or equal to)<br>`boolean:isTrue` (Boolean: is true)<br>`boolean:isFalse` (Boolean: is false)<br>`dateTime:equals` (Date: equals)<br>`dateTime:notEquals` (Date: does not equal)<br>`dateTime:after` (Date: is after)<br>`dateTime:afterOrEquals` (Date: is after or equal to)<br>`dateTime:before` (Date: is before)<br>`dateTime:beforeOrEquals` (Date: is before or equal to)<br>`array:contains` (Array: contains)<br>`array:notContains` (Array: does not contain)<br>`array:lengthEquals` (Array: length equals)<br>`array:lengthNotEquals` (Array: length does not equal)<br>`array:lengthGt` (Array: length greater than)<br>`array:lengthGte` (Array: length greater than or equal to)<br>`array:lengthLt` (Array: length less than)<br>`array:lengthLte` (Array: length less than or equal to)<br>`array:isEmpty` (Array: is empty)<br>`array:isNotEmpty` (Array: is not empty)<br>`object:isEmpty` (Object: is empty)<br>`object:isNotEmpty` (Object: is not empty)<br>`exists` (Exists)<br>`notExists` (Does not exist) | always | — |
| ↳ `conditions.right` | Value 2 | `string` | — | no | — | always | — |
| `combinator` | Combine | `options` | `and` | no | `and` (All conditions (AND))<br>`or` (Any condition (OR)) | always | — |
| `ignoreCase` | Ignore case | `boolean` | `false` | no | — | always | Compare strings without regard to letter case. |
| `looseTypes` | Loose type validation | `boolean` | `false` | no | — | always | Also convert values produced by expressions to the operator's type (for example "5" to 5) instead of failing. Typed literals are always converted. |
<!-- END GENERATED: engi.flow.if -->

## Switch

**What it does.** Routes each item to one of several outputs. Use it where IF would need to be chained.

**How it behaves**
- **Rules mode.** Rule N sends matching items to output N. By default an item goes to the first matching rule only; `allMatching` sends it to every matching rule. With no match, `fallback` either drops the item (`none`) or sends it to the Fallback output, which comes after the rule outputs.
- **Expression mode.** `outputIndex` is evaluated per item (normally an expression) and gives the zero-based output. An index outside the declared outputs is an error.
- Each rule has its own conditions and `combinator`. `ignoreCase` and `looseTypes` work as in IF: typed literals (text typed into a rule's value fields) convert to the operator's type, and expression results stay strict unless `looseTypes` converts them.
- More rules than outputs is an error. Outputs without items do not trigger the nodes behind them.

**Output.** The input item, unchanged, on the chosen output(s).

**Example**

```json
{
  "mode": "rules",
  "rules": [
    { "conditions": [{ "left": "={{ $json.tier }}", "operator": "string:equals", "right": "gold" }] },
    { "conditions": [{ "left": "={{ $json.tier }}", "operator": "string:equals", "right": "silver" }] }
  ],
  "fallback": "fallback"
}
```

**Notes**
- The number of outputs is fixed by config: `nodes.switch.maxRules` (default 8) rule outputs plus the Fallback ([Deployment](../11-deployment.md#115-configuration)).
- Notes: the output count is a server setting, not set per node.

<!-- BEGIN GENERATED: engi.flow.switch -->
**Type:** `engi.flow.switch` · **Version:** 1 · **Group:** `flow` · **Display name:** Switch

Route items to different outputs.

**Inputs**

| Index | Kind | Display name | Required |
|---|---|---|---|
| 0 | `main` | main | no |

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | Output 0 |
| 1 | `main` | Output 1 |
| 2 | `main` | Output 2 |
| 3 | `main` | Output 3 |
| 4 | `main` | Output 4 |
| 5 | `main` | Output 5 |
| 6 | `main` | Output 6 |
| 7 | `main` | Output 7 |
| 8 | `main` | Fallback |

**Credentials**

None.

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `mode` | Mode | `options` | `rules` | no | `rules` (Rules)<br>`expression` (Expression (output index)) | always | — |
| `rules` | Rules | `fixedCollection` | — | no | — | shown when `mode` = `rules` | Rule N sends matching items to output N. Each rule holds conditions and how to combine them. |
| ↳ `rules.conditions` | Conditions | `fixedCollection` | — | no | — | always | — |
| ↳ `rules.conditions.left` | Value 1 | `string` | — | no | — | always | — |
| ↳ `rules.conditions.operator` | Operator | `options` | `string:equals` | no | `string:equals` (String: equals)<br>`string:notEquals` (String: does not equal)<br>`string:contains` (String: contains)<br>`string:notContains` (String: does not contain)<br>`string:startsWith` (String: starts with)<br>`string:endsWith` (String: ends with)<br>`string:regex` (String: matches regex)<br>`string:notRegex` (String: does not match regex)<br>`string:isEmpty` (String: is empty)<br>`string:isNotEmpty` (String: is not empty)<br>`number:equals` (Number: equals)<br>`number:notEquals` (Number: does not equal)<br>`number:gt` (Number: is greater than)<br>`number:gte` (Number: is greater than or equal to)<br>`number:lt` (Number: is less than)<br>`number:lte` (Number: is less than or equal to)<br>`boolean:isTrue` (Boolean: is true)<br>`boolean:isFalse` (Boolean: is false)<br>`dateTime:equals` (Date: equals)<br>`dateTime:notEquals` (Date: does not equal)<br>`dateTime:after` (Date: is after)<br>`dateTime:afterOrEquals` (Date: is after or equal to)<br>`dateTime:before` (Date: is before)<br>`dateTime:beforeOrEquals` (Date: is before or equal to)<br>`array:contains` (Array: contains)<br>`array:notContains` (Array: does not contain)<br>`array:lengthEquals` (Array: length equals)<br>`array:lengthNotEquals` (Array: length does not equal)<br>`array:lengthGt` (Array: length greater than)<br>`array:lengthGte` (Array: length greater than or equal to)<br>`array:lengthLt` (Array: length less than)<br>`array:lengthLte` (Array: length less than or equal to)<br>`array:isEmpty` (Array: is empty)<br>`array:isNotEmpty` (Array: is not empty)<br>`object:isEmpty` (Object: is empty)<br>`object:isNotEmpty` (Object: is not empty)<br>`exists` (Exists)<br>`notExists` (Does not exist) | always | — |
| ↳ `rules.conditions.right` | Value 2 | `string` | — | no | — | always | — |
| ↳ `rules.combinator` | Combine | `options` | `and` | no | `and` (All conditions (AND))<br>`or` (Any condition (OR)) | always | — |
| `outputIndex` | Output index | `number` | `0` | no | — | shown when `mode` = `expression` | Zero-based output for the item, usually an expression. |
| `fallback` | No match | `options` | `none` | no | `none` (Drop the item)<br>`fallback` (Send to the fallback output) | shown when `mode` = `rules` | — |
| `allMatching` | Send to all matching outputs | `boolean` | `false` | no | — | shown when `mode` = `rules` | By default an item goes to the first matching rule only. |
| `ignoreCase` | Ignore case | `boolean` | `false` | no | — | shown when `mode` = `rules` | — |
| `looseTypes` | Loose type validation | `boolean` | `false` | no | — | shown when `mode` = `rules` | — |
<!-- END GENERATED: engi.flow.switch -->

## Merge

**What it does.** Joins the items of several branches back into one stream. Use it after a split to recombine, or to enrich one list with another.

**How it behaves**
- **Waiting.** Policy is `waitAll`: the node runs once every connected input has data. Inputs that nothing is wired to are not waited for. If a branch never delivers (for example an IF output that stays empty), the Merge does not run on partial data; it is dropped when the run ends. Items arriving twice on the same input are appended.
- **Modes.**
  - `append`: all items of input 1, then input 2, and so on.
  - `combineByFields`: joins on `matchFields` (a field of input 1 and, if named differently, of input 2). `joinMode` picks inner, non-matches only, outer, or left/right enrichment. `multipleMatches` chooses all or first. Items lacking the field, or with null, never match.
  - `combineByPosition`: pairs item i of each input. Only the shortest length is kept unless `includeUnpaired` is on.
  - `chooseBranch`: outputs the items of one input (`chooseInput`, 1-based) and ignores the others.
- **Field clashes** (`onClash`): `preferInput1`, `preferInput2` (default), or `addSuffix`, which keeps both with an input-number suffix.
- Merged items keep a link to their source items, so `$("Node").item` still resolves.

**Output**

```json
{ "json": { "id": 1, "name": "Ana", "plan": "pro" } }
```

**Example**

```json
{
  "mode": "combineByFields",
  "matchFields": [{ "field1": "id", "field2": "customerId" }],
  "joinMode": "enrichInput1",
  "multipleMatches": "first",
  "onClash": "preferInput2"
}
```

**Notes**
- Number of inputs: `nodes.merge.maxInputs`, default 2 ([Deployment](../11-deployment.md#115-configuration)).
- Do not feed a Merge from a loop and expect separate runs per pass: waiting data is kept per node.
- Notes: Engi's Merge does not run with what arrived when a connected input never receives data.

<!-- BEGIN GENERATED: engi.flow.merge -->
**Type:** `engi.flow.merge` · **Version:** 1 · **Group:** `flow` · **Display name:** Merge

Combine the items of several inputs.

**Merge policy:** `waitAll`

**Inputs**

| Index | Kind | Display name | Required |
|---|---|---|---|
| 0 | `main` | Input 1 | no |
| 1 | `main` | Input 2 | no |

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

None.

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `mode` | Mode | `options` | `append` | no | `append` (Append)<br>`combineByFields` (Combine by matching fields)<br>`combineByPosition` (Combine by position)<br>`chooseBranch` (Choose branch) | always | — |
| `matchFields` | Fields to match | `fixedCollection` | — | no | — | shown when `mode` = `combineByFields` | Field of input 1 and, when named differently, of input 2. |
| ↳ `matchFields.field1` | Input 1 field | `string` | — | yes | — | always | — |
| ↳ `matchFields.field2` | Input 2 field | `string` | — | no | — | always | Defaults to the input 1 field. |
| `joinMode` | Output | `options` | `keepMatches` | no | `keepMatches` (Matches only (inner join))<br>`keepNonMatches` (Non-matches only)<br>`keepEverything` (Everything (outer join))<br>`enrichInput1` (All of input 1, enriched (left join))<br>`enrichInput2` (All of input 2, enriched (right join)) | shown when `mode` = `combineByFields` | — |
| `multipleMatches` | Multiple matches | `options` | `all` | no | `all` (Include all matches)<br>`first` (Include first match only) | shown when `mode` = `combineByFields` | — |
| `onClash` | When fields clash | `options` | `preferInput2` | no | `preferInput1` (Prefer input 1)<br>`preferInput2` (Prefer input 2)<br>`addSuffix` (Keep both with input number suffix) | shown when `mode` = `combineByFields` or `combineByPosition` | — |
| `includeUnpaired` | Include unpaired items | `boolean` | `false` | no | — | shown when `mode` = `combineByPosition` | When inputs have different lengths, keep the items that have no partner. |
| `chooseInput` | Input to output | `number` | `1` | no | — | shown when `mode` = `chooseBranch` | Number of the input (1-based) whose items are output. |
<!-- END GENERATED: engi.flow.merge -->

## Wait

**What it does.** Pauses the execution, then continues with the same items. Use it for delays, "act at 9:00 tomorrow", or to park a run until an external system calls back.

**How it behaves**
- The execution is saved with status `waiting` and no worker is held. Waiting survives restarts and failovers.
- **`interval`**: resumes after `amount` `unit`. **`specificTime`**: resumes at `dateTime` (ISO-8601; read as UTC without an offset). A time that has already passed continues immediately. Amounts cannot be negative or longer than about 100 years.
- Timed waits are picked up by the wait scheduler every `worker.waiterInterval` (default 1 s), up to `worker.waiterBatchSize` per tick, so the resume can be late by about that interval.
- **`webhook`**: resumes when `GET` or `POST` hits `/hook-wait/<executionId>.<secret>` (prefix from `webhook.waitBasePath`). The secret belongs to the execution, not to the node: it is drawn when the execution starts, stored with the execution state and shared by every webhook Wait node the execution reaches. So the same `$execution.resumeUrl` ([expressions](../05-expressions.md)) can be sent before the Wait node runs and still resumes it. The token is compared in constant time; unknown, wrong and already used tokens all answer 404. The call's `{headers, query, body}` replaces the node's output items. The node has no timeout option for this mode.
- **Security.** The resume URL is a bearer link. Anyone who holds it can resume a waiting webhook Wait node of that execution, and every node of the execution can read it through `$execution.resumeUrl`. Send it only to whoever should resume the run.
- A webhook trigger in `lastNode` mode that reaches a wait answers 202.

**Output.** The input items on resume, or for the webhook mode the resume request.

```json
{ "json": { "headers": {}, "query": { "ok": "1" }, "body": { "approved": true } } }
```

**Example**

```json
{ "resume": "interval", "amount": 15, "unit": "minutes" }
```

**Notes**
- Parked executions are stored in the database; keep that in mind for long waits.
- Notes: the resume URL carries a random secret, not a signature; no resume form or "after time limit" option for the webhook mode.

<!-- BEGIN GENERATED: engi.flow.wait -->
**Type:** `engi.flow.wait` · **Version:** 1 · **Group:** `flow` · **Display name:** Wait

Pause the execution, then continue with the same items.

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
| `resume` | Resume | `options` | `interval` | no | `interval` (After a time interval)<br>`specificTime` (At a specific date and time)<br>`webhook` (On webhook call) | always | — |
| `amount` | Amount | `number` | `1` | no | — | shown when `resume` = `interval` | — |
| `unit` | Unit | `options` | `seconds` | no | `seconds` (Seconds)<br>`minutes` (Minutes)<br>`hours` (Hours)<br>`days` (Days) | shown when `resume` = `interval` | — |
| `dateTime` | Date and time | `dateTime` | — | no | — | shown when `resume` = `specificTime` | ISO-8601; without an offset it is read as UTC. |
<!-- END GENERATED: engi.flow.wait -->

## Execute Workflow

**What it does.** Runs another workflow with this node's items and, optionally, uses its result. Use it to reuse logic or to keep large workflows small.

**How it behaves**
- The target (`workflowId`) must start with an [Execute Workflow Trigger](01-triggers.md#execute-workflow-trigger). The child gets this node's input items, all in one call.
- **`waitForResult` on** (default): the child runs inline in the parent's worker slot (so slots cannot deadlock) and the node continues with the output of the child's last node. If the child does not end with status `success`, this node fails with the child's execution id and error.
- **Off** (fire-and-forget): the child is queued as its own execution, and the input items continue unchanged right away. Its failure does not affect the parent.
- The child records the parent's execution id. Its execution is separate and appears in the executions list.
- **Depth limit.** Nesting deeper than `engine.maxSubworkflowDepth` (default 10) fails with a clear error. This is what stops a workflow that calls itself.

**Output.** With wait: the child's last-node items. Without: the input items.

```json
{ "json": { "orderId": 42, "invoiceUrl": "https://example.com/inv/42" } }
```

**Example**

```json
{ "workflowId": "01J9ZQ8XK3M7", "waitForResult": true }
```

**Notes**
- Starting the child can fail (unknown workflow id, child lacks the trigger); the error appears as "starting sub-workflow failed".
- A waiting child is not re-queued on its own after a crash; the parent runs again and calls it again, so make children safe to repeat.
- Config: `engine.maxSubworkflowDepth` ([Deployment](../11-deployment.md#115-configuration)).
- Notes: the child is referenced by workflow id only; there is no inline-JSON or source-parameter option.

<!-- BEGIN GENERATED: engi.flow.executeWorkflow -->
**Type:** `engi.flow.executeWorkflow` · **Version:** 1 · **Group:** `flow` · **Display name:** Execute Workflow

Run another workflow with these items.

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
| `workflowId` | Workflow ID | `string` | — | yes | — | always | The workflow to run. It must start with an Execute Workflow Trigger node. |
| `waitForResult` | Wait for result | `boolean` | `true` | no | — | always | Continue with the output of the child's last node. When off, the child runs on its own and the input items continue unchanged. |
<!-- END GENERATED: engi.flow.executeWorkflow -->

## Respond to Webhook

**What it does.** Sends the HTTP reply for the webhook call that started the execution, then lets the workflow carry on. Use it when the answer depends on work done inside the workflow, or when you want to reply early and keep processing.

**How it behaves**
- Works only when the starting [Webhook](01-triggers.md#webhook) uses `responseMode: responseNode`. In other modes, or for other triggers, the reply has no effect.
- The first Respond to Webhook that runs wins; the node also passes its input items on unchanged, so downstream nodes keep working after the caller has its answer.
- `respondWith`: `firstItem` (JSON of the first item, `{}` if none), `allItems` (JSON array), `text`, `json` (the `responseBody` must be valid JSON; an expression returning an object is also accepted), or `noData`. `Content-Type` is set to match unless you set it in `headers`.
- `responseCode` must be an integer from 100 to 599 (default 200).
- If the workflow ends without reaching this node the caller gets 500 (202 if the run is waiting). If nothing is sent within `webhook.responseTimeout` the caller gets 504 and the execution keeps running. The reply travels over the bus, so the instance holding the connection need not be the one running the node.

**Output.** The input items, unchanged.

**Example**

```json
{
  "respondWith": "json",
  "responseBody": "={{ { ok: true, id: $json.id } }}",
  "responseCode": 201,
  "headers": [{ "name": "X-Request-Id", "value": "={{ $json.id }}" }]
}
```

**Notes**
- Config: `webhook.responseTimeout` ([Deployment](../11-deployment.md#115-configuration)).
- On a repeated call with the same idempotency key, the custom reply cannot be replayed (it is not stored); the caller gets the original last-node output instead.
- Notes: the reply is a single call per execution; the first one to run is used.

<!-- BEGIN GENERATED: engi.flow.respondToWebhook -->
**Type:** `engi.flow.respondToWebhook` · **Version:** 1 · **Group:** `flow` · **Display name:** Respond to Webhook

Answer the webhook call that started this execution, then continue.

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
| `respondWith` | Respond with | `options` | `firstItem` | no | `firstItem` (First incoming item)<br>`allItems` (All incoming items)<br>`text` (Text)<br>`json` (JSON)<br>`noData` (No data) | always | — |
| `responseBody` | Body | `string` | — | no | — | shown when `respondWith` = `text` or `json` | Text, or JSON for the JSON option. May be an expression. |
| `responseCode` | Status code | `number` | `200` | no | — | always | — |
| `headers` | Headers | `fixedCollection` | — | no | — | always | List of {name, value} response headers. |
<!-- END GENERATED: engi.flow.respondToWebhook -->

## No Operation

**What it does.** Passes items through unchanged. Use it as a named junction to keep a canvas tidy, as a stable merge point, or as a placeholder while you build.

**How it behaves**
- Each input item is output as it is, in order, with its link to the source item kept.
- It has no parameters and cannot fail on its own.

**Output**

```json
{ "json": { "id": 1 } }
```

**Example.** No parameters.

<!-- BEGIN GENERATED: engi.flow.noop -->
**Type:** `engi.flow.noop` · **Version:** 1 · **Group:** `flow` · **Display name:** No Operation

Passes the items through unchanged.

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

None.
<!-- END GENERATED: engi.flow.noop -->

---
**← Previous** [1. Triggers](01-triggers.md) · **Index** [README](README.md) · **Next →** [3. Transform](03-transform.md)
