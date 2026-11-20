# 10. Chat

Chat and messaging services.

## Slack

Posts to and manages Slack from a workflow. Use it to announce results in a channel, reply in a thread, edit a status message as a job progresses, share a text report as a file, or resolve a person's Slack ID from their email.

**Behaviour**

- **Operations.** Post message, Update message, Delete message, Upload text file, List channels, Get user by email.
- **Post and Update.** Need `channel` and either `text`, `blocks` (a Block Kit array, or JSON text) or both. `threadTs` replies in a thread. Update and Delete address a message by `channel` and `ts`; both are returned by Post. Prefer channel IDs (`C0123...`); a channel name works for posting only.
- **Upload text file.** Takes `content` as text, `filename`, optional `title`, `comment` and `threadTs`. It runs Slack's three-step upload (reserve, send bytes, complete) for you. Binary data is not supported.
- **List channels.** Follows pagination in pages of `nodes.chat.pageSize` and stops at `nodes.chat.maxListResults` (default 1000). `types` is a comma list of `public_channel`, `private_channel`, `mpim`, `im`; archived channels are skipped by default.
- **Error envelope.** Slack answers HTTP 200 with `ok: false` and an error code. Engi turns that into a node error such as `slack chat.postMessage: channel_not_found` (the HTTP status is attached). Success responses are returned with `ok` removed.

- **Rate limits.** A `429` is retried after the service's `Retry-After` wait (one second if absent), up to `nodes.chat.maxRetries` times (default 2). A wait longer than `nodes.chat.maxRetryWait` (default 30s), or still being limited after the last retry, fails the item with a "rate limited" error.
- **Secrets stay out of errors.** Tokens and webhook URLs are scrubbed from every error message.
- **Per item, in order.** One call per input item; an error stops the run and names the item (`item 2: ...`).

**Output.** One item per input item, containing Slack's response. For Post: `channel`, `ts` and the sent `message`. For List channels: `channels`, `count`, `truncated`. Get user by email: the `user` object.

**Example**

```json
{
  "operation": "postMessage",
  "channel": "C0123456789",
  "text": "Deploy {{ $json.version }} finished: {{ $json.status }}",
  "threadTs": ""
}
```

**Notes**

