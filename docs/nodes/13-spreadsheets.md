# 13. Spreadsheets and Tables

Hosted spreadsheets and table databases.

## Google Sheets

Reads and writes Google Sheets through the Sheets API v4 (plain HTTP, no Google SDK). Use it to log results as rows, load a sheet as items, update a row you identify by a key column, clear a range, add a tab or list the tabs of a spreadsheet.

**Authentication.** Two ways, chosen with the `authentication` parameter:

- **OAuth2** (default): a generic [`engi.oauth2`](11-credentials.md#oauth2) credential with the grant type "Authorization code (PKCE)", Google's authorization URL (`https://accounts.google.com/o/oauth2/v2/auth`) and token URL (`https://oauth2.googleapis.com/token`), and the scope `https://www.googleapis.com/auth/spreadsheets`. Engi stores and refreshes the token; the node sends it as `Authorization: Bearer`.
- **Service account**: an [`engi.googleServiceAccount`](11-credentials.md#google-service-account) credential holding the key file's `client_email` and `private_key`. The node signs a short JWT (RS256) with the key, exchanges it at the credential's token URL for an access token (valid one hour, requested once per node run and never stored) and sends that. Share each spreadsheet with the service account's email, or set "Impersonate user" for domain-wide delegation.

**Behaviour**

- **Operations.** Append rows, Read rows, Update rows, Clear range, Create sheet (tab), List sheets (tabs).
- **Spreadsheet.** The ID, or the whole spreadsheet URL (the ID is cut out of it). `sheet` is the tab's name.
- **Header row.** The first row (`headerRow`, default 1) names the columns. Append and Update map each item's JSON keys onto those names; columns an item lacks are written blank, and a key that is not in the header fails the item (set `extraColumns` to `ignore` to drop it instead). `columns` (a JSON object) replaces the item JSON as the data to write.
- **Append rows.** All input items go out in **one** request (Google allows about 60 writes a minute per user, so batching matters). The header is read once. Each output item is the row as written, plus its sheet row number in `rowNumberField` (default `rowNumber`; empty omits it).
- **Read rows.** The first row of the range is the header; each later row becomes an object (short rows are padded with `""`, rows that are completely empty are skipped but keep their numbers, an empty header cell drops that column, and a repeated header name keeps the first column). Without `range` it reads `headerRow` through `headerRow + nodes.google.maxReadRows`; with `range` (A1 notation inside the tab, e.g. `B3:F200`) that range's first row is the header and `headerRow` is ignored. At most `nodes.google.maxReadRows` rows (default 10000) are returned per input item. `valueRenderOption` picks displayed values (default), raw values or formulas.
- **Update rows.** Finds the row either by `rowNumber` (must be below the header) or by a **key column**: the column named in `matchColumn` is read once per run and the first row whose cell equals `matchValue` (default: the item's own value of that column) is updated. Only the columns present in the data are written, one cell range each, in a single `values:batchUpdate` per item. `ifNotFound` decides what happens when no row matches: fail (default), skip the item (passed through unchanged) or append it as a new row (later items of the same run see the new row, so a repeated key updates it).
- **Clear range.** Clears values (formatting stays) of `range`, or of the whole tab when empty.
- **Create sheet / List sheets.** Create adds a tab named `title` and outputs its properties (`sheetId`, `title`, `index`, ...). List outputs one item per tab with its properties.
- **Input mode.** `valueInputOption`: `USER_ENTERED` (default; strings are parsed as if typed, so `"=SUM(A1:A3)"` is a formula and `"3"` a number) or `RAW`.
- **Rate limits.** A `429` is retried after the `Retry-After` wait (one second if absent), up to `nodes.google.maxRetries` times; a longer wait than `nodes.google.maxRetryWait` fails the item.
- **Errors.** A non-2xx answer becomes a node error with the HTTP status, Google's status text and message (for example `PERMISSION_DENIED: ...`) and the input item index. Tokens and keys are scrubbed from messages. Append reports the first item of its batch.

**Output.** Append and Update: one item per input item. Read: one item per row, paired to the input item that asked. Clear: `spreadsheetId`, `clearedRange`. Create: the new tab's properties. List: one item per tab.

**Example**

```json
{
  "operation": "updateRows",
  "spreadsheetId": "1AbC...",
  "sheet": "Orders",
  "matchBy": "column",
  "matchColumn": "orderId",
  "ifNotFound": "append"
}
```

**Notes**

- Config: `nodes.google.sheetsBaseUrl`, `maxRetries`, `maxRetryWait`, `maxReadRows` ([Deployment](../11-deployment.md)).
- Credentials: [OAuth2](11-credentials.md#oauth2) or [Google service account](11-credentials.md#google-service-account).
- **Notes.** Covers header-row mapping, read/append/update/clear/create, append-or-update (`ifNotFound: append`), `USER_ENTERED`/`RAW`, a row number on output, OAuth2 and service-account auth. An append of N items is one request, the row number key is configurable, and a key that is not a header fails by default. Not included: delete rows/columns, delete sheet, the Sheets trigger, combining several filters, locating a spreadsheet or tab from a list (resourceLocator), and cell colouring/formatting options.

<!-- BEGIN GENERATED: engi.google.sheets -->
**Type:** `engi.google.sheets` · **Version:** 1 · **Group:** `action` · **Display name:** Google Sheets

Append, read, update and clear rows of a Google Sheet, create tabs and list them.

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
| `engi.oauth2` | `engi.oauth2` | no |
| `engi.googleServiceAccount` | `engi.googleServiceAccount` | no |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `authentication` | Authentication | `options` | `oauth2` | no | `oauth2` (OAuth2 (engi.oauth2))<br>`serviceAccount` (Service account (engi.googleServiceAccount)) | always | — |
| `operation` | Operation | `options` | `readRows` | no | `appendRows` (Append rows)<br>`readRows` (Read rows)<br>`updateRows` (Update rows)<br>`clearRange` (Clear range)<br>`createSheet` (Create sheet (tab))<br>`listSheets` (List sheets (tabs)) | always | — |
| `spreadsheetId` | Spreadsheet | `string` | — | yes | — | always | Spreadsheet ID, or the full spreadsheet URL. |
| `sheet` | Sheet (tab) name | `string` | — | yes | — | shown when `operation` = `appendRows` or `readRows` or `updateRows` or `clearRange` | — |
| `title` | New sheet name | `string` | — | yes | — | shown when `operation` = `createSheet` | — |
| `headerRow` | Header row | `number` | `1` | no | — | shown when `operation` = `appendRows` or `readRows` or `updateRows` | Row number holding the column names. Ignored by Read rows when a range is set (the range's first row is the header). |
| `range` | Range | `string` | — | no | — | shown when `operation` = `readRows` or `clearRange` | A1 range inside the sheet, e.g. A1:F200. Empty reads (or clears) the whole sheet. |
| `valueRenderOption` | Value format | `options` | `FORMATTED_VALUE` | no | `FORMATTED_VALUE` (As displayed)<br>`UNFORMATTED_VALUE` (Raw values)<br>`FORMULA` (Formulas) | shown when `operation` = `readRows` | — |
| `columns` | Columns (JSON) | `json` | — | no | — | shown when `operation` = `appendRows` or `updateRows` | Object of column name to value. Empty uses the input item's JSON. |
| `extraColumns` | Columns missing from the header | `options` | `error` | no | `error` (Fail)<br>`ignore` (Ignore) | shown when `operation` = `appendRows` or `updateRows` | — |
| `valueInputOption` | Value input | `options` | `USER_ENTERED` | no | `USER_ENTERED` (Parsed like typing in the UI)<br>`RAW` (Stored as given) | shown when `operation` = `appendRows` or `updateRows` | — |
| `matchBy` | Find the row by | `options` | `column` | no | `column` (Value in a key column)<br>`rowNumber` (Row number) | shown when `operation` = `updateRows` | — |
| `rowNumber` | Row number | `number` | — | yes | — | shown when `matchBy` = `rowNumber` and `operation` = `updateRows` | — |
| `matchColumn` | Key column | `string` | — | yes | — | shown when `matchBy` = `column` and `operation` = `updateRows` | Header name of the column to match on. |
| `matchValue` | Key value | `string` | — | no | — | shown when `matchBy` = `column` and `operation` = `updateRows` | Value to find. Empty uses the item's value of the key column. |
| `ifNotFound` | If no row matches | `options` | `error` | no | `error` (Fail)<br>`skip` (Skip the item)<br>`append` (Append as a new row) | shown when `matchBy` = `column` and `operation` = `updateRows` | — |
| `rowNumberField` | Row number field | `string` | `rowNumber` | no | — | shown when `operation` = `appendRows` or `readRows` or `updateRows` | Output field that carries the sheet row number. Empty omits it. |
<!-- END GENERATED: engi.google.sheets -->

## Airtable

Reads and writes Airtable records through the REST API (plain HTTP). Use it to look records up with a formula or view, create or update them in bulk, merge on a key with an upsert, or delete them.

**Authentication.** A personal access token in an [`engi.airtable`](11-credentials.md#airtable) credential, sent as `Authorization: Bearer`. Give the token the scopes `data.records:read` and `data.records:write` (and access to the base).

**Behaviour**

- **Operations.** List records, Get record, Create records, Update records, Upsert records, Delete records. `baseId` is the `app...` ID; `table` is the table name or `tbl...` ID.
- **List records.** Options: `filterByFormula`, `view`, `returnFields` (comma list), `sort` (`[{"field":"Name","direction":"desc"}]`) and `limit`. It follows Airtable's `offset` cursor in pages of `nodes.airtable.pageSize` until the records run out or `limit` is reached; `limit` 0, or a limit above `nodes.airtable.maxListResults` (default 1000), is cut to that cap, so an unbounded list can never run away. Records beyond the cap are not returned and no marker is added; narrow the query (formula, view) or raise the cap.
- **Get record.** One record by `recordId`.
- **Create / Update / Upsert / Delete.** Airtable accepts at most 10 records per write request, so the node groups **consecutive input items that target the same base and table** into requests of `nodes.airtable.batchSize` records (default and maximum 10): 23 items are three requests. Create is a `POST`, Update a `PATCH` by `recordId` (only the fields you send change), Delete a `DELETE` with the ids in the query. Fields come from the `fields` JSON object, or the item's JSON when empty. `typecast` is read from the first item of each batch.
- **Upsert.** A `PATCH` with `performUpsert.fieldsToMergeOn` taken from `mergeOn` (comma list, 1 to 3 fields): records matching on those fields are updated, the rest are created. Each output record gets `created: true/false`. The merge fields are read from the first item of each batch.
- **Rate limits.** A `429` is retried after `Retry-After` (one second if absent), up to `nodes.airtable.maxRetries` times; a longer wait than `nodes.airtable.maxRetryWait` fails the call.
- **Errors.** Airtable's error type and message (`INVALID_REQUEST_UNKNOWN: ...`, `NOT_FOUND`) become a node error with the HTTP status. For a batch the item index is the batch's first item. Earlier batches have already been written when a later one fails.

**Output.** List: one item per record, `{id, createdTime, fields}`, paired to the input item that asked. Get/Create/Update/Upsert: the same shape, one per input item, in order. Delete: `{id, deleted: true}`.

**Example**

```json
{
  "operation": "upsertRecords",
  "baseId": "appXXXXXXXXXXXXXX",
  "table": "Contacts",
  "mergeOn": "Email",
  "typecast": true
}
```

**Notes**

- Config: `nodes.airtable.baseUrl`, `maxRetries`, `maxRetryWait`, `maxListResults`, `pageSize`, `batchSize` ([Deployment](../11-deployment.md)).
- Credential: [Airtable](11-credentials.md#airtable).
- **Notes.** Covers the operation set (search is List records with a formula), `filterByFormula`/view/fields/sort, typecast, upsert on merge fields. Writes are batched ten at a time, output keeps Airtable's `{id, createdTime, fields}` shape, and list size is capped by config. Not included: attachment upload, the Airtable trigger, the legacy API-key credential, and picking bases and tables from a list.

<!-- BEGIN GENERATED: engi.airtable -->
**Type:** `engi.airtable` · **Version:** 1 · **Group:** `action` · **Display name:** Airtable

List, get, create, update, upsert and delete Airtable records.

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
| `engi.airtable` | `engi.airtable` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `operation` | Operation | `options` | `listRecords` | no | `listRecords` (List records)<br>`getRecord` (Get record)<br>`createRecords` (Create records)<br>`updateRecords` (Update records)<br>`upsertRecords` (Upsert records)<br>`deleteRecords` (Delete records) | always | — |
| `baseId` | Base ID | `string` | — | yes | — | always | The app... ID of the base. |
| `table` | Table | `string` | — | yes | — | always | Table ID (tbl...) or name. |
| `recordId` | Record ID | `string` | — | yes | — | shown when `operation` = `getRecord` or `updateRecords` or `deleteRecords` | — |
| `fields` | Fields (JSON) | `json` | — | no | — | shown when `operation` = `createRecords` or `updateRecords` or `upsertRecords` | Object of field name to value. Empty uses the input item's JSON. |
| `typecast` | Typecast | `boolean` | `false` | no | — | shown when `operation` = `createRecords` or `updateRecords` or `upsertRecords` | Let Airtable convert strings to the field type and create missing select options. |
| `mergeOn` | Fields to merge on | `string` | — | yes | — | shown when `operation` = `upsertRecords` | Comma separated field names (up to 3) that identify an existing record. |
| `filterByFormula` | Filter formula | `string` | — | no | — | shown when `operation` = `listRecords` | — |
| `view` | View | `string` | — | no | — | shown when `operation` = `listRecords` | View name or ID. |
| `returnFields` | Return only fields | `string` | — | no | — | shown when `operation` = `listRecords` | Comma separated field names. Empty returns all. |
| `sort` | Sort (JSON) | `json` | — | no | — | shown when `operation` = `listRecords` | Array like [{"field":"Name","direction":"asc"}]. |
| `limit` | Limit | `number` | `0` | no | — | shown when `operation` = `listRecords` | Max records to return. 0 returns up to the configured cap (nodes.airtable.maxListResults). |
<!-- END GENERATED: engi.airtable -->

---
**← Previous** [12. AI](12-ai.md) · **Index** [README](README.md)
