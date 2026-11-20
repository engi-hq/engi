# Engi.Plugin — C# / .NET SDK

Write [Engi](../../docs/15-plugins.md) plugins in C#. Implements the Engi Plugin Protocol v1 over
stdio (NDJSON JSON-RPC 2.0) and HTTP (`POST /rpc`). Targets `net8.0`, no third-party runtime
dependencies (System.Text.Json only).

## Quick start

```csharp
using System.Text.Json.Nodes;
using Engi.Plugin;

var plugin = new Plugin("example-dotnet-text", "0.1.0");

plugin.AddNode(
    new NodeDescriptor
    {
        Type = "example.dotnet.reverse",          // <vendor>.<area>.<name>, never "engi."
        Version = 1,
        DisplayName = "Reverse (.NET)",
        Group = NodeGroup.Transform,              // action, transform or flow
        Inputs = [Port.Main()],
        Outputs = [Port.Main()],
        Properties = [new Property { Name = "field", Type = PropertyType.String, Default = JsonValue.Create("text") }],
    },
    (ctx, ct) =>
    {
        var items = ctx.Items(0).Select((item, i) =>
        {
            var field = ctx.Param<string>("field", i) ?? "text";
            item.Json["reversed"] = new string((item.Json[field]?.GetValue<string>() ?? "").Reverse().ToArray());
            item.PairedItem = [new PairedItem(0, i)];
            return item;
        });
        return Task.FromResult(NodeResult.Single(items.ToList()));
    });

await plugin.ServeAsync();                         // stdio, for plugins the host starts
// await plugin.ServeHttpAsync("http://+:8080/");  // or HTTP, for a service
```

## API

| Type | Purpose |
|---|---|
| `Plugin(name, version, options?)` | `AddNode(descriptor, handler)`, `ServeAsync()`, `ServeHttpAsync(prefix)` |
| `NodeHandler` | `Task<NodeResult> (NodeContext ctx, CancellationToken ct)`; `ct` is cancelled on `$/cancel` |
| `NodeContext` | `Items(port)` (deep clones), `Param(name, itemIndex)`, `Param<T>`, `Credential(name)`, `EvalAsync(expr, itemIndex)`, `Blob.ReadAsync/WriteAsync`, `Logger`, `Execution` |
| `NodeResult` | `Single(items)`, `Ports(a, b, ...)`, `.WithWait(new Wait { ... })` |
| `NodeException(message, description, httpStatus, itemIndex)` | Shown to the user like a built-in node error (-32001) |
| `ParamException` | Thrown by `Param` when the host could not resolve that property; carries the host's message |
| `PluginOptions` | `MaxMessageBytes` (default 32 MiB), `MaxConcurrentCalls` (default 16), `BearerToken` (HTTP) |
| `NodeDescriptor`, `Property`, `Port`, `Item`, `BinaryRef`, ... | Wire shapes, serialized camelCase with nulls omitted (`WireJson.Options`) |

Behaviour worth knowing:

- Any other exception becomes a -32603 error carrying its message. Cancellation becomes -32002.
- Over stdio, `Console.Out` is redirected to stderr while serving, so a stray `Console.WriteLine`
  cannot corrupt the protocol stream. Use `ctx.Logger` for logs the host should collect.
- Over HTTP the callbacks (`EvalAsync`, `Blob`) throw `UnsupportedException` (-32003) and log lines are dropped.
- Calls above `MaxConcurrentCalls` are answered with -32004. A message above `MaxMessageBytes`
  closes the stdio connection (`ServeAsync` throws `MessageTooLargeException`).
- Descriptors are checked when serving starts with the same rules as the host (`DescriptorValidator`);
  an invalid one throws `InvalidOperationException` listing every problem.
- Never log or persist `Credential` data.

## Build and test

```bash
cd sdk/dotnet
dotnet build -warnaserror
dotnet test
```

The library targets `net8.0`. The tests target `net10.0` (the SDK installed here has no .NET 8 runtime).

## Example: `examples/Text`

```bash
cd sdk/dotnet/examples/Text
dotnet publish -c Release -o dist                  # framework-dependent: needs a .NET 8+ runtime on PATH
mkdir -p /tmp/example-dotnet-text
cp engi-plugin.yaml /tmp/example-dotnet-text/
cp -r dist /tmp/example-dotnet-text/dist           # the plugin directory is manifest + dist/
engi plugin install /tmp/example-dotnet-text
```

The manifest runs `dotnet dist/Text.dll` from the plugin directory. For hosts without a .NET runtime
publish self-contained instead (`dotnet publish -c Release -r linux-x64 --self-contained -o dist`) and
set `command: [./dist/Text]`.

Smoke test without Engi:

```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}' | dotnet dist/Text.dll
```
