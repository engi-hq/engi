# 3. Transform

Nodes that reshape items.

## Edit Fields (Set)

Adds, changes or removes fields on every item. Use it to rename or reshape data, set constants, or build a clean object to hand to the next node.

**Behaviour**

- **Manual mode** sets each row of `fields` in order; later rows can overwrite earlier ones. **JSON mode** sets every top-level key of `jsonOutput` (an object, or a string holding a JSON object).
- With `dotNotation` on, a name like `address.city` writes into a nested object and creates the parents as needed. Off, the whole name is one key.
- `type` converts the value: `string`, `number` (a numeric string such as `"5"` becomes 5), `boolean`, `array` and `object` (a JSON string is parsed). `auto` keeps the value as it is. A value that cannot be converted fails the node.
- With `keepOtherFields` off, the item keeps only the fields you set, and its binary data is dropped.
- Values are usually [expressions](../05-expressions.md), evaluated once per item.

**Output**: one item per input item, in the same order.

```json
{ "name": "Ada", "age": 36, "address": { "city": "London" } }
```

**Example**: add a numeric `age` and a nested city.

```json
{
  "mode": "manual",
  "fields": [
    { "name": "age", "type": "number", "value": "{{ $json.ageText }}" },
    { "name": "address.city", "type": "string", "value": "London" }
  ],
  "keepOtherFields": true,
  "dotNotation": true
}
```

**Notes**

- Errors name the item and the field, for example `item 2` and `"age": cannot convert ...`.
- **Notes:** there are no per-run "include" modes beyond keep/drop of other fields, and binary data is dropped (not kept) when `keepOtherFields` is off.

<!-- BEGIN GENERATED: engi.transform.set -->
**Type:** `engi.transform.set` · **Version:** 1 · **Group:** `transform` · **Display name:** Edit Fields (Set)

Add, change or remove fields of each item.

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
| `mode` | Mode | `options` | `manual` | no | `manual` (Manual mapping)<br>`json` (JSON) | always | — |
| `fields` | Fields to set | `fixedCollection` | — | no | — | shown when `mode` = `manual` | Each field has a name (a dot path such as address.city when dot notation is on), a type and a value. |
| ↳ `fields.name` | Name | `string` | — | yes | — | always | — |
| ↳ `fields.type` | Type | `options` | `auto` | no | `auto` (Auto (keep as is))<br>`string` (String)<br>`number` (Number)<br>`boolean` (Boolean)<br>`array` (Array)<br>`object` (Object) | always | — |
| ↳ `fields.value` | Value | `string` | — | no | — | always | — |
| `jsonOutput` | JSON | `json` | `{}` | no | — | shown when `mode` = `json` | An object whose top-level keys are set on each item. |
| `keepOtherFields` | Keep other fields | `boolean` | `true` | no | — | always | Keep the fields the item already has. When off, the item holds only the fields set here. |
| `dotNotation` | Dot notation | `boolean` | `true` | no | — | always | Treat a.b as a nested path. When off, the whole name is one key. |
<!-- END GENERATED: engi.transform.set -->

## Filter

Splits items into those that match your conditions and those that do not. Use it to drop unwanted items, or send the rest down a second branch.

**Behaviour**

- Each condition is `left`, a typed `operator` (for example `string:contains`, `number:gt`, `dateTime:before`, `array:isEmpty`, `exists`) and `right`. Unary operators such as `string:isEmpty` ignore `right`.
- `combinator` is `and` (all conditions) or `or` (any). It is evaluated per item.
- `ignoreCase` makes string comparisons case-insensitive. Typed literals are always converted to the operator's type (`"5"` becomes 5 for a number operator). With `looseTypes` on, values produced by expressions are converted too, instead of failing the item.
- Output 0 (**Kept**) gets matching items. Output 1 (**Discarded**) gets the rest only when `keepDiscarded` is on; otherwise they are dropped.
- Items pass through unchanged and keep their order.

**Output**: the original items, split over two ports.

**Example**: keep orders above 100.

```json
{
  "conditions": [
    { "left": "{{ $json.total }}", "operator": "number:gt", "right": "100" }
  ],
  "combinator": "and",
  "keepDiscarded": true
}
```

**Notes**

