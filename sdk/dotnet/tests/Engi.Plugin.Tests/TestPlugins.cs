using System.Text.Json.Nodes;
using Engi.Plugin;

namespace Engi.Plugin.Tests;

internal static class TestPlugins
{
    public static NodeDescriptor Descriptor(string type = "acme.test.reverse") => new()
    {
        Type = type,
        Version = 1,
        DisplayName = "Reverse",
        Group = NodeGroup.Transform,
        Inputs = [Port.Main()],
        Outputs = [Port.Main()],
        Properties = [new Property { Name = "field", Type = PropertyType.String, Default = JsonValue.Create("text") }],
    };

    public static Plugin New(PluginOptions? options = null) => new("acme-test", "1.2.3", options);

    /// <summary>A plugin with the node <c>acme.test.reverse</c> backed by <paramref name="handler"/>.</summary>
    public static Plugin With(NodeHandler handler, PluginOptions? options = null) =>
        New(options).AddNode(Descriptor(), handler);

    public static JsonObject ExecuteParams(
        JsonArray? items = null, JsonObject? param = null, JsonObject? paramErrors = null, string callId = "call-1") => new()
    {
        ["callId"] = callId,
        ["node"] = new JsonObject { ["type"] = "acme.test.reverse", ["version"] = 1 },
        ["inputs"] = new JsonArray(items ?? new JsonArray()),
        ["params"] = new JsonArray(param ?? new JsonObject()),
        ["paramErrors"] = new JsonArray(paramErrors ?? new JsonObject()),
        ["execution"] = new JsonObject { ["executionId"] = "ex_1", ["workflowId"] = "wf_1", ["workflowName"] = "Demo", ["mode"] = "manual", ["nodeName"] = "Reverse", ["runIndex"] = 0, ["attempt"] = 1, ["depth"] = 0 },
    };

    public static JsonArray Items(params JsonObject[] payloads) =>
        new(payloads.Select(p => (JsonNode)new JsonObject { ["json"] = p }).ToArray());

    public static JsonObject Json(string text) => (JsonObject)JsonNode.Parse(text)!;

    public static JsonObject Reverse(string field = "text") => Json($$"""{"{{field}}":"abc"}""");

    public static Task<NodeResult> ReverseHandler(NodeContext ctx, CancellationToken ct)
    {
        var field = ctx.Param<string>("field") ?? "text";
        var items = ctx.Items(0).Select((item, i) =>
        {
            var text = item.Json[field]?.GetValue<string>() ?? "";
            item.Json["reversed"] = new string(text.Reverse().ToArray());
            item.PairedItem = [new PairedItem(0, i)];
            return item;
        });
        return Task.FromResult(NodeResult.Single(items));
    }
}
