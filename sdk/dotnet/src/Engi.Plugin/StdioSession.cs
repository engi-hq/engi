using System.Collections.Concurrent;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Engi.Plugin;

/// <summary>
/// One stdio connection: NDJSON JSON-RPC 2.0 in both directions. Requests run as tasks, responses to
/// callbacks are matched by id, and writes are serialized under a semaphore.
/// </summary>
internal sealed class StdioSession : IHostChannel
{
    private const int ReadBufferBytes = 64 << 10;

    private readonly Plugin _plugin;
    private readonly Stream _in;
    private readonly Stream _out;
    private readonly CallGate _gate;
    private readonly int _maxBytes;
    private readonly SemaphoreSlim _writeLock = new(1, 1);
    private readonly ConcurrentDictionary<string, TaskCompletionSource<JsonObject>> _pending = new();
    private readonly ConcurrentDictionary<string, CancellationTokenSource> _inflight = new();
    private readonly ConcurrentDictionary<long, Task> _running = new();
    private readonly CancellationTokenSource _closing = new();
    private readonly TaskCompletionSource _shutdown = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly byte[] _buffer = new byte[ReadBufferBytes];
    private int _start;
    private int _end;
    private long _nextCallId;
    private long _nextTaskId;
    private volatile bool _closed;

    public StdioSession(Plugin plugin, Stream input, Stream output, CallGate gate)
    {
        _plugin = plugin;
        _in = input;
        _out = output;
        _gate = gate;
        _maxBytes = plugin.Options.MaxMessageBytes;
    }

    /// <summary>Serves until shutdown or end of input; in-flight calls finish (shutdown) or are cancelled (EOF, error).</summary>
    public async Task RunAsync(CancellationToken ct)
    {
        using var stop = ct.Register(_closing.Cancel);
        var reader = ReadLoopAsync(_closing.Token);
        try
        {
            var first = await Task.WhenAny(reader, _shutdown.Task).ConfigureAwait(false);
            if (first == reader) await reader.ConfigureAwait(false);
        }
        catch
        {
            await CloseAsync(cancelInFlight: true).ConfigureAwait(false);
            throw;
        }
        var requested = _shutdown.Task.IsCompleted;
        await CloseAsync(cancelInFlight: !requested).ConfigureAwait(false);
    }

    private async Task CloseAsync(bool cancelInFlight)
    {
        if (cancelInFlight) foreach (var cts in _inflight.Values) cts.Cancel();
        await Task.WhenAll(_running.Values).ConfigureAwait(false);
        _closed = true;
        _closing.Cancel();
        foreach (var tcs in _pending.Values) tcs.TrySetException(new IOException("plugin connection closed"));
    }

    private async Task ReadLoopAsync(CancellationToken ct)
    {
        using var line = new MemoryStream();
        while (!_shutdown.Task.IsCompleted)
        {
            var bytes = await ReadLineAsync(line, ct).ConfigureAwait(false);
            if (bytes is null || _shutdown.Task.IsCompleted) return;
            if (bytes.AsSpan().IndexOfAnyExcept((byte)' ', (byte)'\t', (byte)'\r') < 0) continue;
            Dispatch(bytes);
        }
    }

    private async Task<byte[]?> ReadLineAsync(MemoryStream line, CancellationToken ct)
    {
        line.SetLength(0);
        while (true)
        {
            if (_start == _end)
            {
                var n = await _in.ReadAsync(_buffer.AsMemory(), ct).ConfigureAwait(false);
                if (n == 0) return line.Length > 0 ? line.ToArray() : null;
                _start = 0;
                _end = n;
            }
            if (TakeLine(line) is { } complete) return complete;
        }
    }

    /// <summary>Moves buffered bytes into <paramref name="line"/>; returns the line once its newline was seen.</summary>
    private byte[]? TakeLine(MemoryStream line)
    {
        var span = _buffer.AsSpan(_start, _end - _start);
        var nl = span.IndexOf((byte)'\n');
        var take = nl < 0 ? span.Length : nl;
        if (line.Length + take > _maxBytes) throw new MessageTooLargeException(_maxBytes);
        line.Write(span[..take]);
        if (nl < 0)
        {
            _start = _end;
            return null;
        }
        _start += nl + 1;
        return line.ToArray();
    }