- A value produced by an expression that does not match the operator's type (for example a text value against a `number:` operator) fails with the item index unless `looseTypes` is on.
- A literal that cannot convert (for example `abc` against a `number:` operator) fails with the item index, naming the condition and the value, whatever `looseTypes` is.
- **Notes:** the operator list is Engi's own; check the Options column above.

<!-- BEGIN GENERATED: engi.transform.filter -->
**Type:** `engi.transform.filter` · **Version:** 1 · **Group:** `transform` · **Display name:** Filter

Keep only the items that match the conditions.

**Inputs**

| Index | Kind | Display name | Required |
|---|---|---|---|
| 0 | `main` | main | no |

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | Kept |
| 1 | `main` | Discarded |

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
| `keepDiscarded` | Send discarded items to the second output | `boolean` | `false` | no | — | always | — |
<!-- END GENERATED: engi.transform.filter -->

## Sort

Reorders items by one or more fields. Use it before Limit to take the top N, or before a node that expects ordered input.

**Behaviour**

- `keys` apply in order; a later key only breaks ties of the earlier ones. The sort is stable, so equal items keep their original order.
- Each key has `field` (a dot path), `order` (`asc`/`desc`), `type` and `ignoreCase`. `type` is `auto`, `string`, `number` or `dateTime`. With `auto`, strings compare as text, numbers (and numeric values) as numbers, and mixed values sort numbers before strings before everything else.
- Items where the field is missing or null sort last, in both directions.
- A value that cannot be converted to an explicit `number` or `dateTime` type fails the node.

**Output**: the same items in the new order.

**Example**: newest first, then by name.

```json
{
  "keys": [
    { "field": "createdAt", "order": "desc", "type": "dateTime" },
    { "field": "name", "order": "asc", "type": "string", "ignoreCase": true }
  ]
}
```

**Notes**

- Sort needs at least one key, and every key needs a `field`.
- **Notes:** multi-key sorting is built in, and sorting by a JavaScript expression is not offered; use [Code](04-code.md) for that.

<!-- BEGIN GENERATED: engi.transform.sort -->
**Type:** `engi.transform.sort` · **Version:** 1 · **Group:** `transform` · **Display name:** Sort

Order items by one or more fields. Items missing a field sort last.

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
| `keys` | Sort by | `fixedCollection` | — | yes | — | always | Keys apply in order; later keys break ties. |
| ↳ `keys.field` | Field | `string` | — | yes | — | always | — |
| ↳ `keys.order` | Order | `options` | `asc` | no | `asc` (Ascending)<br>`desc` (Descending) | always | — |
| ↳ `keys.type` | Type | `options` | `auto` | no | `auto` (Auto)<br>`string` (String)<br>`number` (Number)<br>`dateTime` (Date) | always | — |
| ↳ `keys.ignoreCase` | Ignore case | `boolean` | `false` | no | — | always | — |
<!-- END GENERATED: engi.transform.sort -->

## Limit

Keeps only the first or last N items. Use it to cap a list, or after Sort to take the top results.

**Behaviour**

- `keep` is `first` (default) or `last`. `maxItems` defaults to 1; 0 keeps nothing; a negative number is an error.
- If there are fewer items than `maxItems`, all of them pass. Order is preserved.

**Output**: up to `maxItems` of the original items, unchanged.

**Example**: the three most recent items after a descending Sort.

```json
{ "maxItems": 3, "keep": "first" }
```

**Notes**

- `maxItems` is read once for the whole batch, so an expression there uses the first item.

<!-- BEGIN GENERATED: engi.transform.limit -->
**Type:** `engi.transform.limit` · **Version:** 1 · **Group:** `transform` · **Display name:** Limit

Keep only the first or last items.

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
| `maxItems` | Max items | `number` | `1` | no | — | always | How many items to keep. |
| `keep` | Keep | `options` | `first` | no | `first` (First items)<br>`last` (Last items) | always | — |
<!-- END GENERATED: engi.transform.limit -->

## Split Out

Turns an array field into separate items, one per element. Use it after a node that returns a list inside one item (for example an API response with an `items` array).

**Behaviour**