- Config: `nodes.chat.slackBaseUrl`, `maxRetries`, `maxRetryWait`, `maxListResults`, `pageSize` ([Deployment](../11-deployment.md)).
- Common errors: `not_in_channel` (invite the bot), `channel_not_found`, `missing_scope` (add the scope, reinstall the app), `invalid_auth`, `users_not_found`.
- Credential: [Slack](11-credentials.md#slack).
- **Notes.** The operation set is limited; files are text uploads through Slack's external upload API.

<!-- BEGIN GENERATED: engi.chat.slack -->
**Type:** `engi.chat.slack` · **Version:** 1 · **Group:** `action` · **Display name:** Slack

Post, update and delete Slack messages, upload text files, list channels and find users.

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
| `engi.slack` | `engi.slack` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `operation` | Operation | `options` | `postMessage` | no | `postMessage` (Post message)<br>`updateMessage` (Update message)<br>`deleteMessage` (Delete message)<br>`uploadFile` (Upload text file)<br>`listChannels` (List channels)<br>`getUserByEmail` (Get user by email) | always | — |
| `channel` | Channel | `string` | — | yes | — | shown when `operation` = `postMessage` or `updateMessage` or `deleteMessage` or `uploadFile` | Channel ID (C0123...) or, for posting, a channel name. |
| `text` | Text | `string` | — | no | — | shown when `operation` = `postMessage` or `updateMessage` | — |
| `blocks` | Blocks (JSON) | `json` | — | no | — | shown when `operation` = `postMessage` or `updateMessage` | Optional Block Kit array. |
| `threadTs` | Thread timestamp | `string` | — | no | — | shown when `operation` = `postMessage` or `uploadFile` | Reply in the thread of this message. |
| `ts` | Message timestamp | `string` | — | yes | — | shown when `operation` = `updateMessage` or `deleteMessage` | — |
| `content` | File content | `string` | — | no | — | shown when `operation` = `uploadFile` | — |
| `filename` | File name | `string` | `file.txt` | no | — | shown when `operation` = `uploadFile` | — |
| `title` | Title | `string` | — | no | — | shown when `operation` = `uploadFile` | — |
| `comment` | Comment | `string` | — | no | — | shown when `operation` = `uploadFile` | — |
| `types` | Channel types | `string` | `public_channel` | no | — | shown when `operation` = `listChannels` | Comma separated: public_channel, private_channel, mpim, im. |
| `excludeArchived` | Exclude archived | `boolean` | `true` | no | — | shown when `operation` = `listChannels` | — |
| `email` | Email | `string` | — | yes | — | shown when `operation` = `getUserByEmail` | — |
<!-- END GENERATED: engi.chat.slack -->

## Telegram

Sends messages from a Telegram bot. Use it for alerts and notifications to a person, group or channel the bot can reach.

**Behaviour**

- **Operations.** Send message, Edit message, Send document by URL, Get chat. `chatId` is a numeric ID or `@channelusername`.
- **Text.** `parseMode` is plain text (default), `MarkdownV2` or `HTML`. With MarkdownV2 you must escape special characters yourself; Telegram rejects badly formed markup. `disablePreview` turns off link previews, `replyTo` replies to a message ID (0 for none).
- **Edit** needs a positive `messageId` (returned by Send as `message_id`). **Send document** takes a URL that Telegram downloads itself, so it must be publicly reachable; uploading bytes from Engi is not supported.
- **Error envelope.** Telegram answers with `ok: false` and a description; Engi raises a node error such as `telegram sendMessage: Bad Request: chat not found` with the HTTP status attached. The bot token is replaced by `[redacted]` in anything shown.

- **Rate limits.** A `429` is retried after the service's `Retry-After` wait (one second if absent), up to `nodes.chat.maxRetries` times (default 2). A wait longer than `nodes.chat.maxRetryWait` (default 30s), or still being limited after the last retry, fails the item with a "rate limited" error.
- **Secrets stay out of errors.** Tokens and webhook URLs are scrubbed from every error message.
- **Per item, in order.** One call per input item; an error stops the run and names the item (`item 2: ...`).

**Output.** One item per input item holding Telegram's `result` object (a message for Send/Edit/Send document, a chat for Get chat).

**Example**

```json
{
  "operation": "sendMessage",
  "chatId": "-1001234567890",
  "text": "<b>Backup finished</b>: {{ $json.size }} MB",
  "parseMode": "HTML",
  "disablePreview": true
}
```

**Notes**

- Config: `nodes.chat.telegramBaseUrl` (point it at a local Bot API server if you run one), `maxRetries`, `maxRetryWait` ([Deployment](../11-deployment.md)).
- Common errors: `chat not found` (the bot has never been in that chat), `Forbidden: bot was blocked by the user`, `can't parse entities`, `message is not modified`.
- Credential: [Telegram](11-credentials.md#telegram).
- **Notes.** The operation set is limited; documents are sent by URL only, and receiving updates is not part of this node.

<!-- BEGIN GENERATED: engi.chat.telegram -->
**Type:** `engi.chat.telegram` · **Version:** 1 · **Group:** `action` · **Display name:** Telegram

Send and edit Telegram messages, send documents by URL and read chat details.

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
| `engi.telegram` | `engi.telegram` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `operation` | Operation | `options` | `sendMessage` | no | `sendMessage` (Send message)<br>`editMessage` (Edit message)<br>`sendDocument` (Send document by URL)<br>`getChat` (Get chat) | always | — |
| `chatId` | Chat ID | `string` | — | yes | — | always | Numeric ID or @channelusername. |
| `messageId` | Message ID | `number` | — | yes | — | shown when `operation` = `editMessage` | — |
| `text` | Text | `string` | — | yes | — | shown when `operation` = `sendMessage` or `editMessage` | — |
| `documentUrl` | Document URL | `string` | — | yes | — | shown when `operation` = `sendDocument` | — |
| `caption` | Caption | `string` | — | no | — | shown when `operation` = `sendDocument` | — |
| `parseMode` | Parse mode | `options` | `""` | no | `""` (Plain text)<br>`MarkdownV2`<br>`HTML` | shown when `operation` = `sendMessage` or `editMessage` or `sendDocument` | — |
| `disablePreview` | Disable link preview | `boolean` | `false` | no | — | shown when `operation` = `sendMessage` or `editMessage` | — |
| `replyTo` | Reply to message ID | `number` | `0` | no | — | shown when `operation` = `sendMessage` or `sendDocument` | 0 sends a standalone message. |
<!-- END GENERATED: engi.chat.telegram -->

## Microsoft Teams

Posts a message to Microsoft Teams in one of two ways, chosen by `mode`. Use the incoming webhook for simple notifications to one channel, and Graph when you need to pick the team and channel in the workflow.

**Behaviour**

- **Incoming webhook mode** posts to the URL in an [`engi.teamsWebhook`](11-credentials.md#microsoft-teams-webhook) credential. With a `card`: an object whose `type` is `AdaptiveCard` is wrapped in a message attachment; any other card object (for example a MessageCard) is sent as is. Without a card, `text` is sent as a simple MessageCard. If neither is set the node fails with `text or card is required`. Success returns the HTTP status.
- **Graph mode** posts a channel message through Microsoft Graph using an [`engi.oauth2`](11-credentials.md#oauth2) credential bound to the `graph` slot. It needs `teamId`, `channelId` and `text`; `contentType` is `text` or `html`. The response is Graph's message object. Engi refreshes the OAuth2 token before it expires.
- **Credentials by mode.** Only the slot for the chosen mode needs a credential; the other stays empty.
- **Errors.** A non-2xx webhook answer becomes `teams webhook: <first 200 characters of the reply>`. Graph errors are shown as `teams graph: <code>: <message>`. The webhook URL (which is a secret) and the access token are scrubbed.

- **Rate limits.** A `429` is retried after the service's `Retry-After` wait (one second if absent), up to `nodes.chat.maxRetries` times (default 2). A wait longer than `nodes.chat.maxRetryWait` (default 30s), or still being limited after the last retry, fails the item with a "rate limited" error.
- **Secrets stay out of errors.** Tokens and webhook URLs are scrubbed from every error message.
- **Per item, in order.** One call per input item; an error stops the run and names the item (`item 2: ...`).

**Output.** Webhook mode: `{ "status": 200 }`. Graph mode: the created message (`id`, `createdDateTime`, `webUrl`, ...).

**Example**

```json
{
  "mode": "graph",
  "teamId": "9b3c1c2e-0000-0000-0000-000000000000",
  "channelId": "19:abc123@thread.tacv2",
  "text": "<b>Nightly build</b> passed",
  "contentType": "html"
}
```

**Notes**

- Config: `nodes.chat.graphBaseUrl`, `maxRetries`, `maxRetryWait` ([Deployment](../11-deployment.md)).
- Graph posting to a channel as a user needs a delegated token with a scope that allows sending channel messages (`ChannelMessage.Send`), which usually needs admin consent; application tokens cannot post channel messages this way.
- Common errors: `401`/`403` (expired token or missing consent), `404` (wrong team or channel ID), webhook `4xx` when the connector was removed.
- **Notes.** Posting only: no reading of channels or messages.

<!-- BEGIN GENERATED: engi.chat.teams -->
**Type:** `engi.chat.teams` · **Version:** 1 · **Group:** `action` · **Display name:** Microsoft Teams

Send a card through a Teams incoming webhook, or post a channel message through Microsoft Graph.

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
| `webhook` | `engi.teamsWebhook` | no |
| `graph` | `engi.oauth2` | no |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `mode` | Mode | `options` | `webhook` | no | `webhook` (Incoming webhook)<br>`graph` (Graph: post channel message) | always | — |
| `text` | Text | `string` | — | no | — | always | Plain message. Ignored in webhook mode when a card is given. |
| `card` | Card (JSON) | `json` | — | no | — | shown when `mode` = `webhook` | A MessageCard or an Adaptive Card object. |
| `teamId` | Team ID | `string` | — | yes | — | shown when `mode` = `graph` | — |
| `channelId` | Channel ID | `string` | — | yes | — | shown when `mode` = `graph` | — |
| `contentType` | Content type | `options` | `text` | no | `text` (Text)<br>`html` (HTML) | shown when `mode` = `graph` | — |
<!-- END GENERATED: engi.chat.teams -->

---
**← Previous** [9. Email](09-email.md) · **Index** [README](README.md) · **Next →** [11. Credential Types](11-credentials.md)