    private void Dispatch(byte[] bytes)
    {
        JsonObject msg;
        try
        {
            msg = JsonNode.Parse(bytes) as JsonObject ?? throw new JsonException("not an object");
        }
        catch (JsonException)
        {
            SendSync(RpcMessages.Error(null, ErrorCodes.ParseError, "invalid JSON"));
            return;
        }
        var id = msg["id"];
        var method = msg["method"]?.GetValue<string>();
        var parameters = msg["params"];
        switch (method)
        {
            case null when id is not null:
                if (_pending.TryRemove(RpcMessages.Key(id), out var waiter)) waiter.TrySetResult(msg);
                break;
            case "$/cancel":
                if (parameters?["id"] is { } target && _inflight.TryGetValue(RpcMessages.Key(target), out var cts)) cts.Cancel();
                break;
            case "shutdown" when id is null:
                _shutdown.TrySetResult();
                break;
            case null:
                break;
            default:
                if (id is not null) StartRequest(id, method, parameters);
                break;
        }
    }

    private void StartRequest(JsonNode id, string method, JsonNode? parameters)
    {
        var counted = method == "execute";
        if (counted && !_gate.TryEnter())
        {
            SendSync(RpcMessages.Error(id, ErrorCodes.Busy, "too many concurrent requests"));
            return;
        }
        var key = RpcMessages.Key(id);
        var cts = CancellationTokenSource.CreateLinkedTokenSource(_closing.Token);
        _inflight[key] = cts;
        var taskId = Interlocked.Increment(ref _nextTaskId);
        var done = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        _running[taskId] = done.Task;
        _ = Task.Run(async () =>
        {
            try
            {
                JsonObject reply;
                try
                {
                    var result = await _plugin.HandleAsync(method, parameters, this, cts.Token).ConfigureAwait(false);
                    reply = RpcMessages.Result(id, result);
                }
                catch (Exception ex)
                {
                    reply = RpcMessages.ErrorFrom(id, ex);
                }
                await SendAsync(reply, id).ConfigureAwait(false);
            }
            catch (Exception)
            {
                // The connection is gone; there is nobody to tell.
            }
            finally
            {
                _inflight.TryRemove(key, out _);
                cts.Dispose();
                if (counted) _gate.Exit();
                _running.TryRemove(taskId, out _);
                done.SetResult();
            }
        });
    }

    // IHostChannel

    public void Notify(string method, JsonNode? parameters)
    {
        if (_closed) return;
        try { SendSync(RpcMessages.Notification(method, parameters)); }
        catch (IOException) { }
        catch (ObjectDisposedException) { }
    }

    public async Task<JsonNode?> CallAsync(string method, JsonNode? parameters, CancellationToken ct)
    {
        if (_closed) throw new IOException("plugin connection closed");
        var id = Interlocked.Increment(ref _nextCallId);
        var key = id.ToString(System.Globalization.CultureInfo.InvariantCulture);
        var tcs = new TaskCompletionSource<JsonObject>(TaskCreationOptions.RunContinuationsAsynchronously);
        _pending[key] = tcs;
        try
        {
            await SendAsync(RpcMessages.Request(id, method, parameters), null).ConfigureAwait(false);
            using var reg = ct.Register(() =>
            {
                tcs.TrySetCanceled(ct);
                Notify("$/cancel", new JsonObject { ["id"] = id });
            });
            var reply = await tcs.Task.ConfigureAwait(false);
            if (reply["error"] is JsonObject err)
            {
                var code = err["code"]?.GetValue<int>() ?? ErrorCodes.InternalError;
                var message = err["message"]?.GetValue<string>() ?? "error";
                throw code == ErrorCodes.Unsupported
                    ? new UnsupportedException(method)
                    : new RpcException(code, message, err["data"]?.DeepClone());
            }
            return reply["result"]?.DeepClone();
        }
        finally
        {
            _pending.TryRemove(key, out _);
        }
    }

    // Writing

    private byte[] Encode(JsonObject message)
    {
        var bytes = JsonSerializer.SerializeToUtf8Bytes(message, WireJson.Options);
        return bytes.Length + 1 > _maxBytes
            ? throw new MessageTooLargeException(_maxBytes)
            : [.. bytes, (byte)'\n'];
    }

    /// <summary>Writes a message; a reply too large to send is replaced by an error reply.</summary>
    private async Task SendAsync(JsonObject message, JsonNode? replyId)
    {
        byte[] line;
        try
        {
            line = Encode(message);
        }
        catch (MessageTooLargeException ex) when (message.ContainsKey("result") || message.ContainsKey("error"))
        {
            line = Encode(RpcMessages.Error(replyId, ErrorCodes.InternalError, ex.Message));
        }
        await _writeLock.WaitAsync().ConfigureAwait(false);
        try
        {
            await _out.WriteAsync(line).ConfigureAwait(false);
            await _out.FlushAsync().ConfigureAwait(false);
        }
        finally
        {
            _writeLock.Release();
        }
    }

    private void SendSync(JsonObject message)
    {
        var line = Encode(message);
        _writeLock.Wait();
        try
        {
            _out.Write(line);
            _out.Flush();
        }
        finally
        {
            _writeLock.Release();
        }
    }
}
