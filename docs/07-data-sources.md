# 7. Data Sources & Credentials

A **data source** is an external system a node talks to (Postgres, Kafka, S3, Slack...).
Access is configured once as a **credential** (entity in [page 3](03-data-model.md#36-credential))
and referenced by ID from nodes. Packages: `internal/service/credential`, port `Cipher`.

## 7.1 Credential types

A credential *type* is code, not data (`pkg/nodesdk/credential_type.go`). Built-in types live in
`internal/credtype/<name>` and are registered in an explicit `registry.Registry` built by
`internal/app` (no `init()` registration). Each type declares:

| Part | Meaning |
|---|---|
| `Name`, `DisplayName`, `Icon` | Identifier (`engi.postgres`), UI label, icon key |
| `Fields` | `CredentialField`: the node `Property` schema (type, required, default, options, `displayOptions`) plus `Secret` and `Managed` flags |
| `Secret` | Write-only: encrypted at rest, never returned (the API sees the sentinel `__ENGI_SECRET__`) |
| `Managed` | System-owned (the OAuth2 token set): rejected as input, never returned, preserved by updates |
| `Authenticate` (optional) | `AuthSpec`: how to apply the credential to an HTTP request: `header`, `query`, `basic`, `bearer` or a `custom` function. Field names refer to keys of the credential data |
| `Test` (optional) | `TestSpec`: a cheap request (method, URL or URL field, expected status) sent with `Authenticate` applied |
| `OAuth2` (optional) | `OAuth2Spec`: names the fields that hold grant, URLs, client id/secret, scope, token set (section 7.3) |

Built-in types:

| Type | Fields | Authenticates by |
|---|---|---|
| `engi.http.basic` | user, password* | Basic header |
| `engi.http.header` | name, value* | Header `name: value` |
| `engi.http.bearer` | token* | `Authorization: Bearer` |
| `engi.http.queryKey` | name, value* | Query parameter |
| `engi.oauth2` | grantType, authUrl, accessTokenUrl, clientId, clientSecret*, scope, authentication (header/body) | Bearer with the current access token |
| `engi.postgres` | host, port, database, user, password*, sslMode | used by the Postgres node (`engi.db.postgres`) |
| `engi.mysql` | host, port, database, user, password*, tlsMode (disable/required/skip-verify), caCertificate* | used by the MySQL node (`engi.db.mysql`) |
| `engi.redis` | host, port, db, username, password*, tls | used by the Redis node (`engi.db.redis`) |
| `engi.kafka` | brokers, clientId, saslMechanism (none/plain/scram-sha-256/scram-sha-512), username, password*, tls, caCertificate, tlsSkipVerify (development only) | used by the Kafka trigger (`engi.trigger.kafka`) and producer (`engi.kafka.produce`) |
| `engi.smtp` | host, port (587), user, password*, security (none/starttls/tls), fromAddress | used by Send Email (`engi.email.send`); STARTTLS or TLS is verified against the system roots, and PLAIN auth is refused on an unencrypted link to a non-local host |
| `engi.imap` | host, port (993), user, password*, security (none/starttls/tls) | used by the IMAP trigger (`engi.trigger.imap`) |
| `engi.s3` | endpoint (empty for AWS), region, accessKeyId, secretAccessKey*, sessionToken*, forcePathStyle, tlsSkipVerify (development only) | used by the S3 node (`engi.storage.s3`) for AWS S3, MinIO, R2 and other S3-compatible stores |
| `engi.mongodb` | connectionString*, host, port, user, password*, database, tls | used by the MongoDB node (`engi.db.mongodb`); a connection string wins over host/port/user/password |

`*` secret field.

Rules:
- On create and update the data is validated against `Fields`: unknown fields, wrong types, missing required fields (fields hidden by `displayOptions` are not required), invalid options and managed fields are rejected with a `*domain.ValidationError` listing every issue. Defaults are applied.
- **Redaction:** `Get` replaces every secret value with the sentinel. `Update` keeps the stored value of a secret that is omitted or sent back as the sentinel, so a UI can round-trip a form. A sentinel on create is an error. The type cannot change on update. Editing OAuth2 grant, token URL, client id/secret or scope drops the stored token.
- `Test` is on demand only (`Service.Test`; the `POST /credentials/{id}/test` route comes with the API) and bounded by `credentials.testTimeout`. OAuth2 types test by fetching a fresh token; types with neither a test nor OAuth2 return `domain.ErrNoTest`.
- A node's descriptor lists the credential bindings it accepts (`Descriptor.Credentials`); the engine resolves a binding to a credential id from the workflow and calls `CredentialResolver.Resolve`.
- Nodes get `Input.Credential(ctx, name)`: the decrypted data plus `Authenticate(req)`, which applies the type's `AuthSpec`.

### HTTP Request node authentication

`authentication` is `none`, `predefined` (the credential bound as `predefined`, any type that declares `Authenticate`) or `generic` (`genericAuthType` picks basic, header, bearer, query key or OAuth2; the credential is bound under the type's own name). The credential is applied after the user's headers and query, so it wins on a conflict, and is resolved once per node run.

## 7.2 Encryption at rest

| Item | Decision |
|---|---|
| Algorithm | AES-256-GCM, random 96-bit nonce per encryption (`internal/adapter/crypto/aesgcm`, port `Cipher`) |
| Key source | A keyring from config: `ENGI_ENCRYPTION_KEYS` (`keyId:base64key,...`) and/or `ENGI_ENCRYPTION_KEYS_FILE`, plus `ENGI_ENCRYPTION_ACTIVE_KEY_ID`. 32 bytes each, no default. Server roles refuse to start without a keyring (`CredentialsConfig.RequireKeyring`) |
| Stored form | `keyId` column + `data` = `nonce \|\| ciphertext` (see [page 3](03-data-model.md#36-credential)); the credential ID is bound as GCM additional data, so ciphertext copied onto another row fails to decrypt |
| Scope | The whole `data` object (JSON) is one ciphertext. Names and types stay in clear text for listing |
| Never | Logged, returned by the API, copied into execution data or exports. Decrypt errors reveal nothing beyond "decryption failed" |

### Key rotation

Keys are identified by a short **key ID**. The keyring holds one active key and any number of retired keys that can still decrypt.

1. Add the new key to the keyring and mark it active. Restart (rolling).
2. New and updated credentials are encrypted with the active key. Old ones still decrypt via their key ID.
3. Run `Service.RotateKeys` (to be exposed as `engi credentials reencrypt` and `POST /api/v1/admin/reencrypt`): it pages through credentials in batches of `credentials.rotationBatchSize`, decrypts each row not on the active key and rewrites it with the active key. Each batch runs in one transaction when a `TxManager` is wired, and each row under its credential lock. It is idempotent and resumable; rows that cannot be decrypted are counted as failed and reported while the rest continue.
4. When a run reports zero rotated rows and no failures, remove the retired key.

Credential exports are never decryptable elsewhere unless the user chooses an explicit,
passphrase-protected export.

## 7.3 OAuth2

Generic OAuth2 is one credential type (`engi.oauth2`); providers (Google, Slack...) will be thin presets.

| Grant | Flow |
|---|---|
| Authorization code + **PKCE** (S256) | `Service.BeginAuthorization(ctx, id)` returns the provider URL; `Service.CompleteAuthorization(ctx, state, code)` validates state, exchanges the code and stores the token set |
| Client credentials | No user step. The token is fetched on first use |

- **State without server-side storage:** the `state` parameter is the credential id, the PKCE verifier and an expiry (`credentials.oauth2StateTtl`), sealed with the credential cipher. Any replica can complete the flow, tampering or expiry yields `ErrInvalidState`, and the verifier never appears in clear text in the URL. (The authorization code itself is single use at the provider.) The redirect URI is `credentials.oauth2RedirectUrl`.
- **Callback route:** `GET /oauth2/callback` on the `api` role calls `CompleteAuthorization`. The route comes with the REST API (phase 2b-3).
- **Token storage:** the token set (`accessToken`, `refreshToken`, `expiresAt`, scope, `needsReauth`) is a managed field inside the encrypted credential data.
- **Refresh:** lazily in `Resolve`, when the access token expires within `credentials.oauth2RefreshSkew`. Client credentials fetch a new token; authorization code uses the refresh token and keeps the old one if the provider rotates nothing. Refreshes are serialised per credential with an in-process lock that re-reads the stored token after acquiring it, so concurrent callers share one token request and never burn a rotating refresh token twice. A cross-process lease is not implemented yet: with several workers, two may refresh at nearly the same time (client credentials: harmless; rotating refresh tokens: planned with the lease port).
- A provider answering `invalid_grant` marks the token `needsReauth`; `Resolve` then returns an error wrapping `domain.ErrNeedsReauth` without calling the provider again until the user reconnects, and `View.NeedsReauth` lets the UI show a reconnect button.
- The token endpoint client is the `OAuth2Client` port (`internal/adapter/oauth2/httptoken`), using the shared HTTP client. Client credentials are sent as an HTTP Basic header or in the body (`authentication` field). Error responses never echo the response body.

## 7.4 Decryption only at execution time

```
node needs credential ──► engine asks CredentialResolver (port)
    ──► load row ──► Cipher.Decrypt ──► refresh OAuth if needed
    ──► typed Credential value handed to this node call only
```

- Only the `worker` (and `api` for `Test`, `runner` via plugin call) holds the key. `webhook` and `trigger` roles need it only if a trigger node uses a credential.
- The plaintext lives in memory for one node call and is not stored in `State`, `runData`, logs or traces. Waiting executions re-resolve credentials on resume.
- Plugins receive the decrypted value for the call only ([page 6](06-nodes-and-plugins.md#63-any-language-plugins)).
- Expressions cannot read credentials; `$vars` secret values are masked in run output.
- Without a configured keyring `internal/app` wires a resolver that knows no credentials (the in-memory `engi run` path). With one, it builds the credential service and uses it as the engine's resolver.
- Tests assert (with a capturing `slog` handler) that no secret, token or refresh token reaches the logs.

### Credential design

| Topic | Engi |
|---|---|
| Cipher | AES-256-GCM only, keyring with key ids, credential id as AAD |
| Redaction | A sentinel per secret field, kept on update; managed OAuth data is never returned |
| Authenticate | Declarative `header`/`query`/`basic`/`bearer` by field name, plus a `custom` function (no body or expression templating) |
| OAuth2 tokens | Stored in the credential data; refreshed proactively before expiry with a skew, one refresh per credential at a time |
| Auth-code state | Sealed, self-contained state (credential id, PKCE verifier, expiry); PKCE S256 for the code grant |

## 7.5 Connection pooling (database data sources)

Nodes must not open a connection per item. As built, `port.DBPool` (`DB(ctx, driver, credential)
-> *sql.DB`) is implemented by `internal/adapter/dbpool.Manager`, one per process, created in
`internal/app` and injected into the Postgres and MySQL nodes through `app.WithDBPools`; there are
no globals. The pool key is **driver + credential ID**; the stored entry also records the
credential **version** (`UpdatedAt` in unix nanoseconds, carried on `nodesdk.Credential.Version`).
A request with a different version closes the old pool and builds a new one, so editing a
credential takes effect on the next node run. Connections are opened lazily, so a wrong host shows
up as a query error. Postgres uses pgx (`stdlib`), MySQL `go-sql-driver/mysql`; MySQL dates are
parsed as UTC. Pools are closed on graceful shutdown (`App.CloseDatabasePools`, called by
`Server.Shutdown`).

| Setting (config) | Meaning |
|---|---|
| `nodes.database.maxOpenConns` / `maxIdleConns` | Per-pool connection limits |
| `nodes.database.idleTimeout` | Idle connections closed after this time |
| `nodes.database.maxLifetime` | Hard recycle time |
| `nodes.database.connectTimeout` | Per-connection dial/handshake timeout |

Redis (`go-redis`) and MongoDB (`mongo-driver/v2`) are not `database/sql`, so they use typed
managers, `internal/adapter/redispool` and `internal/adapter/mongopool`, built on the same
generic keyed cache (`internal/clientcache`) that now also backs `dbpool`: one client per
credential ID, replaced when the credential version changes, closed on shutdown by
`App.CloseDatabasePools`. They are injected with `app.WithRedisClients` / `app.WithMongoClients`.
The pool settings above map as: `maxOpenConns` to the Redis pool size and Mongo `maxPoolSize`,
`idleTimeout` to the idle limit, `connectTimeout` to dial/server-selection timeouts and
`maxLifetime` to the Redis connection lifetime. Two caps are new: `nodes.database.redisMaxKeys`
(keys returned by the Redis `keys` operation, via `SCAN`, never `KEYS`) and
`nodes.database.mongoMaxResults` (documents per find or aggregate).

Kafka (`franz-go`) follows the same pattern in `internal/adapter/kafkapool`: producers are cached by `clientcache` (key: credential id and version plus acks/compression/idempotence), flushed (`nodes.kafka.flushTimeout`) and closed by `App.CloseDatabasePools`; consumers are built per listener and closed by it (leaving the group). Injected with `app.WithKafkaClients`. Dial timeout is `nodes.kafka.connectTimeout`.

S3 (`aws-sdk-go-v2`) follows the same pattern in `internal/adapter/s3pool`: one client per credential id and version, cached by `clientcache`, dropped on shutdown by `App.CloseDatabasePools` (the SDK client holds no connection to flush). Injected with `app.WithS3Clients`. Dial timeout is `nodes.s3.connectTimeout`.

Not built yet: a cap on live pools (LRU eviction), closing a pool the moment its credential is
deleted (it is dropped at shutdown or when its version changes), and a separate query timeout
(use the node's own execution timeout). Non-pooled systems (HTTP APIs, S3) share the HTTP client
from config (proxy, TLS, timeouts).

## 7.6 External secret stores (later port)

A `SecretStore` port will let a credential field hold a reference such as
`vault://kv/data/prod#password` or an External Secrets Operator mount instead of a value.
References are resolved at execution time, never persisted resolved. Adapters: Vault,
Kubernetes Secret, ESO. Planned in Roadmap phase 8; the credential schema already allows a
field to be `{ "ref": "..." }` so no data migration is needed.

## 7.7 Data source list

Target catalogue from [page 6, section 6.4](06-nodes-and-plugins.md#64-built-in-node-catalogue-v1-target).

| Data source | Credential type | Pooled | Auth |
|---|---|---|---|
| Postgres, MySQL, SQL Server | `engi.postgres`, `engi.mysql`, `engi.mssql` | yes | user/password, TLS options |
| MongoDB | `engi.mongodb` | yes (client) | connection string, or host/user/password; TLS |
| Redis | `engi.redis` | yes (client) | username/password, TLS |
| Kafka | `engi.kafka` | producers cached (one per credential version and option set); each trigger owns its consumer | SASL plain/SCRAM, TLS with optional CA (client certificates not built) |
| S3-compatible | `engi.s3` | one client per credential version | static keys and optional session token, custom endpoint, path-style, TLS skip-verify (dev) |
| S3 (and MinIO) | `engi.s3` | HTTP client | access key / role |
| Google Sheets | `engi.oauth2` (Sheets scope), `engi.googleServiceAccount` | HTTP client | OAuth2 code, or service-account key (JWT exchanged per run) |
| Airtable | `engi.airtable` | HTTP client | personal access token |
| Elasticsearch | `engi.elasticsearch` | HTTP client | API key / basic |
| Slack | `engi.slack` | HTTP client | bot token (OAuth2 later) |
| Telegram | `engi.telegram` | HTTP client | bot token |
| Microsoft Teams | `engi.teamsWebhook`, `engi.oauth2` (Graph) | HTTP client | webhook URL / OAuth2 |
| Discord | per service | HTTP client | bot token |
| SMTP / IMAP | `engi.smtp`, `engi.imap` | per call / listener | user/password, OAuth2 |
| Generic HTTP | `engi.http.*` | HTTP client | basic, bearer, header, OAuth2 |

---
**← Previous** [6. Nodes & Plugins](06-nodes-and-plugins.md) · **Index** [README](README.md) · **Next →** [8. Triggers & Webhooks](08-triggers.md)
