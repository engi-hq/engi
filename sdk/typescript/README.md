# @engi/plugin-sdk (TypeScript)

SDK for writing [Engi](../../docs/15-plugins.md) plugins in TypeScript or JavaScript
(Node 18+). No runtime dependencies. It speaks the Engi Plugin Protocol v1 over stdio
(NDJSON JSON-RPC) or HTTP.

## Quick start

```ts
import { definePlugin } from "@engi/plugin-sdk";

const plugin = definePlugin({ name: "acme-text", version: "1.0.0" });

plugin.node(
  {
    type: "acme.text.reverse",
    displayName: "Reverse",
    group: "transform",
    properties: [{ name: "field", type: "string", default: "text", required: true }],
  },
  async (ctx) => [
    ctx.items().map((item, i) => ({
      json: { reversed: [...String(item.json[ctx.param<string>("field", i)!])].reverse().join("") },
      pairedItem: [{ input: 0, item: i }],
    })),
  ],
);

await plugin.serve(); // stdio. Or: await plugin.serveHttp({ port: 8443 })
```

Pair it with an `engi-plugin.yaml` (see `examples/text/`) and install it with
`engi plugin install <dir>`. Inside this checkout the example imports `@engi/plugin-sdk`, which
resolves to the package itself, so build first: `npm install && npm run build`, then
`cd examples/text && node main.mjs`.

### Packaging a plugin

`engi plugin install` copies the plugin directory and starts **the copy**, so the directory must
carry its dependencies in its own `node_modules` (or be bundled into one file):

```sh
npm run build && npm pack --pack-destination /tmp            # engi-plugin-sdk-0.1.0.tgz
cp -R examples/text /tmp/my-plugin && cd /tmp/my-plugin
npm init -y && npm install /tmp/engi-plugin-sdk-0.1.0.tgz
engi plugin install /tmp/my-plugin
```

## API

- `definePlugin({ name, version, maxMessageBytes = 33554432, maxConcurrentCalls = 16 })`.
  Set both limits to match the host's `plugins.maxMessageBytes` / `plugins.maxConcurrentCalls`;
  requests above the cap get code -32004.
- `plugin.node(definition, handler)`. Definition: `type`, `version = 1`, `displayName`,
  `group` (`action` | `transform` | `flow`), `description`, `icon`, `properties`, `inputs`,
  `outputs`, `credentials`, `mergePolicy`. Ports default to one `main` input and output; pass
  `[]` for none. Types must not start with `engi.`. Descriptors are validated by `serve()`.
- The handler returns `Item[][]` (one array per output port) or `{ outputs, wait }`.
- `Context`: `items(port = 0)`, `param(name, itemIndex = 0)`, `credential(name)`,
  `blob.read(ref)`, `blob.write(name, mime, data)`, `eval(expression, itemIndex = 0)`,
  `log.{debug,info,warn,error}(message, attrs?)`, `execution`, `signal` (aborted on `$/cancel`).
- Errors: `new NodeError(message, { description, httpStatus, itemIndex })` fails the node with a
  user-visible message. `ParamError` is thrown by `ctx.param` with the host's message. Other
  exceptions become internal errors (-32603). Never log credential data.
- `plugin.serve({ input, output })` resolves at shutdown or end of input and rejects with
  `MessageTooLargeError` for an oversize message. While serving, `console.log` and writes to
  `process.stdout` go to stderr. `plugin.serveHttp({ port, host, token })` serves `POST /rpc`
  and resolves with the listening `http.Server`; `blob` and `eval` reject with `UnsupportedError`.

## Tests

```bash
npm install && npm run build && npm test
```
