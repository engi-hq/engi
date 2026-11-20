# 9. Email

Send email. To receive it, see the [IMAP Trigger](01-triggers.md#imap-trigger).

## Send Email

Sends one email per input item over SMTP. Use it for notifications, reports with attachments, or messages built from data earlier in the workflow. To react to incoming mail, use the [IMAP Trigger](01-triggers.md#imap-trigger).

**Behaviour**

- **One message per item.** All parameters are evaluated per item. Recipients (`to`, `cc`, `bcc`, `replyTo`) can be a comma-separated string or a list from an expression; `Name <addr@example.com>` form works.
- **From.** Uses the node's `from`, otherwise the credential's `fromAddress`; if neither is set the node fails.
- **Bodies.** Set `text`, `html` or both. Both makes a `multipart/alternative` message; neither is an error. Subjects and names are encoded for non-ASCII text.
- **Attachments.** `attachments` lists binary field names of the item (for example `data, invoice`). Each becomes an attachment named after the binary's file name (or the field name) with its MIME type. A missing field fails the item.
- **Bcc** recipients receive the mail but never appear in the headers.
- **Header injection is refused.** Subject, addresses and the message id are rejected if they contain a line break or control character, so a value from an incoming webhook cannot add headers or recipients. Header lines longer than the limit are rejected too.
- **Build first, send second.** Every message is built and size-checked before the SMTP connection is opened, so a bad item fails the run before anything is sent. Then all messages go over one connection, in order.
- **Partial recipients.** A recipient the server refuses is listed in `rejected`; the item fails only when every recipient is refused. Authentication and TLS follow the credential: `starttls` (default), `tls` or `none`. PLAIN login is refused over an unencrypted link to a remote host.

**Output.** One item per sent message.

```json
{
  "messageId": "9f2c0b1e7a3d4c5e8b6a1f0d2c3b4a59@example.com",
  "from": "noreply@example.com",
  "accepted": ["ada@example.org"],
  "rejected": []
}
```

`accepted` means the SMTP server took the message, not that it was delivered.

**Example**

```json
{
  "to": "{{ $json.email }}",
  "subject": "Your invoice {{ $json.number }}",
  "text": "Hello {{ $json.name }}, your invoice is attached.",
  "html": "<p>Hello {{ $json.name }}, your invoice is attached.</p>",
  "attachments": "invoice"
}
```

**Notes**

- Config ([Deployment](../11-deployment.md)): `nodes.email.maxRecipients` (to + cc + bcc per message, default 100), `nodes.email.maxMessageBytes` (whole message with attachments, default 25 MiB), `nodes.email.sendTimeout` (whole run, default 2m), `nodes.email.connectTimeout`.
- Common errors: `the message has no recipients`, `no From address`, `the item has no binary field "x" to attach`, `SMTP authentication: ...`, `every recipient was refused`.
- Credential: [SMTP](11-credentials.md#smtp).
- **Notes.** No per-node SMTP settings (everything is in the credential), no inline images or custom headers, and the header-injection and size checks run before any connection is made.

<!-- BEGIN GENERATED: engi.email.send -->
**Type:** `engi.email.send` · **Version:** 1 · **Group:** `action` · **Display name:** Send Email

Send one email per input item over SMTP.

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
| `engi.smtp` | `engi.smtp` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `from` | From | `string` | — | no | — | always | Empty uses the credential's default from address. |
| `to` | To | `string` | — | yes | — | always | Comma-separated addresses, or a list from an expression. |
| `cc` | Cc | `string` | — | no | — | always | — |
| `bcc` | Bcc | `string` | — | no | — | always | — |
| `replyTo` | Reply-To | `string` | — | no | — | always | — |
| `subject` | Subject | `string` | — | yes | — | always | — |
| `text` | Text body | `string` | — | no | — | always | Plain-text body. Set text, HTML or both. |
| `html` | HTML body | `string` | — | no | — | always | With both bodies the mail is multipart/alternative. |
| `attachments` | Attachments | `string` | — | no | — | always | Comma-separated names of the binary fields of the item to attach. |
<!-- END GENERATED: engi.email.send -->

---
**← Previous** [8. Storage](08-storage.md) · **Index** [README](README.md) · **Next →** [10. Chat](10-chat.md)
