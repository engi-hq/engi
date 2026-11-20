using System.Text.Json.Nodes;
using Engi.Plugin;

namespace Engi.Plugin.Tests;

public class StdioTests
{
    private static JsonObject Error(JsonObject response) => response["error"]!.AsObject();

    [Fact]
    public async Task Initialize_AnswersProtocolVersionPluginAndCamelCaseDescriptors()
    {
        await using var host = new FakeHost(TestPlugins.With(TestPlugins.ReverseHandler));
        var response = await host.CallAsync("initialize", TestPlugins.Json("""{"protocolVersion":1,"host":{"name":"engi","version":"dev"}}"""));

        var result = response["result"]!.AsObject();
        Assert.Equal(1, (int)result["protocolVersion"]!);
        Assert.Equal("acme-test", (string)result["plugin"]!["name"]!);
        Assert.Equal("1.2.3", (string)result["plugin"]!["version"]!);
        var node = result["nodes"]![0]!.AsObject();
        Assert.Equal("acme.test.reverse", (string)node["type"]!);
        Assert.Equal("transform", (string)node["group"]!);
        Assert.Equal("main", (string)node["inputs"]![0]!["kind"]!);
        Assert.Equal("string", (string)node["properties"]![0]!["type"]!);
        Assert.Equal("text", (string)node["properties"]![0]!["default"]!);
        Assert.False(node.ContainsKey("description"), "null members are omitted");
    }

    [Fact]
    public async Task Health_ReturnsEmptyObject()
    {
        await using var host = new FakeHost(TestPlugins.With(TestPlugins.ReverseHandler));
        var response = await host.CallAsync("health", new JsonObject());
        Assert.Empty(response["result"]!.AsObject());
    }

    [Fact]
    public async Task Execute_RunsHandlerAndPairsItems()
    {
        await using var host = new FakeHost(TestPlugins.With(TestPlugins.ReverseHandler));
        var args = TestPlugins.ExecuteParams(
            TestPlugins.Items(TestPlugins.Reverse("name"), TestPlugins.Reverse("name")),
            new JsonObject { ["field"] = "name" });

        var response = await host.CallAsync("execute", args);

        var outputs = response["result"]!["outputs"]!.AsArray();
        Assert.Single(outputs);
        Assert.Equal(2, outputs[0]!.AsArray().Count);
        Assert.Equal("cba", (string)outputs[0]![1]!["json"]!["reversed"]!);
        Assert.Equal(1, (int)outputs[0]![1]!["pairedItem"]![0]!["item"]!);
        Assert.Equal(0, (int)outputs[0]![1]!["pairedItem"]![0]!["input"]!);
    }

    [Fact]
    public async Task Items_ReturnsDeepClones()
    {
        JsonNode? seenTwice = null;
        var plugin = TestPlugins.With((ctx, _) =>
        {
            ctx.Items(0)[0].Json["x"] = 1;
            seenTwice = ctx.Items(0)[0].Json["x"];
            return Task.FromResult(NodeResult.Single([]));
        });
        await using var host = new FakeHost(plugin);
        await host.CallAsync("execute", TestPlugins.ExecuteParams(TestPlugins.Items(new JsonObject())));
        Assert.Null(seenTwice);
    }

    [Fact]
    public async Task ParamError_SurfacesHostMessageOnlyWhenRead()
    {
        var plugin = TestPlugins.With((ctx, _) =>
        {
            ctx.Param("fine");
            ctx.Param("field");
            return Task.FromResult(NodeResult.Single([]));
        });
        await using var host = new FakeHost(plugin);

        var args = TestPlugins.ExecuteParams(param: new JsonObject(), paramErrors: new JsonObject { ["field"] = "expression error: boom" });
        var response = await host.CallAsync("execute", args);

        Assert.Equal(-32001, (int)Error(response)["code"]!);
        Assert.Equal("expression error: boom", (string)Error(response)["message"]!);
        Assert.Equal(0, (int)Error(response)["data"]!["itemIndex"]!);
    }

