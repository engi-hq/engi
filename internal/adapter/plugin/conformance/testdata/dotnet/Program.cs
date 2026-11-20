// Conformance test plugin. Modes: "eval" (stdio, calls expression.eval),
// "http <prefix>" (HTTP transport, reverse node).
using System.Text.Json.Nodes;
using Engi.Plugin;

var mode = args.Length > 0 ? args[0] : "eval";
var plugin = new Plugin("conformance-dotnet", "0.1.0");

if (mode == "eval")
{
    plugin.AddNode(
        new NodeDescriptor
        {
            Type = "conformance.dotnet.eval",
            Version = 1,
            DisplayName = "Eval",
            Group = NodeGroup.Transform,
            Inputs = [Port.Main()],
            Outputs = [Port.Main()],
        },
        async (ctx, ct) =>
        {
            ctx.Logger.Info("evaluating", new Dictionary<string, object?> { ["step"] = "eval" });
            var items = new List<Item>();
            for (var i = 0; i < ctx.Items(0).Count; i++)
            {
                var value = await ctx.EvalAsync("1+1", i, ct);
                items.Add(new Item { Json = new JsonObject { ["value"] = value } });
            }
            return NodeResult.Single(items);
        });
    await plugin.ServeAsync();
}
else
{
    plugin.AddNode(
        new NodeDescriptor
        {
            Type = "example.dotnet.reverse",
            Version = 1,
            DisplayName = "Reverse (.NET)",
            Group = NodeGroup.Transform,
            Inputs = [Port.Main()],
            Outputs = [Port.Main()],
            Properties =
            [
                new Property
                {
                    Name = "field",
                    DisplayName = "Field",
                    Type = PropertyType.String,
                    Default = JsonValue.Create("text"),
                },
            ],
        },
        (ctx, _) =>
        {
            var items = ctx.Items(0).Select((item, index) =>
            {
                var field = ctx.Param<string>("field", index) ?? "text";
                var text = item.Json[field]?.GetValue<string>() ?? "";
                return new Item
                {
                    Json = new JsonObject { ["reversed"] = new string(text.Reverse().ToArray()) },
                    PairedItem = [new PairedItem(0, index)],
                };
            });
            return Task.FromResult(NodeResult.Single(items.ToList()));
        });
    await plugin.ServeHttpAsync(args[1]);
}
