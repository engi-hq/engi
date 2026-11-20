# 8. Storage

Object storage.

## S3 Storage

Works with objects in AWS S3 or any S3-compatible store (MinIO, Cloudflare R2, Backblaze B2 and similar). Use it to fetch a file for a later node, store a generated report, copy or delete objects, list what is in a bucket, or hand out a time-limited download or upload link.

**Behaviour**

- **Operations.** List buckets, List objects, Get, Put, Delete, Copy and Presigned URL. Everything except List buckets needs `bucket`; Get, Put, Delete, Copy and Presign also need `key`. Empty bucket or key fails with a clear message.
- **List objects** returns one item per object (`key`, `size`, `etag`, `storageClass`, `lastModified`), followed by one `{prefix}` item per common prefix when a `delimiter` such as `/` is set. It stops at `maxKeys` or `nodes.s3.maxKeys` (default 1000), whichever is smaller, and follows pagination for you.
- **Get** with `as`:
  - `text` or `json`: the body goes into `data` of the item. Objects larger than `nodes.s3.maxObjectBytes` (default 1 MiB) fail; fetch them as binary instead. `json` fails if the body does not parse.
  - `binary`: the object is streamed into Engi's blob store and attached under the `binaryField` (default `data`). No size limit applies here, and the item carries metadata only.
- **Put** takes its body from `text`, `json` (a string is sent as is under `text`, anything else as JSON), or `binary` (the named binary field of the input item). The body is held in memory so it can be signed, so it is capped by `nodes.s3.maxUploadBytes` (default 64 MiB). Content type defaults to `text/plain`, `application/json` or the binary's own MIME type unless you set `contentType`.
- **Copy** copies inside the store (no download). An empty `destBucket` means the source bucket.
- **Presigned URL** builds a link for `get` (download) or `put` (upload) that works for `expiresIn` seconds (default 900) without credentials. Values above `nodes.s3.presignMaxExpiry` (default 24h) are refused, as are zero or negative values. Signing is local; the node does not check that the object exists.
- **Per item, in order.** Items run one after another; an error stops the run and names the item. Each request has the `nodes.s3.requestTimeout` limit (default 60s).
- **Pooling.** One client per credential version ([Data Sources 7.5](../07-data-sources.md#75-connection-pooling-database-data-sources)).

**Output.** Depends on the operation: Get gives `bucket`, `key`, `size`, `contentType`, `etag`, `lastModified` and `data` (or a binary); Put gives `bucket`, `key`, `size`, `etag`; Delete gives `bucket`, `key`, `deleted: true`; Copy gives the destination plus `sourceBucket`/`sourceKey`; Presign gives `url`, `method`, `expiresIn`.

**Example**

```json
{
  "operation": "presign",
  "bucket": "reports",
  "key": "2026/10/summary.pdf",
  "method": "get",
  "expiresIn": 3600
}
```

**Notes**

- Config ([Deployment](../11-deployment.md)): `nodes.s3.maxKeys`, `maxObjectBytes`, `maxUploadBytes`, `presignMaxExpiry`, `connectTimeout`, `requestTimeout`.
- Common errors: `NoSuchKey`/`NoSuchBucket`, `AccessDenied` (the key lacks the permission for that call), and `exceeds the limit` for presign and upload caps. MinIO and many self-hosted stores need `forcePathStyle` on the credential.
- Credential: [S3](11-credentials.md#s3).
- **Notes.** Text and JSON reads are size-capped and large objects go through binary; presigned URLs are a built-in operation with an operator-set maximum lifetime.

<!-- BEGIN GENERATED: engi.storage.s3 -->
**Type:** `engi.storage.s3` · **Version:** 1 · **Group:** `action` · **Display name:** S3 Storage

Work with buckets and objects in AWS S3 or any S3-compatible store (MinIO, R2, ...).

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
| `engi.s3` | `engi.s3` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `operation` | Operation | `options` | `get` | no | `listBuckets` (List buckets)<br>`list` (List objects)<br>`get` (Get object)<br>`put` (Put object)<br>`delete` (Delete object)<br>`copy` (Copy object)<br>`presign` (Presigned URL) | always | — |
| `bucket` | Bucket | `string` | — | yes | — | hidden when `operation` = `listBuckets` | — |
| `key` | Key | `string` | — | yes | — | shown when `operation` = `get` or `put` or `delete` or `copy` or `presign` | — |
| `prefix` | Prefix | `string` | — | no | — | shown when `operation` = `list` | — |
| `delimiter` | Delimiter | `string` | — | no | — | shown when `operation` = `list` | Usually "/": groups keys below a common prefix into one prefix item. |
| `maxKeys` | Max objects | `number` | — | no | — | shown when `operation` = `list` | Capped by nodes.s3.maxKeys; empty uses the cap. |
| `as` | Return as | `options` | `text` | no | `text` (Text in the item)<br>`json` (JSON in the item)<br>`binary` (Binary data) | shown when `operation` = `get` | — |
| `source` | Body from | `options` | `text` | no | `text` (Text)<br>`json` (JSON)<br>`binary` (Binary data) | shown when `operation` = `put` | — |
| `value` | Body | `string` | — | no | — | shown when `operation` = `put` and `source` = `text` or `json` | — |
| `binaryField` | Binary field | `string` | `data` | no | — | shown when `operation` = `get` or `put` | Binary property of the item to upload, or to store the downloaded object in. |
| `contentType` | Content type | `string` | — | no | — | shown when `operation` = `put` | Empty uses application/json, text/plain or the binary's MIME type. |
| `destBucket` | Destination bucket | `string` | — | no | — | shown when `operation` = `copy` | Empty copies within the source bucket. |
| `destKey` | Destination key | `string` | — | yes | — | shown when `operation` = `copy` | — |
| `method` | URL for | `options` | `get` | no | `get` (Download)<br>`put` (Upload) | shown when `operation` = `presign` | — |
| `expiresIn` | Expires in (seconds) | `number` | `900` | no | — | shown when `operation` = `presign` | Capped by nodes.s3.presignMaxExpiry. |
<!-- END GENERATED: engi.storage.s3 -->

---
**← Previous** [7. Streaming](07-streaming.md) · **Index** [README](README.md) · **Next →** [9. Email](09-email.md)