    [Fact]
    public async Task NodeException_MapsToNodeErrorWithData()
    {
        var plugin = TestPlugins.With((_, _) => throw new NodeException("upstream failed", "try again later", 502, 3));
        await using var host = new FakeHost(plugin);

        var error = Error(await host.CallAsync("execute", TestPlugins.ExecuteParams()));

        Assert.Equal(-32001, (int)error["code"]!);
        Assert.Equal("upstream failed", (string)error["message"]!);
        Assert.Equal("try again later", (string)error["data"]!["description"]!);
        Assert.Equal(502, (int)error["data"]!["httpStatus"]!);
        Assert.Equal(3, (int)error["data"]!["itemIndex"]!);
    }

    [Fact]
    public async Task OtherExceptions_MapToInternalError()
    {
        var plugin = TestPlugins.With((_, _) => throw new InvalidOperationException("oops"));
        await using var host = new FakeHost(plugin);

        var error = Error(await host.CallAsync("execute", TestPlugins.ExecuteParams()));

        Assert.Equal(-32603, (int)error["code"]!);
        Assert.Equal("oops", (string)error["message"]!);
    }

    [Fact]
    public async Task UnknownMethodAndUnknownNode_AreRejected()
    {
        await using var host = new FakeHost(TestPlugins.With(TestPlugins.ReverseHandler));

        Assert.Equal(-32601, (int)Error(await host.CallAsync("nope"))["code"]!);

        var args = TestPlugins.ExecuteParams();
        args["node"]!["type"] = "acme.test.missing";
        Assert.Equal(-32602, (int)Error(await host.CallAsync("execute", args))["code"]!);
    }

    [Fact]
    public async Task InvalidJson_AnswersParseErrorAndKeepsServing()
    {
        await using var host = new FakeHost(TestPlugins.With(TestPlugins.ReverseHandler));
        await host.SendRawAsync("{not json");
        await host.SendRawAsync("");

        var response = await host.CallAsync("health", new JsonObject());
        Assert.Empty(response["result"]!.AsObject());
        Assert.Contains(host.RawLines, l => l.Contains("-32700"));
    }

    [Fact]
    public async Task Callbacks_EvalAndBlobRoundTripWithCallId()
    {
        var plugin = TestPlugins.With(async (ctx, ct) =>
        {
            var value = await ctx.EvalAsync("$json.name", 2, ct);
            var written = await ctx.Blob.WriteAsync("a.txt", "text/plain", [1, 2, 3], ct);
            var read = await ctx.Blob.ReadAsync(written, ct);
            return NodeResult.Single([new Item(new JsonObject { ["value"] = value, ["bytes"] = read.Length, ["ref"] = written.Ref })]);
        });
        await using var host = new FakeHost(plugin);
        var seen = new List<(string Method, JsonObject Params)>();
        host.OnRequest = (method, p) =>
        {
            seen.Add((method, p));
            return method switch
            {
                "expression.eval" => new JsonObject { ["value"] = "Ada" },
                "blob.write" => new JsonObject { ["ref"] = "blob-1", ["mime"] = "text/plain", ["name"] = "a.txt", ["size"] = 3 },
                "blob.read" => new JsonObject { ["data"] = Convert.ToBase64String([1, 2, 3]) },
                _ => throw new InvalidOperationException(method),
            };
        };

        var response = await host.CallAsync("execute", TestPlugins.ExecuteParams(callId: "call-xyz"));

        var json = response["result"]!["outputs"]![0]![0]!["json"]!;
        Assert.Equal("Ada", (string)json["value"]!);
        Assert.Equal(3, (int)json["bytes"]!);
        Assert.Equal("blob-1", (string)json["ref"]!);
        Assert.Equal(["expression.eval", "blob.write", "blob.read"], seen.Select(s => s.Method));
        Assert.All(seen, s => Assert.Equal("call-xyz", (string)s.Params["callId"]!));
        Assert.Equal(2, (int)seen[0].Params["itemIndex"]!);
        Assert.Equal("$json.name", (string)seen[0].Params["expression"]!);
        Assert.Equal(Convert.ToBase64String([1, 2, 3]), (string)seen[1].Params["data"]!);
        Assert.Equal("blob-1", (string)seen[2].Params["ref"]!["ref"]!);
    }

