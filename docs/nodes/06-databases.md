# 6. Databases

SQL and NoSQL data stores.

## Postgres

Reads and writes a Postgres database. Use it when the data you need is in a table: look up a customer before sending an email, record a result, or sync rows from an API.


**Behaviour**

- **Parameterised only.** Values never get pasted into SQL text. In Execute Query you write placeholders (`$1, $2, ...`) and pass the values in `queryParams`; the other operations build the statement for you, bind every value, and quote table and column names as identifiers.
- **Operations.** Select reads rows (optional columns, conditions, sort, limit). Insert writes one row per input item. Update changes the rows that match `matchColumns`. Upsert inserts, or updates the non-match columns when the row exists. Delete removes rows by condition.
- **Writing rows.** With `dataMode` = `autoMap` the item's JSON fields become the columns (sorted by name). With `define` you list column/value pairs yourself. For Update, the match columns are taken out of that data and form the `WHERE`; every match column must have a value, otherwise the node fails. A `null` match value compares with `IS NULL`.
- **Refuses to empty a table.** Delete with no condition fails, and Update with no match column fails. Both are checked before anything is sent.
- **Plan first, then run.** Every statement for every item is built before the first one runs, so a bad parameter on item 7 does not leave items 1 to 6 applied.
- **Run mode.** Execute Query and Select run once per item by default. `runMode` = `once` runs a single time using the first item. Writes always run per item.
- **Transactions.** `none` (default) autocommits each statement, so an error part-way leaves earlier items applied. `all` wraps the whole run in one transaction. `perItem` commits each item separately. Any error rolls back the open transaction.
- **Pooling.** One connection pool per credential, shared by all workflows and reused across runs. Editing the credential closes the old pool and the next run opens a new one. See [Data Sources 7.5](../07-data-sources.md#75-connection-pooling-database-data-sources).
- **Values out.** Timestamps are RFC 3339 in UTC. JSON columns become JSON values. Binary columns are base64 strings. `BIGINT` and `NUMERIC`/`DECIMAL` columns are numbers unless `numbersAsString` is on, which keeps full precision as strings (a `BIGINT` above 2^53 loses digits as a JavaScript number).
- **Schema.** `schema` defaults to `public`; table and schema are quoted separately.
- **Upsert** needs a unique index or constraint on exactly the match columns (it uses `ON CONFLICT (...)`); with no non-match columns it does nothing on conflict.

**Output.** One item per returned row, paired with the input item that produced it. A statement that returns no rows (an `UPDATE`, `DELETE` or `INSERT` without `RETURNING`) yields one item with `affectedRows`. Insert, Update and Upsert add `RETURNING *`, so you get the written rows back.


**Example**

```json
{
  "operation": "executeQuery",
  "query": "SELECT id, email FROM users WHERE status = $1 AND created_at > $2",
  "queryParams": ["active", "2026-01-01"],
  "runMode": "once"
}
```

Upsert a row from each input item, atomically:

```json
{
  "operation": "upsert",
  "schema": "public",
  "table": "customers",
  "dataMode": "autoMap",
  "matchColumns": "email",
  "transaction": "all"
}
```

**Notes**

- Connection limits and timeouts come from `nodes.database.maxOpenConns`, `maxIdleConns`, `idleTimeout`, `maxLifetime` and `connectTimeout` ([Deployment](../11-deployment.md)). Connections open lazily, so a wrong host shows up as an error on the first query, not when you save the credential.
- Errors name the failing item, for example `item 3: running query: ...`. There is no separate query timeout; the node's execution timeout applies.
- Credential: [Postgres](11-credentials.md#postgres).
- **Notes.** Values reach the database only as bound parameters, a Delete or Update without a condition is refused, and the transaction scope is an explicit setting.

<!-- BEGIN GENERATED: engi.db.postgres -->
**Type:** `engi.db.postgres` · **Version:** 1 · **Group:** `action` · **Display name:** Postgres

Run SQL against Postgres: query, select, insert, update, upsert or delete.

**Inputs**

| Index | Kind | Display name | Required |
|---|---|---|---|
| 0 | `main` | main | no |

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

| Slot | Accepted credential types | Required |
|---|---|---|
| `engi.postgres` | `engi.postgres` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `operation` | Operation | `options` | `executeQuery` | no | `executeQuery` (Execute Query)<br>`select` (Select)<br>`insert` (Insert)<br>`update` (Update)<br>`upsert` (Insert or Update (Upsert))<br>`delete` (Delete) | always | — |
| `schema` | Schema | `string` | `public` | no | — | shown when `operation` = `select` or `insert` or `update` or `upsert` or `delete` | — |
| `table` | Table | `string` | — | yes | — | shown when `operation` = `select` or `insert` or `update` or `upsert` or `delete` | — |
| `query` | Query | `code` | — | yes | — | shown when `operation` = `executeQuery` | SQL text. Reference values only through placeholders ($1, $2, ...), never by pasting data into the text. |
| `queryParams` | Query parameters | `json` | — | no | — | shown when `operation` = `executeQuery` | Array of values bound to the placeholders in order, e.g. [{{ $json.id }}, "open"]. |
| `columns` | Columns | `string` | — | no | — | shown when `operation` = `select` | Comma-separated columns to return. Empty returns all. |
| `combinator` | Combine conditions with | `options` | `and` | no | `and` (AND)<br>`or` (OR) | shown when `operation` = `select` or `delete` | — |
| `where` | Conditions | `fixedCollection` | — | no | — | shown when `operation` = `select` or `delete` | Delete requires at least one condition. |
| ↳ `where.column` | Column | `string` | — | yes | — | always | — |
| ↳ `where.operator` | Operator | `options` | `equal` | no | `equal` (Equals)<br>`notEqual` (Not equals)<br>`greaterThan` (Greater than)<br>`greaterThanOrEqual` (Greater than or equal)<br>`lessThan` (Less than)<br>`lessThanOrEqual` (Less than or equal)<br>`like` (Like)<br>`notLike` (Not like)<br>`ilike` (Like (case-insensitive))<br>`isNull` (Is null)<br>`isNotNull` (Is not null)<br>`in` (In list)<br>`notIn` (Not in list) | always | — |
| ↳ `where.value` | Value | `string` | — | no | — | always | — |
| `sortColumn` | Sort by | `string` | — | no | — | shown when `operation` = `select` | — |
| `sortDirection` | Sort direction | `options` | `asc` | no | `asc` (Ascending)<br>`desc` (Descending) | shown when `operation` = `select` | — |
| `limit` | Limit | `number` | `0` | no | — | shown when `operation` = `select` | Maximum rows to return. 0 means no limit. |
| `dataMode` | Data | `options` | `autoMap` | no | `autoMap` (Map input fields to columns by name)<br>`define` (Define columns below) | shown when `operation` = `insert` or `update` or `upsert` | — |
| `values` | Column values | `fixedCollection` | — | no | — | shown when `dataMode` = `define` | — |
| ↳ `values.name` | Column | `string` | — | yes | — | always | — |
| ↳ `values.value` | Value | `string` | — | no | — | always | — |
| `matchColumns` | Match columns | `string` | — | no | — | shown when `operation` = `update` or `upsert` | Comma-separated columns that identify the row (WHERE for update, conflict key for upsert). Their values come from the data above. |
| `runMode` | Run | `options` | `perItem` | no | `perItem` (Once for each item)<br>`once` (Once for all items) | shown when `operation` = `executeQuery` or `select` | Once runs a single time using the first item; per item runs for every input item. Writes always run per item. |
| `transaction` | Transaction | `options` | `none` | no | `none` (None)<br>`all` (Single transaction for all items)<br>`perItem` (One transaction per item) | always | All wraps every statement of the run in one transaction; per item commits each item on its own. Any error rolls back the open transaction. |
| `numbersAsString` | Bigint and decimal as string | `boolean` | `false` | no | — | always | Return BIGINT and NUMERIC/DECIMAL columns as strings to keep full precision. Timestamps are always RFC 3339 (UTC). |
<!-- END GENERATED: engi.db.postgres -->

## MySQL

Reads and writes a MySQL (or MariaDB-compatible) database. Use it when the data lives in MySQL tables: look up rows, record results, or sync data from an API.


**Behaviour**

- **Parameterised only.** Values never get pasted into SQL text. In Execute Query you write placeholders (`?, ?, ...`) and pass the values in `queryParams`; the other operations build the statement for you, bind every value, and quote table and column names as identifiers.
- **Operations.** Select reads rows (optional columns, conditions, sort, limit). Insert writes one row per input item. Update changes the rows that match `matchColumns`. Upsert inserts, or updates the non-match columns when the row exists. Delete removes rows by condition.
- **Writing rows.** With `dataMode` = `autoMap` the item's JSON fields become the columns (sorted by name). With `define` you list column/value pairs yourself. For Update, the match columns are taken out of that data and form the `WHERE`; every match column must have a value, otherwise the node fails. A `null` match value compares with `IS NULL`.
- **Refuses to empty a table.** Delete with no condition fails, and Update with no match column fails. Both are checked before anything is sent.
- **Plan first, then run.** Every statement for every item is built before the first one runs, so a bad parameter on item 7 does not leave items 1 to 6 applied.
- **Run mode.** Execute Query and Select run once per item by default. `runMode` = `once` runs a single time using the first item. Writes always run per item.
- **Transactions.** `none` (default) autocommits each statement, so an error part-way leaves earlier items applied. `all` wraps the whole run in one transaction. `perItem` commits each item separately. Any error rolls back the open transaction.
- **Pooling.** One connection pool per credential, shared by all workflows and reused across runs. Editing the credential closes the old pool and the next run opens a new one. See [Data Sources 7.5](../07-data-sources.md#75-connection-pooling-database-data-sources).
- **Values out.** Timestamps are RFC 3339 in UTC. JSON columns become JSON values. Binary columns are base64 strings. `BIGINT` and `NUMERIC`/`DECIMAL` columns are numbers unless `numbersAsString` is on, which keeps full precision as strings (a `BIGINT` above 2^53 loses digits as a JavaScript number).
- **Upsert** uses `ON DUPLICATE KEY UPDATE`, so MySQL decides the conflict from whichever unique key collides, not from `matchColumns` alone. Make sure a unique key covers the match columns.

**Output.** One item per returned row, paired with the input item that produced it. A statement that returns no rows (an `UPDATE`, `DELETE` or `INSERT` without `RETURNING`) yields one item with `affectedRows` and, for inserts into an auto-increment table, `lastInsertId`. MySQL has no `RETURNING`, so write operations give you the counts, not the rows; read them back with Select.


**Example**

```json
{
  "operation": "select",
  "table": "orders",
  "columns": "id, total, status",
  "combinator": "and",
  "where": [
    { "column": "status", "operator": "equal", "value": "open" },
    { "column": "total", "operator": "greaterThan", "value": "100" }
  ],
  "sortColumn": "id",
  "sortDirection": "desc",
  "limit": 50
}
```

**Notes**

- Dates are parsed as UTC. Pool settings are the same `nodes.database.*` keys as Postgres ([Deployment](../11-deployment.md)).
- MySQL TLS is set on the credential (`tlsMode`: `disable`, `required`, `skip-verify`, plus an optional CA certificate). See [MySQL](11-credentials.md#mysql).
- Errors name the failing item. There is no `schema` parameter: put the database in the credential.
- **Notes.** Same as [Postgres](#postgres): placeholders only, a Delete or Update needs a condition, and the transaction mode is explicit.

<!-- BEGIN GENERATED: engi.db.mysql -->
**Type:** `engi.db.mysql` · **Version:** 1 · **Group:** `action` · **Display name:** MySQL

Run SQL against MySQL: query, select, insert, update, upsert or delete.

**Inputs**

| Index | Kind | Display name | Required |
|---|---|---|---|
| 0 | `main` | main | no |

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

| Slot | Accepted credential types | Required |
|---|---|---|
| `engi.mysql` | `engi.mysql` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `operation` | Operation | `options` | `executeQuery` | no | `executeQuery` (Execute Query)<br>`select` (Select)<br>`insert` (Insert)<br>`update` (Update)<br>`upsert` (Insert or Update (Upsert))<br>`delete` (Delete) | always | — |
| `table` | Table | `string` | — | yes | — | shown when `operation` = `select` or `insert` or `update` or `upsert` or `delete` | — |
| `query` | Query | `code` | — | yes | — | shown when `operation` = `executeQuery` | SQL text. Reference values only through placeholders (?), never by pasting data into the text. |
| `queryParams` | Query parameters | `json` | — | no | — | shown when `operation` = `executeQuery` | Array of values bound to the placeholders in order, e.g. [{{ $json.id }}, "open"]. |
| `columns` | Columns | `string` | — | no | — | shown when `operation` = `select` | Comma-separated columns to return. Empty returns all. |
| `combinator` | Combine conditions with | `options` | `and` | no | `and` (AND)<br>`or` (OR) | shown when `operation` = `select` or `delete` | — |
| `where` | Conditions | `fixedCollection` | — | no | — | shown when `operation` = `select` or `delete` | Delete requires at least one condition. |
| ↳ `where.column` | Column | `string` | — | yes | — | always | — |
| ↳ `where.operator` | Operator | `options` | `equal` | no | `equal` (Equals)<br>`notEqual` (Not equals)<br>`greaterThan` (Greater than)<br>`greaterThanOrEqual` (Greater than or equal)<br>`lessThan` (Less than)<br>`lessThanOrEqual` (Less than or equal)<br>`like` (Like)<br>`notLike` (Not like)<br>`ilike` (Like (case-insensitive))<br>`isNull` (Is null)<br>`isNotNull` (Is not null)<br>`in` (In list)<br>`notIn` (Not in list) | always | — |
| ↳ `where.value` | Value | `string` | — | no | — | always | — |
| `sortColumn` | Sort by | `string` | — | no | — | shown when `operation` = `select` | — |
| `sortDirection` | Sort direction | `options` | `asc` | no | `asc` (Ascending)<br>`desc` (Descending) | shown when `operation` = `select` | — |
| `limit` | Limit | `number` | `0` | no | — | shown when `operation` = `select` | Maximum rows to return. 0 means no limit. |
| `dataMode` | Data | `options` | `autoMap` | no | `autoMap` (Map input fields to columns by name)<br>`define` (Define columns below) | shown when `operation` = `insert` or `update` or `upsert` | — |
| `values` | Column values | `fixedCollection` | — | no | — | shown when `dataMode` = `define` | — |
| ↳ `values.name` | Column | `string` | — | yes | — | always | — |
| ↳ `values.value` | Value | `string` | — | no | — | always | — |
| `matchColumns` | Match columns | `string` | — | no | — | shown when `operation` = `update` or `upsert` | Comma-separated columns that identify the row (WHERE for update, conflict key for upsert). Their values come from the data above. |
| `runMode` | Run | `options` | `perItem` | no | `perItem` (Once for each item)<br>`once` (Once for all items) | shown when `operation` = `executeQuery` or `select` | Once runs a single time using the first item; per item runs for every input item. Writes always run per item. |
| `transaction` | Transaction | `options` | `none` | no | `none` (None)<br>`all` (Single transaction for all items)<br>`perItem` (One transaction per item) | always | All wraps every statement of the run in one transaction; per item commits each item on its own. Any error rolls back the open transaction. |
| `numbersAsString` | Bigint and decimal as string | `boolean` | `false` | no | — | always | Return BIGINT and NUMERIC/DECIMAL columns as strings to keep full precision. Timestamps are always RFC 3339 (UTC). |
<!-- END GENERATED: engi.db.mysql -->

## Redis

Talks to Redis for caching, counters, simple queues and notifications. Use it to remember a value between runs, rate-limit by incrementing a counter, hand work to another system through a list, or announce an event with Publish.

**Behaviour**

- **Operations.** Get, Set (optional TTL in seconds), Delete, Increment/Decrement (by `amount`), List keys, Hash get/set, List push/pop (left or right end) and Publish.
- **Values.** Set and List push store strings as they are; objects and arrays are stored as JSON text. Redis returns text, so a Get of a JSON value gives you a string that you parse yourself (for example with the Code node).
- **Missing data is not an error.** Get, Hash get with a `field`, and List pop return `value: null` for a missing key or an empty list.
- **List keys uses `SCAN`, never `KEYS`,** so it will not block a busy server. It stops at `nodes.database.redisMaxKeys` (default 1000) and sets `truncated: true` when more keys matched. Narrow the `pattern` if you hit it.
- **Parse first, then run.** Every item is validated before the first command is sent. Commands then run one after another, in item order, with no transaction: an error stops the run and earlier items stay applied.
- **Pooling.** One client per credential, replaced when the credential changes ([Data Sources 7.5](../07-data-sources.md#75-connection-pooling-database-data-sources)).

**Output.** One item per input item. The shape depends on the operation:

| Operation | Output fields |
|---|---|
| Get, List pop, Increment, Decrement | `key`, `value` |
| Set | `key`, `ok` |
| Delete | `key`, `deleted` (count) |
| List push | `key`, `length` |
| Hash get | `key`, `value` (the field's text, or an object of all fields when `field` is empty) |
| Hash set | `key`, `fieldsSet` |
| List keys | `pattern`, `keys`, `truncated` |
| Publish | `channel`, `receivers` |

**Example**

```json
{
  "operation": "set",
  "key": "session:42",
  "value": { "user": "ada", "plan": "pro" },
  "ttl": 3600
}
```

**Notes**

- The cap is `nodes.database.redisMaxKeys`; pool size follows `nodes.database.maxOpenConns` ([Deployment](../11-deployment.md)).
- Increment fails with a Redis error if the existing value is not an integer.
- Credential: [Redis](11-credentials.md#redis) (host, port, database number, optional username/password, TLS).
- **Notes.** The operation set is limited (no server info or subscribe), and key listing is capped and flagged.

<!-- BEGIN GENERATED: engi.db.redis -->
**Type:** `engi.db.redis` · **Version:** 1 · **Group:** `action` · **Display name:** Redis

Read and write Redis keys, hashes and lists, list keys, and publish messages.

**Inputs**

| Index | Kind | Display name | Required |
|---|---|---|---|
| 0 | `main` | main | no |

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

| Slot | Accepted credential types | Required |
|---|---|---|
| `engi.redis` | `engi.redis` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `operation` | Operation | `options` | `get` | no | `get` (Get)<br>`set` (Set)<br>`delete` (Delete)<br>`incr` (Increment)<br>`decr` (Decrement)<br>`keys` (List keys)<br>`hashGet` (Hash: get)<br>`hashSet` (Hash: set)<br>`listPush` (List: push)<br>`listPop` (List: pop)<br>`publish` (Publish) | always | — |
| `key` | Key | `string` | — | yes | — | shown when `operation` = `get` or `set` or `delete` or `incr` or `decr` or `hashGet` or `hashSet` or `listPush` or `listPop` | — |
| `value` | Value | `string` | — | no | — | shown when `operation` = `set` or `listPush` | Strings are stored as is; objects and arrays are stored as JSON. |
| `ttl` | Expire after (seconds) | `number` | `0` | no | — | shown when `operation` = `set` | 0 keeps the key until it is deleted. |
| `amount` | Amount | `number` | `1` | no | — | shown when `operation` = `incr` or `decr` | — |
| `pattern` | Pattern | `string` | `*` | no | — | shown when `operation` = `keys` | Glob pattern, e.g. user:*. Results are capped by nodes.database.redisMaxKeys. |
| `field` | Field | `string` | — | no | — | shown when `operation` = `hashGet` | Empty returns every field of the hash. |
| `fields` | Fields | `json` | — | no | — | shown when `operation` = `hashSet` | Object of field names to values, e.g. {"name": "Ada"}. |
| `side` | End of list | `options` | `left` | no | `left` (Left (head))<br>`right` (Right (tail)) | shown when `operation` = `listPush` or `listPop` | — |
| `channel` | Channel | `string` | — | yes | — | shown when `operation` = `publish` | — |
| `message` | Message | `string` | — | no | — | shown when `operation` = `publish` | — |
<!-- END GENERATED: engi.db.redis -->

## MongoDB

Reads and writes a MongoDB collection. Use it to look up documents, record results, or run an aggregation to summarise data before the next node.

**Behaviour**

- **Operations.** Find, Find one and update, Insert, Update, Delete and Aggregate. `filter`, `projection`, `sort`, `update` and `pipeline` are JSON. [Extended JSON](https://www.mongodb.com/docs/manual/reference/mongodb-extended-json/) works, so `{"_id": {"$oid": "..."}}` matches an ObjectId.
- **Result cap.** Find and Aggregate return at most `nodes.database.mongoMaxResults` documents (default 1000). A smaller `limit` on Find lowers it; `0` means "up to the cap". Aggregate has the cap added as a final `$limit` stage. There is no "truncated" flag, so use a `limit` or filter and paginate if you may have more.
- **Refuses to touch everything.** Update and Delete fail on an empty filter, before anything is sent. Update and Delete change only the first match unless `multiple` is on.
- **Update forms.** `update` is an operator document such as `{"$set": {"status": "done"}}` or an aggregation pipeline array. `upsert` inserts when nothing matches. Find one and update returns the document `after` (default) or `before` the change, and returns no item when nothing matched and `upsert` is off.
- **Insert.** `documents` can be one document or an array. If empty, the input item's JSON is inserted.
- **Per item.** The operation runs once per input item with that item's parameters; there is no transaction.
- **Pooling.** One client per credential, replaced when the credential changes ([Data Sources 7.5](../07-data-sources.md#75-connection-pooling-database-data-sources)).

**Output.** Find and Aggregate give one item per document (in relaxed Extended JSON: an ObjectId appears as `{"$oid": "..."}` and a date as `{"$date": "..."}`, so values can be pasted back into a filter). Insert gives `insertedCount` and `insertedIds`. Update gives `matchedCount`, `modifiedCount`, `upsertedCount`, `upsertedId`. Delete gives `deletedCount`.

**Example**

```json
{
  "operation": "find",
  "collection": "orders",
  "filter": { "status": "open", "total": { "$gt": 100 } },
  "projection": { "customer": 1, "total": 1 },
  "sort": { "createdAt": -1 },
  "limit": 20
}
```

**Notes**

- Config: `nodes.database.mongoMaxResults`, plus the shared pool keys ([Deployment](../11-deployment.md)).
- Credential: [MongoDB](11-credentials.md#mongodb). A connection string, when set, overrides host, port, user and password.
- Common errors: `update needs a non-empty filter`, `pipeline needs at least one stage`, `limit must be a whole number, 0 or more`.
- **Notes.** Update and Delete require a filter, and Find and Aggregate results are capped by config.

<!-- BEGIN GENERATED: engi.db.mongodb -->
**Type:** `engi.db.mongodb` · **Version:** 1 · **Group:** `action` · **Display name:** MongoDB

Query and modify MongoDB documents: find, find one and update, insert, update, delete or aggregate.

**Inputs**

| Index | Kind | Display name | Required |
|---|---|---|---|
| 0 | `main` | main | no |

**Outputs**

| Index | Kind | Display name |
|---|---|---|
| 0 | `main` | main |

**Credentials**

| Slot | Accepted credential types | Required |
|---|---|---|
| `engi.mongodb` | `engi.mongodb` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `operation` | Operation | `options` | `find` | no | `find` (Find)<br>`findOneAndUpdate` (Find one and update)<br>`insert` (Insert)<br>`update` (Update)<br>`delete` (Delete)<br>`aggregate` (Aggregate) | always | — |
| `collection` | Collection | `string` | — | yes | — | always | — |
| `filter` | Filter | `json` | — | no | — | shown when `operation` = `find` or `findOneAndUpdate` or `update` or `delete` | Query document. Update and Delete refuse an empty filter. Extended JSON is accepted, e.g. {"_id": {"$oid": "..."}}. |
| `projection` | Projection | `json` | — | no | — | shown when `operation` = `find` or `findOneAndUpdate` | Fields to include or exclude, e.g. {"name": 1}. |
| `sort` | Sort | `json` | — | no | — | shown when `operation` = `find` or `findOneAndUpdate` | e.g. {"createdAt": -1}. |
| `limit` | Limit | `number` | `0` | no | — | shown when `operation` = `find` | Maximum documents to return. 0 returns up to nodes.database.mongoMaxResults. |
| `update` | Update | `json` | — | no | — | shown when `operation` = `findOneAndUpdate` or `update` | Update operators such as {"$set": {"status": "done"}}, or an aggregation pipeline array. |
| `upsert` | Insert if no match | `boolean` | `false` | no | — | shown when `operation` = `findOneAndUpdate` or `update` | — |
| `returnDocument` | Return | `options` | `after` | no | `after` (Document after the update)<br>`before` (Document before the update) | shown when `operation` = `findOneAndUpdate` | — |
| `multiple` | Affect all matches | `boolean` | `false` | no | — | shown when `operation` = `update` or `delete` | Off changes only the first matching document. |
| `documents` | Documents | `json` | — | no | — | shown when `operation` = `insert` | A document or an array of documents. Empty inserts the input item's JSON. |
| `pipeline` | Pipeline | `json` | — | yes | — | shown when `operation` = `aggregate` | Array of aggregation stages. Results are capped by nodes.database.mongoMaxResults. |
<!-- END GENERATED: engi.db.mongodb -->

---
**← Previous** [5. HTTP](05-http.md) · **Index** [README](README.md) · **Next →** [7. Streaming](07-streaming.md)
