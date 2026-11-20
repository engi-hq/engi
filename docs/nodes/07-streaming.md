# 7. Streaming

Produce to event streams. To consume, see the [Kafka Trigger](01-triggers.md#kafka-trigger).

## Kafka

Publishes a message to a Kafka topic for every input item. Use it to feed events to other systems or to stream the results of a workflow into a pipeline. To start a workflow from Kafka, use the [Kafka Trigger](01-triggers.md#kafka-trigger).

**Behaviour**

- **One record per item.** `topic`, `key`, `value` and `headers` are evaluated per item. Records with the same `key` go to the same partition, in order.
- **Value format.** `json` (default) encodes whatever the expression returns as JSON. `text` sends a string unchanged and encodes any other value as JSON.
- **Headers** are an object of names to values; values are converted to text and added in name order.
- **Acknowledgements.** `all` (default) waits for every in-sync replica, `leader` for the partition leader, `none` does not wait. **Idempotent** (default on) means retries cannot duplicate a record; it needs `acks` = `all`, and the node refuses the combination idempotent + `leader`/`none` with an error telling you which to change.
- **Compression** per batch: `none`, `gzip`, `snappy`, `lz4` or `zstd`.
- **Validate first, then send.** All records are built and size-checked before the first is sent, so a bad item fails the run without publishing anything. Sending then waits for every record to be acknowledged. If a send fails, the run fails with the item number and topic; records already acknowledged stay published.
- **Pooling.** One producer per credential version and `acks`/compression/idempotent combination, shared across runs; flushed on shutdown ([Data Sources 7.5](../07-data-sources.md#75-connection-pooling-database-data-sources), [Triggers 8.9](../08-triggers.md#89-kafka-trigger-and-producer-as-built)).

**Output.** One item per input item with where the record landed.

```json
{ "topic": "orders", "partition": 2, "offset": 18452, "timestamp": "2026-10-18T09:30:00.123Z" }
```

**Example**

```json
{
  "topic": "orders",
  "key": "{{ $json.customerId }}",
  "value": "{{ $json }}",
  "format": "json",
  "headers": { "source": "engi" },
  "acks": "all",
  "compression": "zstd",
  "idempotent": true
}
```

**Notes**

- Config ([Deployment](../11-deployment.md)): `nodes.kafka.maxRecordBytes` (largest key + value, default 1 MiB), `nodes.kafka.produceTimeout` (whole run, default 30s), `nodes.kafka.connectTimeout`, `nodes.kafka.flushTimeout`.
- Common errors: `record is N bytes; the limit is M`, `topic must not be empty`, `an idempotent producer needs acks "all"`, and broker errors such as unknown topic when auto-creation is off.
- Credential: [Kafka](11-credentials.md#kafka) (brokers, SASL, TLS).
- **Notes.** Idempotence is on by default, the producer is shared across runs, and only JSON and text values are supported (no Avro or schema registry).

<!-- BEGIN GENERATED: engi.kafka.produce -->
**Type:** `engi.kafka.produce` · **Version:** 1 · **Group:** `action` · **Display name:** Kafka

Produce a message to a Kafka topic for every input item.

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
| `engi.kafka` | `engi.kafka` | yes |

**Parameters**

| Name | Display name | Type | Default | Required | Options | Shown when | Description |
|---|---|---|---|---|---|---|---|
| `topic` | Topic | `string` | — | yes | — | always | — |
| `key` | Key | `string` | — | no | — | always | Messages with the same key go to the same partition, in order. |
| `value` | Value | `string` | — | no | — | always | JSON format encodes whatever the expression returns; text format sends strings as they are and encodes other values as JSON. |
| `format` | Value format | `options` | `json` | no | `json` (JSON)<br>`text` (Text) | always | — |
| `headers` | Headers | `json` | — | no | — | always | Object of header names to string values. |
| `acks` | Acknowledgements | `options` | `all` | no | `all` (All in-sync replicas)<br>`leader` (Leader only)<br>`none` (None) | always | — |
| `compression` | Compression | `options` | `none` | no | `none` (None)<br>`gzip`<br>`snappy` (Snappy)<br>`lz4` (LZ4)<br>`zstd` (Zstandard) | always | — |
| `idempotent` | Idempotent producer | `boolean` | `true` | no | — | always | Retries never duplicate a message. Needs acknowledgements from all in-sync replicas. |
<!-- END GENERATED: engi.kafka.produce -->

---
**← Previous** [6. Databases](06-databases.md) · **Index** [README](README.md) · **Next →** [8. Storage](08-storage.md)