    [Fact]
    public async Task Logger_SendsLogNotification()
    {
        var plugin = TestPlugins.With((ctx, _) =>
        {
            ctx.Logger.Info("hello", new Dictionary<string, object?> { ["n"] = 5 });
            return Task.FromResult(NodeResult.Single([]));
        });
        await using var host = new FakeHost(plugin);
        _ = host.CallAsync("execute", TestPlugins.ExecuteParams(callId: "call-log"));

        var log = await host.NextNotificationAsync();

        Assert.Equal("log", (string)log["method"]!);
        Assert.Equal("info", (string)log["params"]!["level"]!);
        Assert.Equal("hello", (string)log["params"]!["message"]!);
        Assert.Equal(5, (int)log["params"]!["attrs"]!["n"]!);
        Assert.Equal("call-log", (string)log["params"]!["callId"]!);
    }

    [Fact]
    public async Task Credential_IsReadFromCall()
    {
        var plugin = TestPlugins.With((ctx, _) =>
        {
            var key = ctx.Credential("api")!.Data["key"]!.GetValue<string>();
            return Task.FromResult(NodeResult.Single([new Item(new JsonObject { ["len"] = key.Length })]));
        });
        await using var host = new FakeHost(plugin);
        var args = TestPlugins.ExecuteParams();
        args["credentials"] = TestPlugins.Json("""{"api":{"type":"acme.apiKey","id":"cr_1","version":3,"data":{"key":"secret"}}}""");

        var response = await host.CallAsync("execute", args);

        Assert.Equal(6, (int)response["result"]!["outputs"]![0]![0]!["json"]!["len"]!);
    }

    [Fact]
    public async Task Wait_IsSerialized()
    {
        var until = new DateTimeOffset(2030, 1, 2, 3, 4, 5, TimeSpan.Zero);
        var plugin = TestPlugins.With((_, _) =>
            Task.FromResult(NodeResult.Single([]).WithWait(new Wait { Until = until, ResumeToken = "tok" })));
        await using var host = new FakeHost(plugin);

        var response = await host.CallAsync("execute", TestPlugins.ExecuteParams());

        Assert.Equal("tok", (string)response["result"]!["wait"]!["resumeToken"]!);
        Assert.Equal(until, DateTimeOffset.Parse((string)response["result"]!["wait"]!["until"]!));
    }

    [Fact]
    public async Task Cancel_CancelsTheHandlerToken()
    {
        var started = new TaskCompletionSource();
        var plugin = TestPlugins.With(async (_, ct) =>
        {
            started.SetResult();
            await Task.Delay(Timeout.Infinite, ct);
            return NodeResult.Single([]);
        });
        await using var host = new FakeHost(plugin);
        var (id, task) = host.StartCall("execute", TestPlugins.ExecuteParams());
        await started.Task.WaitAsync(FakeHost.Timeout);

        await host.NotifyAsync("$/cancel", new JsonObject { ["id"] = id });

        Assert.Equal(-32002, (int)Error(await task)["code"]!);
    }

    [Fact]
    public async Task Busy_AboveMaxConcurrentCalls()
    {
        var release = new TaskCompletionSource();
        var started = new TaskCompletionSource();
        var plugin = TestPlugins.With(async (_, _) =>
        {
            started.TrySetResult();
            await release.Task;
            return NodeResult.Single([]);
        }, new PluginOptions { MaxConcurrentCalls = 1 });
        await using var host = new FakeHost(plugin);
        var (_, first) = host.StartCall("execute", TestPlugins.ExecuteParams());
        await started.Task.WaitAsync(FakeHost.Timeout);

        var second = await host.CallAsync("execute", TestPlugins.ExecuteParams());
        Assert.Equal(-32004, (int)Error(second)["code"]!);

        release.SetResult();
        Assert.NotNull((await first)["result"]);
        var third = await host.CallAsync("execute", TestPlugins.ExecuteParams());
        Assert.NotNull(third["result"]);
    }

