# 5. HTTP

Call HTTP APIs.

## HTTP Request

Calls an HTTP endpoint once for every input item and emits the response. Use it for any REST API that has no dedicated node, or to download and upload files.

**Behaviour**

- `url` must be `http` or `https`. `query` pairs are appended (repeated names repeat the parameter) and `headers` are set on the request. Every parameter may be an expression, so each item can call a different URL.
- **Body** (`bodyType`, for POST, PUT, PATCH and DELETE): `json` (object, array or JSON text; sets `application/json`), `form` (`application/x-www-form-urlencoded`), `raw` (text with your `rawContentType`) or `binary` (streams the item's binary field named by `inputBinaryField`, default `data`, with its MIME type).
- **Response** (`responseFormat`): `auto` parses JSON when the `Content-Type` contains "json", otherwise text; `json` and `text` force a mode; `base64` returns the bytes as base64 text; `binary` stores the body in the blob store and puts a reference in the binary field `outputBinaryField` (default `data`). The file name comes from `Content-Disposition`, else the URL path.
- **Output shape**: a JSON object response becomes one item. A JSON array becomes one item per element (non-object elements become `{ "data": value }`). Any other body (text, number) becomes `{ "data": body }`. With `fullResponse`, the item is `{ statusCode, statusMessage, headers, body }` instead (header names lower-cased, repeated values joined with `, `); arrays are then not split.
- `failOnError` (default on) fails the node on a non-2xx status and quotes up to 512 characters of the response body. Turn it off to handle 4xx/5xx yourself, usually together with `fullResponse`.
- `followRedirects` (default on) follows redirects up to the configured maximum; off, you get the 3xx response itself.
- `timeoutMs` sets a per-request deadline; 0 uses the configured default.
- **Authentication**: `none`; `predefined` uses whichever credential you bind to the `predefined` slot (any credential type that knows how to authenticate); `generic` picks one of Basic Auth, Header Auth, Bearer Token, API Key in Query or OAuth2 and uses the credential bound to that slot. Credentials are applied after your headers and query, so they win on conflict. See [Credential Types](11-credentials.md).

**Output** (default):

```json
{ "id": 42, "title": "Hello" }
```

With `fullResponse`:

```json
{ "statusCode": 201, "statusMessage": "Created", "headers": { "content-type": "application/json" }, "body": { "id": 42 } }
```

**Example**: create a record with a bearer token.

```json
{
  "method": "POST",
  "url": "https://api.example.com/items",
  "headers": [ { "name": "X-Request-Id", "value": "{{ $json.id }}" } ],
  "authentication": "generic",
  "genericAuthType": "engi.http.bearer",
  "bodyType": "json",
  "jsonBody": { "title": "{{ $json.title }}" },
  "responseFormat": "auto",
  "failOnError": true,
  "timeoutMs": 5000
}
```

**Notes**

- Limits ([configuration](../11-deployment.md)): `httpClient.timeout` (30s default), `httpClient.maxRedirects` (10), `httpClient.maxResponseBytes` (10 MiB, the largest body read), `httpClient.proxyUrl`, and TLS options such as `httpClient.tlsCaFile`.
- Common errors: `url is required`, `url ... must use http or https`, `request failed with status 404 Not Found` (with the body excerpt), `request failed` (connection or timeout; the description has the cause), `the input item has no binary field "data"`, `response is not valid JSON` when `responseFormat` is `json`.
- The node does not paginate; loop with [Flow](02-flow.md) nodes or a Code node.
- **Notes:** no pagination, batching or retry options on the node, a limited set of body types (no multipart form), and no "ignore SSL issues" toggle; TLS trust is set server-wide.

<!-- BEGIN GENERATED: engi.http.request -->
**Type:** `engi.http.request` · **Version:** 1 · **Group:** `action` · **Display name:** HTTP Request

Call an HTTP endpoint once per input item.

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
| `predefined` | any (chosen by a parameter) | no |
| `engi.http.basic` | `engi.http.basic` | no |
| `engi.http.header` | `engi.http.header` | no |
| `engi.http.bearer` | `engi.http.bearer` | no |
| `engi.http.queryKey` | `engi.http.queryKey` | no |
| `engi.oauth2` | `engi.oauth2` | no |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `method` | Method | `options` | `GET` | no | `GET`<br>`POST`<br>`PUT`<br>`PATCH`<br>`DELETE`<br>`HEAD`<br>`OPTIONS` | always | — |
| `url` | URL | `string` | — | yes | — | always | — |
| `query` | Query parameters | `fixedCollection` | — | no | — | always | Appended to the URL; repeated names add repeated parameters. |
| ↳ `query.name` | Name | `string` | — | yes | — | always | — |
| ↳ `query.value` | Value | `string` | — | no | — | always | — |
| `headers` | Headers | `fixedCollection` | — | no | — | always | Request headers. |
| ↳ `headers.name` | Name | `string` | — | yes | — | always | — |
| ↳ `headers.value` | Value | `string` | — | no | — | always | — |
| `authentication` | Authentication | `options` | `none` | no | `none` (None)<br>`predefined` (Predefined credential)<br>`generic` (Generic credential) | always | — |
| `genericAuthType` | Credential type | `options` | `engi.http.basic` | no | `engi.http.basic` (Basic Auth)<br>`engi.http.header` (Header Auth)<br>`engi.http.bearer` (Bearer Token)<br>`engi.http.queryKey` (API Key in Query)<br>`engi.oauth2` (OAuth2) | shown when `authentication` = `generic` | — |
| `bodyType` | Body | `options` | `none` | no | `none` (None)<br>`json` (JSON)<br>`form` (Form (urlencoded))<br>`raw` (Raw text)<br>`binary` (Binary file) | shown when `method` = `POST` or `PUT` or `PATCH` or `DELETE` | — |
| `inputBinaryField` | Input binary field | `string` | `data` | no | — | shown when `bodyType` = `binary` | — |
| `jsonBody` | JSON body | `json` | — | no | — | shown when `bodyType` = `json` | An object or array, or a string holding JSON. |
| `formFields` | Form fields | `fixedCollection` | — | no | — | shown when `bodyType` = `form` | Sent as application/x-www-form-urlencoded. |
| ↳ `formFields.name` | Name | `string` | — | yes | — | always | — |
| ↳ `formFields.value` | Value | `string` | — | no | — | always | — |
| `rawBody` | Raw body | `string` | — | no | — | shown when `bodyType` = `raw` | — |
| `rawContentType` | Content type | `string` | `text/plain` | no | — | shown when `bodyType` = `raw` | — |
| `responseFormat` | Response format | `options` | `auto` | no | `auto` (Auto (JSON when the server says so, else text))<br>`json` (JSON)<br>`text` (Text)<br>`base64` (Binary as base64 text)<br>`binary` (Binary file (stored, referenced by the item)) | always | — |
| `outputBinaryField` | Output binary field | `string` | `data` | no | — | shown when `responseFormat` = `binary` | — |
| `fullResponse` | Include status and headers | `boolean` | `false` | no | — | always | Output {statusCode, statusMessage, headers, body} instead of just the body. |
| `failOnError` | Fail on non-2xx status | `boolean` | `true` | no | — | always | — |
| `followRedirects` | Follow redirects | `boolean` | `true` | no | — | always | — |
| `timeoutMs` | Timeout (ms) | `number` | `0` | no | — | always | Per-request timeout. 0 uses the configured default. |
<!-- END GENERATED: engi.http.request -->

---
**← Previous** [4. Code](04-code.md) · **Index** [README](README.md) · **Next →** [6. Databases](06-databases.md)
