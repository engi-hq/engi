# engi-plugin (Python)

SDK for writing [Engi](../../docs/15-plugins.md) plugins in Python 3.9+. Standard library
only. It speaks the Engi Plugin Protocol v1 over stdio (NDJSON JSON-RPC) or HTTP.

## Quick start

```python
from engi_plugin import Plugin, Property

plugin = Plugin(name="acme-text", version="1.0.0")

@plugin.node(
    type="acme.text.reverse", display_name="Reverse", group="transform",
    properties=[Property("field", default="text", required=True)],
)
def run(ctx):
    out = []
    for i, item in enumerate(ctx.items()):
        text = item["json"][ctx.param("field", i)]
        out.append({"json": {"reversed": text[::-1]}, "pairedItem": [{"input": 0, "item": i}]})
    return [out]            # one list of items per output port

if __name__ == "__main__":
    plugin.serve()          # stdio; plugin.serve_http("0.0.0.0", 8443) for a service
```

Pair it with an `engi-plugin.yaml` (see `examples/text/`) and install it with
`engi plugin install <dir>`.

### Packaging a plugin

`engi plugin install` copies the plugin directory and starts **the copy**, so the directory must
be self-contained. Either vendor the SDK next to the entry point, where Python finds it first:

```sh
cp -R examples/text /tmp/my-plugin
cp -R engi_plugin /tmp/my-plugin/
engi plugin install /tmp/my-plugin
```

or `pip install ./sdk/python` into the interpreter that the manifest's `runtime.command` runs on
the server. The example also adds `../../` to `sys.path`, which only helps when it runs from this
checkout.

## API

- `Plugin(name, version, max_message_bytes=33554432, max_concurrent_calls=16)`. Set both
  limits to match the host's `plugins.maxMessageBytes` / `plugins.maxConcurrentCalls`.
  Requests above the cap are answered with code -32004.
- `@plugin.node(type, version=1, display_name, group="action", description, icon, properties,
  inputs, outputs, credentials, merge_policy)`. Ports default to one `main` input and output;
  pass `[]` for none. Groups: `action`, `transform`, `flow`. Types must not start with `engi.`.
- Descriptor parts: `Property`, `Option`, `DisplayOptions`, `Port`, `CredentialRef`. They
  serialize to the camelCase wire JSON. `Property.default=None` means "no default".
- `run(ctx)` returns `list[list[dict]]` (items: `{"json": {...}, "binary": ..., "pairedItem": ...}`)
  or `Output(outputs, wait=Wait(until=..., resume_token=...))`. Each execute request runs in a
  worker thread.
- `Context`: `items(port=0)`, `param(name, item_index=0, default=None)`, `credential(name)`,
  `blob.read(ref)`, `blob.write(name, mime, data)`, `eval(expression, item_index=0)`, `log`
  (`logging.Logger`; pass `extra={"attrs": {...}}` for attributes), `execution`, `cancelled`
  (`threading.Event`, set on `$/cancel`).
- Errors: `NodeError(message, description, http_status, item_index)` fails the node with a
  user-visible message. `ParamError` is raised by `ctx.param` with the host's message.
  Other exceptions become internal errors (-32603). Never log credential data.
- `plugin.serve()` validates descriptors, then serves stdio. While serving, `sys.stdout`
  is redirected to stderr so `print` cannot corrupt the protocol stream. `plugin.serve_http(host,
  port, token=None)` serves `POST /rpc`; `blob`, `eval` raise `UnsupportedError` there.

## Tests

```bash
python3 -m unittest discover -s tests -v
```