    [Fact]
    public async Task OversizeMessage_ClosesTheConnection()
    {
        var plugin = TestPlugins.With(TestPlugins.ReverseHandler, new PluginOptions { MaxMessageBytes = 1024 });
        await using var small = new FakeHost(plugin);

        await small.SendRawAsync(new string('x', 4000));

        await Assert.ThrowsAsync<MessageTooLargeException>(() => small.Serve.WaitAsync(FakeHost.Timeout));
    }

    [Fact]
    public async Task OversizeReply_BecomesInternalError()
    {
        var plugin = TestPlugins.With((_, _) =>
            Task.FromResult(NodeResult.Single([new Item(new JsonObject { ["big"] = new string('y', 5000) })])),
            new PluginOptions { MaxMessageBytes = 2048 });
        await using var host = new FakeHost(plugin);

        var response = await host.CallAsync("execute", TestPlugins.ExecuteParams());

        Assert.Equal(-32603, (int)Error(response)["code"]!);
    }

    [Fact]
    public async Task StrayConsoleWrites_GoToStderrNotTheProtocolStream()
    {
        var original = Console.Out;
        var captured = new StringWriter();
        Console.SetOut(captured);
        try
        {
            var plugin = TestPlugins.With((_, _) =>
            {
                Console.WriteLine("stray output");
                return Task.FromResult(NodeResult.Single([]));
            });
            await using (var host = new FakeHost(plugin, redirectConsole: true))
            {
                await host.CallAsync("execute", TestPlugins.ExecuteParams());
                Assert.All(host.RawLines, l => Assert.NotNull(JsonNode.Parse(l)));
                Assert.DoesNotContain(host.RawLines, l => l.Contains("stray"));
            }
            Assert.Empty(captured.ToString());
            Console.Write("after");
            Assert.Equal("after", captured.ToString());
        }
        finally
        {
            Console.SetOut(original);
        }
    }

    [Fact]
    public async Task Shutdown_WaitsForInFlightThenReturns()
    {
        var release = new TaskCompletionSource();
        var started = new TaskCompletionSource();
        var plugin = TestPlugins.With(async (_, _) =>
        {
            started.SetResult();
            await release.Task;
            return NodeResult.Single([]);
        });
        await using var host = new FakeHost(plugin);
        var (_, call) = host.StartCall("execute", TestPlugins.ExecuteParams());
        await started.Task.WaitAsync(FakeHost.Timeout);

        await host.NotifyAsync("shutdown", new JsonObject());
        await Task.Delay(100);
        Assert.False(host.Serve.IsCompleted, "must wait for the call in flight");

        release.SetResult();
        Assert.NotNull((await call)["result"]);
        await host.Serve.WaitAsync(FakeHost.Timeout);
    }

    [Fact]
    public async Task ClosedInput_CancelsInFlightCallsAndReturns()
    {
        var started = new TaskCompletionSource();
        var cancelled = new TaskCompletionSource();
        var plugin = TestPlugins.With(async (_, ct) =>
        {
            started.SetResult();
            try { await Task.Delay(Timeout.Infinite, ct); }
            catch (OperationCanceledException) { cancelled.SetResult(); throw; }
            return NodeResult.Single([]);
        });
        var host = new FakeHost(plugin);
        _ = host.StartCall("execute", TestPlugins.ExecuteParams());
        await started.Task.WaitAsync(FakeHost.Timeout);

        await host.DisposeAsync();

        await cancelled.Task.WaitAsync(FakeHost.Timeout);
    }
}
