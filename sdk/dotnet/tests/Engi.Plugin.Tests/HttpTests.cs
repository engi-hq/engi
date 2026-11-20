using System.Net;
using System.Net.Http.Headers;
using System.Net.Sockets;
using System.Text;
using System.Text.Json.Nodes;
using Engi.Plugin;

namespace Engi.Plugin.Tests;

public sealed class HttpTests
{
    private static string FreePrefix()
    {
        var l = new TcpListener(IPAddress.Loopback, 0);
        l.Start();
        var port = ((IPEndPoint)l.LocalEndpoint).Port;
        l.Stop();
        return $"http://localhost:{port}/";
    }

    private static async Task<(HttpClient Client, string Prefix, Task Serve, CancellationTokenSource Stop)> StartAsync(Plugin plugin)
    {
        var prefix = FreePrefix();
        var stop = new CancellationTokenSource();
        var serve = Task.Run(() => plugin.ServeHttpAsync(prefix, stop.Token));
        var client = new HttpClient { BaseAddress = new Uri(prefix), Timeout = FakeHost.Timeout };
        for (var i = 0; i < 100 && !serve.IsFaulted; i++)
        {
            try
            {
                using var probe = await client.GetAsync("rpc");
                break;
            }
            catch (HttpRequestException)
            {
                await Task.Delay(50);
            }
        }
        return (client, prefix, serve, stop);
    }

    private static StringContent Rpc(string method, JsonNode? p, long id = 1) =>
        new(new JsonObject { ["jsonrpc"] = "2.0", ["id"] = id, ["method"] = method, ["params"] = p }.ToJsonString(), Encoding.UTF8, "application/json");

    private static async Task<JsonObject> ReadAsync(HttpResponseMessage r) =>
        JsonNode.Parse(await r.Content.ReadAsStringAsync())!.AsObject();

    [Fact]
    public async Task InitializeAndExecute_OverPostRpc()
    {
        var (client, _, serve, stop) = await StartAsync(TestPlugins.With(TestPlugins.ReverseHandler));
        using (client)
        {
            var init = await ReadAsync(await client.PostAsync("rpc", Rpc("initialize", TestPlugins.Json("""{"protocolVersion":1}"""))));
            Assert.Equal("acme.test.reverse", (string)init["result"]!["nodes"]![0]!["type"]!);

            var args = TestPlugins.ExecuteParams(TestPlugins.Items(TestPlugins.Reverse()));
            var run = await ReadAsync(await client.PostAsync("rpc", Rpc("execute", args)));
            Assert.Equal("cba", (string)run["result"]!["outputs"]![0]![0]!["json"]!["reversed"]!);
        }
        await stop.CancelAsync();
        await serve.WaitAsync(FakeHost.Timeout);
    }

    [Fact]
    public async Task WrongMethodAndPath_Answer405And404()
    {
        var (client, _, serve, stop) = await StartAsync(TestPlugins.With(TestPlugins.ReverseHandler));
        using (client)
        {
            using var get = await client.GetAsync("rpc");
            Assert.Equal(HttpStatusCode.MethodNotAllowed, get.StatusCode);
            using var other = await client.PostAsync("elsewhere", Rpc("health", new JsonObject()));
            Assert.Equal(HttpStatusCode.NotFound, other.StatusCode);
        }
        await stop.CancelAsync();
        await serve.WaitAsync(FakeHost.Timeout);
    }

    [Fact]
    public async Task Callbacks_AnswerUnsupported()
    {
        var plugin = TestPlugins.With(async (ctx, ct) =>
        {
            await ctx.EvalAsync("1", 0, ct);
            return NodeResult.Single([]);
        });
        var (client, _, serve, stop) = await StartAsync(plugin);
        using (client)
        {
            var run = await ReadAsync(await client.PostAsync("rpc", Rpc("execute", TestPlugins.ExecuteParams())));
            Assert.Equal(-32003, (int)run["error"]!["code"]!);
        }
        await stop.CancelAsync();
        await serve.WaitAsync(FakeHost.Timeout);
    }

    [Fact]
    public async Task NodeException_IsReturnedAsJsonRpcError()
    {
        var plugin = TestPlugins.With((_, _) => throw new NodeException("bad", httpStatus: 400));
        var (client, _, serve, stop) = await StartAsync(plugin);
        using (client)
        {
            var run = await ReadAsync(await client.PostAsync("rpc", Rpc("execute", TestPlugins.ExecuteParams())));
            Assert.Equal(-32001, (int)run["error"]!["code"]!);
            Assert.Equal(400, (int)run["error"]!["data"]!["httpStatus"]!);
        }
        await stop.CancelAsync();
        await serve.WaitAsync(FakeHost.Timeout);
    }

    [Fact]
    public async Task BearerToken_IsEnforcedWhenConfigured()
    {
        var plugin = TestPlugins.With(TestPlugins.ReverseHandler, new PluginOptions { BearerToken = "s3cret" });
        var (client, _, serve, stop) = await StartAsync(plugin);
        using (client)
        {
            using var denied = await client.PostAsync("rpc", Rpc("health", new JsonObject()));
            Assert.Equal(HttpStatusCode.Unauthorized, denied.StatusCode);

            using var request = new HttpRequestMessage(HttpMethod.Post, "rpc") { Content = Rpc("health", new JsonObject()) };
            request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", "s3cret");
            using var ok = await client.SendAsync(request);
            Assert.Equal(HttpStatusCode.OK, ok.StatusCode);
        }
        await stop.CancelAsync();
        await serve.WaitAsync(FakeHost.Timeout);
    }

    [Fact]
    public async Task OversizeBody_Answers413()
    {
        var plugin = TestPlugins.With(TestPlugins.ReverseHandler, new PluginOptions { MaxMessageBytes = 512 });
        var (client, _, serve, stop) = await StartAsync(plugin);
        using (client)
        {
            using var response = await client.PostAsync("rpc", new StringContent(new string('x', 4000)));
            Assert.Equal(HttpStatusCode.RequestEntityTooLarge, response.StatusCode);
        }
        await stop.CancelAsync();
        await serve.WaitAsync(FakeHost.Timeout);
    }
}
