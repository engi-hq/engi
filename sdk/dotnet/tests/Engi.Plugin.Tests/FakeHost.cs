using System.IO.Pipes;
using System.Text.Json.Nodes;
using System.Threading.Channels;
using Engi.Plugin;

namespace Engi.Plugin.Tests;

/// <summary>Plays the Go host: talks NDJSON to a plugin over anonymous pipes.</summary>
internal sealed class FakeHost : IAsyncDisposable
{
    public static readonly TimeSpan Timeout = TimeSpan.FromSeconds(10);

    private readonly AnonymousPipeServerStream _toPlugin = new(PipeDirection.Out);
    private readonly AnonymousPipeServerStream _fromPlugin = new(PipeDirection.In);
    private readonly AnonymousPipeClientStream _pluginIn;
    private readonly AnonymousPipeClientStream _pluginOut;
    private readonly StreamWriter _writer;
    private readonly Dictionary<string, TaskCompletionSource<JsonObject>> _responses = new();
    private readonly Channel<JsonObject> _notifications = Channel.CreateUnbounded<JsonObject>();
    private readonly object _lock = new();
    private long _nextId;

    /// <summary>The raw lines the plugin wrote, to prove the stream stays clean.</summary>
    public List<string> RawLines { get; } = [];

    /// <summary>Answers requests from the plugin (callbacks): method and params to result.</summary>
    public Func<string, JsonObject, JsonNode?>? OnRequest { get; set; }

    /// <summary>Completes when the plugin's ServeAsync returns.</summary>
    public Task Serve { get; }

    public FakeHost(Plugin plugin, bool redirectConsole = false)
    {
        _pluginIn = new AnonymousPipeClientStream(PipeDirection.In, _toPlugin.ClientSafePipeHandle);
        _pluginOut = new AnonymousPipeClientStream(PipeDirection.Out, _fromPlugin.ClientSafePipeHandle);
        _writer = new StreamWriter(_toPlugin) { AutoFlush = true };
        Serve = Task.Run(() => plugin.ServeAsync(_pluginIn, _pluginOut, redirectConsole));
        _ = Task.Run(ReadLoop);
    }

    private async Task ReadLoop()
    {
        using var reader = new StreamReader(_fromPlugin);
        while (await reader.ReadLineAsync() is { } line)
        {
            lock (_lock) RawLines.Add(line);
            var msg = JsonNode.Parse(line)!.AsObject();
            if (msg["method"] is null && msg["id"] is not null)
            {
                lock (_lock)
                {
                    if (_responses.Remove(msg["id"]!.ToJsonString(), out var tcs)) tcs.SetResult(msg);
                }
            }
            else if (msg["id"] is { } id)
            {
                var result = OnRequest!(msg["method"]!.GetValue<string>(), msg["params"]!.AsObject());
                await SendAsync(new JsonObject { ["jsonrpc"] = "2.0", ["id"] = id.DeepClone(), ["result"] = result });
            }
            else
            {
                await _notifications.Writer.WriteAsync(msg);
            }
        }
    }

    public Task SendAsync(JsonObject message) => SendRawAsync(message.ToJsonString());

    public async Task SendRawAsync(string line) => await _writer.WriteLineAsync(line);

    /// <summary>Sends a request and returns the full response.</summary>
    public Task<JsonObject> CallAsync(string method, JsonNode? parameters = null) => StartCall(method, parameters).Task;

    public (long Id, Task<JsonObject> Task) StartCall(string method, JsonNode? parameters)
    {
        var id = Interlocked.Increment(ref _nextId);
        var tcs = new TaskCompletionSource<JsonObject>(TaskCreationOptions.RunContinuationsAsynchronously);
        lock (_lock) _responses[id.ToString()] = tcs;
        _ = SendAsync(new JsonObject { ["jsonrpc"] = "2.0", ["id"] = id, ["method"] = method, ["params"] = parameters });
        return (id, tcs.Task.WaitAsync(Timeout));
    }

    public Task NotifyAsync(string method, JsonNode? parameters = null) =>
        SendAsync(new JsonObject { ["jsonrpc"] = "2.0", ["method"] = method, ["params"] = parameters });

    /// <summary>Waits for the next notification from the plugin.</summary>
    public async Task<JsonObject> NextNotificationAsync() =>
        await _notifications.Reader.ReadAsync().AsTask().WaitAsync(Timeout);

    public async ValueTask DisposeAsync()
    {
        await _writer.DisposeAsync();
        try { await Serve.WaitAsync(Timeout); } catch (MessageTooLargeException) { }
        _pluginIn.Dispose();
        _pluginOut.Dispose();
        _fromPlugin.Dispose();
    }
}
