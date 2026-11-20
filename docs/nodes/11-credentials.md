# 11. Credential Types

Every built-in credential type, generated from the credential type registry. Create credentials in the editor or through the [API](../09-api.md); secret fields are write-only. See [Data Sources & Credentials](../07-data-sources.md).

## How credentials work

A credential stores the connection details for one external system (a database, a Kafka cluster, a Slack bot). You create it once, in the editor or through the [API](../09-api.md), and nodes refer to it by ID. The full design is in [Data Sources & Credentials](../07-data-sources.md).

- **Encrypted at rest.** The whole data object is encrypted with AES-256-GCM using the active key of the keyring the operator configures (`credentials.keys` or `credentials.keysFile`, and `credentials.activeKeyId`; see [Deployment](../11-deployment.md)). The server will not start without a keyring. Plan for key rotation: add the new key, make it active, re-encrypt, then retire the old key ([7.2](../07-data-sources.md#72-encryption-at-rest)).
- **Secrets are write-only.** Fields marked *Secret* in the tables below are never returned: the API shows the placeholder `__ENGI_SECRET__`. When you edit a credential and leave a secret field untouched (or send the placeholder back), the stored value is kept. A placeholder on create is an error. The credential type cannot be changed after creation.
- **Managed fields.** Some fields are owned by Engi, for example the OAuth2 token set. They cannot be set by you and are never returned.
- **Used only at run time.** A credential is decrypted for one node call and is not written to logs, execution data or exports. Expressions cannot read credentials.
- **Edits take effect on the next run.** Connection pools and clients are keyed by credential version, so a changed credential is picked up without a restart.
- **Test button.** `POST /credentials/{id}/test` checks a credential, bounded by `credentials.testTimeout` (default 15s). It is implemented for OAuth2 credentials (by fetching a token). The other built-in types declare no test, so the API reports that testing is not available for them; run a node that uses the credential instead.
- **OAuth2 flow.** For the authorization-code grant, the editor starts the flow (`POST /credentials/{id}/authorize`), sends you to the provider, and the provider returns you to `GET /oauth2/callback`. Operators must set `credentials.oauth2RedirectUrl` to that exact callback URL and register it with the provider. The flow uses PKCE and a sealed `state` that expires after `credentials.oauth2StateTtl` (default 10m). Tokens are refreshed automatically shortly before they expire (`credentials.oauth2RefreshSkew`). If the provider rejects the refresh token, the credential is marked as needing re-authorisation and nodes fail with a clear error until you reconnect it. The client-credentials grant needs no user step.
- **Refreshing in a cluster.** Refresh-token providers often rotate the refresh token on every use, so two replicas refreshing the same credential at once would invalidate each other's token. In cluster mode (Postgres) a refresh therefore runs under a transaction-scoped advisory lock keyed on the credential ID, and the credential is read again once the lock is held: a replica that waited uses the token the other one stored, and only one token request goes out per expiry. A waiter gives up after `credentials.oauth2RefreshLockTimeout` (default 30s) and the node fails with a clear error. In standalone mode one process owns the database, and its in-process lock is enough. Tokens are never logged, and a refused refresh (`invalid_grant`) is stored so that every replica reports the credential as needing re-authorisation.

## Credential types

<!-- BEGIN GENERATED: credential-types -->
### Airtable

**Type:** `engi.airtable` · **Authentication:** `bearer`: `Authorization: Bearer` from `token`

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `token` | Personal access token | `string` | — | yes | yes | no | — | A personal access token, e.g. pat... |

### Anthropic

**Type:** `engi.anthropic` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `apiKey` | API key | `string` | — | yes | yes | no | — | — |
| `baseUrl` | Base URL | `string` | — | no | no | no | — | Optional. Overrides nodes.ai.anthropicBaseUrl, for a proxy or gateway. |

### Azure OpenAI

**Type:** `engi.azureOpenAI` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `apiKey` | API key | `string` | — | yes | yes | no | — | — |
| `endpoint` | Resource endpoint | `string` | — | yes | no | no | — | For example https://my-resource.openai.azure.com. The node's model is the deployment name. |
| `apiVersion` | API version | `string` | — | no | no | no | — | Optional. Overrides nodes.ai.azureApiVersion. |

### Google service account

**Type:** `engi.googleServiceAccount` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `email` | Service account email | `string` | — | yes | no | no | — | The client_email of the service account key file. |
| `privateKey` | Private key | `string` | — | yes | yes | no | — | The private_key (PEM) of the key file. |
| `tokenUrl` | Token URL | `string` | `https://oauth2.googleapis.com/token` | no | no | no | — | Token endpoint; the token_uri of the key file. |
| `subject` | Impersonate user | `string` | — | no | no | no | — | Optional. Email of a Workspace user to act as (domain-wide delegation). |

### HTTP Basic Auth

**Type:** `engi.http.basic` · **Authentication:** `basic`: HTTP Basic from `user` and `password`

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `user` | User | `string` | — | yes | no | no | — | — |
| `password` | Password | `string` | — | no | yes | no | — | — |

### Bearer Token

**Type:** `engi.http.bearer` · **Authentication:** `bearer`: `Authorization: Bearer` from `token`

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `token` | Token | `string` | — | yes | yes | no | — | — |

### HTTP Header Auth

**Type:** `engi.http.header` · **Authentication:** `header`: header name from `name` = `value`

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `name` | Header name | `string` | — | yes | no | no | — | — |
| `value` | Header value | `string` | — | yes | yes | no | — | — |

### API Key in Query

**Type:** `engi.http.queryKey` · **Authentication:** `query`: query parameter name from `name` = `value`

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `name` | Parameter name | `string` | `api_key` | yes | no | no | — | — |
| `value` | API key | `string` | — | yes | yes | no | — | — |

### IMAP

**Type:** `engi.imap` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `host` | Host | `string` | — | yes | no | no | — | — |
| `port` | Port | `number` | `993` | no | no | no | — | — |
| `user` | User | `string` | — | yes | no | no | — | — |
| `password` | Password | `string` | — | yes | yes | no | — | — |
| `security` | Security | `options` | `tls` | no | no | no | `none` (None)<br>`starttls` (STARTTLS)<br>`tls` (TLS) | — |

### Kafka

**Type:** `engi.kafka` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `brokers` | Brokers | `string` | — | yes | no | no | — | Comma-separated host:port list. |
| `clientId` | Client ID | `string` | `engi` | no | no | no | — | — |
| `saslMechanism` | SASL mechanism | `options` | `none` | no | no | no | `none` (None)<br>`plain` (PLAIN)<br>`scram-sha-256` (SCRAM-SHA-256)<br>`scram-sha-512` (SCRAM-SHA-512) | — |
| `username` | Username | `string` | — | no | no | no | — | — |
| `password` | Password | `string` | — | no | yes | no | — | — |
| `tls` | Use TLS | `boolean` | `false` | no | no | no | — | — |
| `caCertificate` | CA certificate (PEM) | `string` | — | no | no | no | — | Trusts this CA instead of the system roots. Leave empty for public brokers. |
| `tlsSkipVerify` | Skip certificate verification | `boolean` | `false` | no | no | no | — | Development only: accepts any broker certificate. |

### MCP Server

**Type:** `engi.mcp` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `authentication` | Authentication | `options` | `bearer` | no | no | no | `none` (None)<br>`bearer` (Bearer token)<br>`header` (Custom header) | — |
| `token` | Token | `string` | — | no | yes | no | — | — |
| `headerName` | Header name | `string` | — | no | no | no | — | — |
| `headerValue` | Header value | `string` | — | no | yes | no | — | — |

### MongoDB

**Type:** `engi.mongodb` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `connectionString` | Connection string | `string` | — | no | yes | no | — | mongodb:// or mongodb+srv:// URI. When set, host, port, user and password are ignored. |
| `host` | Host | `string` | — | no | no | no | — | — |
| `port` | Port | `number` | `27017` | no | no | no | — | — |
| `user` | User | `string` | — | no | no | no | — | — |
| `password` | Password | `string` | — | no | yes | no | — | — |
| `database` | Database | `string` | — | yes | no | no | — | — |
| `tls` | Use TLS | `boolean` | `false` | no | no | no | — | — |

### MySQL

**Type:** `engi.mysql` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `host` | Host | `string` | — | yes | no | no | — | — |
| `port` | Port | `number` | `3306` | no | no | no | — | — |
| `database` | Database | `string` | — | yes | no | no | — | — |
| `user` | User | `string` | — | yes | no | no | — | — |
| `password` | Password | `string` | — | no | yes | no | — | — |
| `tlsMode` | TLS mode | `options` | `disable` | no | no | no | `disable` (Disabled)<br>`required` (Required (verify))<br>`skip-verify` (Required (skip verification)) | required verifies the server certificate and host name. |
| `caCertificate` | CA certificate (PEM) | `string` | — | no | yes | no | — | Trusted CA for the server certificate; empty uses the system roots. |

### OAuth2

**Type:** `engi.oauth2` · **Authentication:** OAuth2 (token handled by the credential service)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `grantType` | Grant type | `options` | `clientCredentials` | yes | no | no | `clientCredentials` (Client credentials)<br>`authorizationCode` (Authorization code (PKCE)) | — |
| `authUrl` | Authorization URL | `string` | — | yes | no | no | — | — |
| `accessTokenUrl` | Access token URL | `string` | — | yes | no | no | — | — |
| `clientId` | Client ID | `string` | — | yes | no | no | — | — |
| `clientSecret` | Client secret | `string` | — | no | yes | no | — | — |
| `scope` | Scope | `string` | — | no | no | no | — | Space separated. |
| `authentication` | Send client credentials | `options` | `header` | no | no | no | `header` (In Authorization header (Basic))<br>`body` (In request body) | — |
| `oauthTokenData` | Token data | `json` | — | no | yes | yes | — | — |

### Ollama

**Type:** `engi.ollama` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `baseUrl` | Base URL | `string` | — | no | no | no | — | Optional. Defaults to nodes.ai.ollamaBaseUrl (http://localhost:11434). |
| `apiKey` | API key | `string` | — | no | yes | no | — | Optional. For an Ollama server behind an authenticating proxy. |
| `headerName` | Header name | `string` | — | no | no | no | — | Optional. Send the API key in this header instead of Authorization: Bearer. |

### OpenAI

**Type:** `engi.openai` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `apiKey` | API key | `string` | — | yes | yes | no | — | — |
| `baseUrl` | Base URL | `string` | — | no | no | no | — | Optional. Overrides nodes.ai.openaiBaseUrl, for a compatible server or gateway. |
| `organization` | Organization ID | `string` | — | no | no | no | — | Optional. Sent as the OpenAI-Organization header. |

### Postgres

**Type:** `engi.postgres` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `host` | Host | `string` | — | yes | no | no | — | — |
| `port` | Port | `number` | `5432` | no | no | no | — | — |
| `database` | Database | `string` | — | yes | no | no | — | — |
| `user` | User | `string` | — | yes | no | no | — | — |
| `password` | Password | `string` | — | no | yes | no | — | — |
| `sslMode` | SSL mode | `options` | `prefer` | no | no | no | `disable`<br>`allow`<br>`prefer`<br>`require`<br>`verify-ca`<br>`verify-full` | — |

### Redis

**Type:** `engi.redis` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `host` | Host | `string` | `localhost` | yes | no | no | — | — |
| `port` | Port | `number` | `6379` | no | no | no | — | — |
| `db` | Database index | `number` | `0` | no | no | no | — | — |
| `username` | Username | `string` | — | no | no | no | — | — |
| `password` | Password | `string` | — | no | yes | no | — | — |
| `tls` | Use TLS | `boolean` | `false` | no | no | no | — | — |

### S3

**Type:** `engi.s3` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `endpoint` | Endpoint | `string` | — | no | no | no | — | Leave empty for AWS S3. Set it for MinIO, Cloudflare R2, Backblaze B2 and other S3-compatible stores. |
| `region` | Region | `string` | `us-east-1` | yes | no | no | — | — |
| `accessKeyId` | Access key ID | `string` | — | yes | no | no | — | — |
| `secretAccessKey` | Secret access key | `string` | — | yes | yes | no | — | — |
| `sessionToken` | Session token | `string` | — | no | yes | no | — | Only for temporary credentials. |
| `forcePathStyle` | Force path-style addressing | `boolean` | `false` | no | no | no | — | Use endpoint/bucket/key instead of bucket.endpoint/key. Most self-hosted stores need this. |
| `tlsSkipVerify` | Skip certificate verification | `boolean` | `false` | no | no | no | — | Development only: accepts any server certificate. |

### Slack

**Type:** `engi.slack` · **Authentication:** `bearer`: `Authorization: Bearer` from `token`

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `token` | Bot token | `string` | — | yes | yes | no | — | A bot or user token, e.g. xoxb-... |

### SMTP

**Type:** `engi.smtp` · **Authentication:** none declared (used directly by the node)

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `host` | Host | `string` | — | yes | no | no | — | — |
| `port` | Port | `number` | `587` | no | no | no | — | — |
| `user` | User | `string` | — | no | no | no | — | Leave empty for servers that need no authentication. |
| `password` | Password | `string` | — | no | yes | no | — | — |
| `security` | Security | `options` | `starttls` | no | no | no | `none` (None)<br>`starttls` (STARTTLS)<br>`tls` (TLS) | — |
| `fromAddress` | Default from address | `string` | — | no | no | no | — | Used when the node sets no From. |

### Microsoft Teams webhook

**Type:** `engi.teamsWebhook` · **Authentication:** `custom`

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `url` | Webhook URL | `string` | — | yes | yes | no | — | — |

### Telegram

**Type:** `engi.telegram` · **Authentication:** `custom`

| Name | Display name | Type | Default | Required | Secret | Managed | Options | Description |
|---|---|---|---|---|---|---|---|---|
| `token` | Bot token | `string` | — | yes | yes | no | — | The token @BotFather gave you. |
<!-- END GENERATED: credential-types -->

## Getting the details

Short notes on where each value comes from and what access to give it. Grant the minimum: a credential that can only do what the workflow does limits the damage if it leaks.

**HTTP credentials** (`engi.http.*`, `engi.oauth2`). These apply to the [HTTP Request](05-http.md) node. Use the type that matches how the API authenticates: Basic (user and password), Bearer (token), Header (a custom header such as `X-API-Key`) or Query (a key in the URL). For OAuth2, register an application with the provider, set its redirect URL to the value of `credentials.oauth2RedirectUrl`, and copy the client ID and secret, the authorization and token URLs, and the scopes you need.

**Postgres.** Create a dedicated role (`CREATE ROLE engi LOGIN PASSWORD '...'`) and grant only the privileges the workflows need (`SELECT`, `INSERT`, and so on, on specific tables). Use `sslMode` `require` or stricter for anything that is not on a private network.

**MySQL.** Create a dedicated user with grants limited to the schemas and statements in use. Set `tlsMode` to `required` for remote servers, and add `caCertificate` if the server uses a private CA. `skip-verify` encrypts without checking the certificate; use it only for testing.

**Redis.** Host, port and database number. With Redis 6 or later, use an ACL user restricted to the commands and key patterns the workflow uses (for example `~cache:*`) rather than the default user. Turn on `tls` for remote servers.

**MongoDB.** Either a connection string (`mongodb://` or `mongodb+srv://`, which overrides the other fields) or host, port, user, password and optional database. Create a user with a role such as `read` or `readWrite` on the one database, not a cluster-wide role.

**Kafka.** `brokers` is a comma-separated `host:port` list. Choose the SASL mechanism your cluster uses (Confluent Cloud and most managed services use `plain` over TLS; self-managed clusters often use `scram-sha-512`). The principal needs `Write` and `Describe` on topics for the producer, and `Read` on topics plus `Read` on the consumer group (named `engi-<workflow id>-<node>` unless you set `groupId`) for the trigger. Use `caCertificate` for a private CA; `tlsSkipVerify` is for development only.

**S3.** Create an access key for a dedicated IAM user or bucket-scoped key. For AWS, leave `endpoint` empty and set `region`. For MinIO, R2, B2 and similar, set `endpoint` and usually `forcePathStyle`. Grant only the actions used: `s3:ListAllMyBuckets` (List buckets), `s3:ListBucket` (List objects), `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`. Presigned links carry the permissions of this key. Use `sessionToken` only for temporary credentials. `tlsSkipVerify` is for development only.

**SMTP.** Use your mail provider's SMTP host and port: `587` with `starttls` is the usual choice, `465` with `tls`. Many providers need an app password or an SMTP-specific API key rather than the account password. Set `fromAddress` to an address the account may send from, or the server will refuse the sender.

**IMAP.** Host, port (`993` with `tls` by default), user and password; most providers need an app password. The account only needs access to the mailbox the [IMAP Trigger](01-triggers.md#imap-trigger) reads; if you use "move" or "mark as read" afterwards, it needs write access to that mailbox.

**Slack.** Create an app at api.slack.com, install it to the workspace and copy the Bot User OAuth Token (`xoxb-...`). Add only the scopes for the operations you use: `chat:write` (post, update and delete the bot's own messages), `files:write` (upload), `channels:read` and `groups:read` (list public and private channels), `users:read.email` (find a user by email). The bot must be invited to a channel before it can post there (unless it has `chat:write.public` for public channels).

**Google Sheets (OAuth2).** In Google Cloud, enable the Google Sheets API, create an OAuth client (type "Web application") whose redirect URL is the value of `credentials.oauth2RedirectUrl`, and create an [OAuth2](#oauth2) credential with grant "Authorization code (PKCE)", authorization URL `https://accounts.google.com/o/oauth2/v2/auth`, token URL `https://oauth2.googleapis.com/token`, your client ID and secret, and scope `https://www.googleapis.com/auth/spreadsheets` (use `.../spreadsheets.readonly` for read-only flows). Connect it once; Engi refreshes the token.

**Google service account.** In Google Cloud, enable the Sheets API, create a service account and a JSON key, and paste the key's `client_email` and `private_key` into this credential. Share each spreadsheet with the service account's email (as editor to write). Engi signs a JWT with the key and exchanges it for a one-hour access token on every run of the node; the token is never stored. Fill "Impersonate user" only for Google Workspace domain-wide delegation. Engi has no service-account support in the generic OAuth2 credential, which is why this is its own type.

**Airtable.** At airtable.com/create/tokens create a personal access token. Add the scopes `data.records:read` and `data.records:write` and grant access only to the bases the workflow uses. The token starts with `pat`.

**Telegram.** Talk to @BotFather, send `/newbot`, and copy the token it returns. The bot can only message chats where a user has started it or where it has been added as a member (or admin, for channels).

**Microsoft Teams webhook.** In the Teams channel, add an incoming webhook (through the Workflows app or the connector, depending on your tenant) and copy the URL it gives you. The URL is the secret: anyone with it can post to that channel, so store it only in this credential. For posting through Graph, use an [OAuth2](#oauth2) credential instead (see the [Microsoft Teams](10-chat.md#microsoft-teams) node).

---
**← Previous** [10. Chat](10-chat.md) · **Index** [README](README.md) · **Next →** [12. AI](12-ai.md)