- `fields` is a comma-separated list of paths. With one field, each element becomes an item. With several, elements are paired by position and the shortest lists are left empty for the missing positions.
- A field that exists but is not an array counts as a one-element array. A missing field fails the node.
- With one field and `destinationField` empty, object elements are spread into the item (their keys become fields); other elements are stored under the field's last path segment (`a.b.c` becomes `c`). With a `destinationField`, each element is stored under that name.
- `include` controls the other fields: `none` (default), `all` (everything except the split fields) or `selected` (the paths in `fieldsToInclude`).

**Output**

```json
{ "id": 1, "sku": "A-1" }
```

**Example**: one item per order line, carrying the order id.

```json
{ "fields": "lines", "include": "selected", "fieldsToInclude": "orderId" }
```

**Notes**

- An empty array produces no items for that input item.
- **Notes:** a missing field is an error and is not skipped silently.

<!-- BEGIN GENERATED: engi.transform.splitOut -->
**Type:** `engi.transform.splitOut` · **Version:** 1 · **Group:** `transform` · **Display name:** Split Out

Turn the elements of an array field into separate items.

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
| `fields` | Fields to split out | `string` | — | yes | — | always | Comma-separated field paths. With several fields, their elements are paired by position. A field that is not an array counts as a one-element array. |
| `destinationField` | Output field | `string` | — | no | — | always | Name for the elements (single field only). Object elements are spread into the item when empty. |
| `include` | Other fields | `options` | `none` | no | `none` (None)<br>`all` (All other fields)<br>`selected` (Selected fields) | always | — |
| `fieldsToInclude` | Fields to include | `string` | — | no | — | shown when `include` = `selected` | Comma-separated field paths copied onto every produced item. |
<!-- END GENERATED: engi.transform.splitOut -->

## Aggregate

Combines many items into one. Use it to build a single list for an email body, a bulk API call, or a summary.

**Behaviour**

- **Individual fields** (`mode`): for each row of `fields`, collects that field's value from every item into an array stored under `outputName` (default: the field name). Items without the field are skipped, or become `null` with `keepMissing`. With `mergeLists`, array values are concatenated instead of nested.
- **All item data**: puts a copy of every item into one array under `destinationField` (default `data`). `include` is `all`, `selected` or `except`, using the paths in `fieldList`.
- Always emits exactly one item, even for empty input, which then holds an empty array.

**Output**

```json
{ "emails": ["a@example.com", "b@example.com"] }
```

**Example**

```json
{
  "mode": "individualFields",
  "fields": [ { "field": "email", "outputName": "emails" } ],
  "keepMissing": false
}
```

**Notes**

- Individual-field output names are literal keys, not dot paths.
- **Notes:** there is no binary merging or "Keep only unique" option.

<!-- BEGIN GENERATED: engi.transform.aggregate -->
**Type:** `engi.transform.aggregate` · **Version:** 1 · **Group:** `transform` · **Display name:** Aggregate

Combine many items into one.

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
| `mode` | Aggregate | `options` | `individualFields` | no | `individualFields` (Individual fields)<br>`allItemData` (All item data (into one list)) | always | — |
| `fields` | Fields to aggregate | `fixedCollection` | — | no | — | shown when `mode` = `individualFields` | — |
| ↳ `fields.field` | Input field | `string` | — | yes | — | always | — |
| ↳ `fields.outputName` | Output name | `string` | — | no | — | always | Defaults to the input field name. |
| `mergeLists` | Merge array values | `boolean` | `false` | no | — | shown when `mode` = `individualFields` | When the field holds arrays, concatenate them instead of nesting. |
| `keepMissing` | Keep missing values | `boolean` | `false` | no | — | shown when `mode` = `individualFields` | Add null for items without the field instead of skipping them. |
| `destinationField` | Output field | `string` | `data` | no | — | shown when `mode` = `allItemData` | — |
| `include` | Include | `options` | `all` | no | `all` (All fields)<br>`selected` (Selected fields)<br>`except` (All fields except) | shown when `mode` = `allItemData` | — |
| `fieldList` | Fields | `string` | — | no | — | shown when `include` = `selected` or `except` | Comma-separated field paths. |
<!-- END GENERATED: engi.transform.aggregate -->

---
**← Previous** [2. Flow](02-flow.md) · **Index** [README](README.md) · **Next →** [4. Code](04-code.md)
