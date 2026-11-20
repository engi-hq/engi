using System.Text.Json.Nodes;
using Engi.Plugin;

var plugin = new Plugin("example-dotnet-text", "0.1.0");

plugin.AddNode(
    new NodeDescriptor
    {
        Type = "example.dotnet.reverse",
        Version = 1,
        DisplayName = "Reverse (.NET)",
        Description = "Reverses a text field of every item into \"reversed\".",
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
                Description = "The item field to reverse.",
            },
        ],
    },
    (ctx, _) =>
    {
        var items = ctx.Items(0).Select((item, index) =>
        {
            // Parameters are resolved per item, so an expression in "field" may differ between items.
            var field = ctx.Param<string>("field", index) ?? "text";
            if (item.Json[field] is not JsonValue value || !value.TryGetValue<string>(out var text))
                throw new NodeException($"field \"{field}\" is not a string", itemIndex: index);
            item.Json["reversed"] = new string(text.Reverse().ToArray());
            item.PairedItem = [new PairedItem(0, index)];
            return item;
        });
        return Task.FromResult(NodeResult.Single(items.ToList()));
    });

await plugin.ServeAsync();
