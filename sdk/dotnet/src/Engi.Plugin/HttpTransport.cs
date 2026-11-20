using System.Net;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Engi.Plugin;

/// <summary>The HTTP transport: <c>POST {prefix}rpc</c>, one JSON-RPC message per request, no host callbacks.</summary>
internal sealed class HttpTransport(Plugin plugin, CallGate gate)
{
    private readonly IHostChannel _channel = new NoHostChannel();
    private readonly HashSet<Task> _handlers = [];

    public async Task RunAsync(string prefix, CancellationToken ct)
    {
        if (!prefix.EndsWith('/')) prefix += "/";
        var rpcPath = new Uri(prefix.Replace("+", "localhost").Replace("*", "localhost")).AbsolutePath + "rpc";
        using var listener = new HttpListener();
        listener.Prefixes.Add(prefix);
        listener.Start();
        using var stop = CancellationTokenSource.CreateLinkedTokenSource(ct);
        using var reg = stop.Token.Register(listener.Stop);
        try
        {
            while (!stop.IsCancellationRequested)
            {
                HttpListenerContext context;
                try
                {
                    context = await listener.GetContextAsync().ConfigureAwait(false);
                }
                catch (Exception ex) when (ex is HttpListenerException or ObjectDisposedException or InvalidOperationException)
                {
                    break;
                }
                Track(Task.Run(() => HandleAsync(context, rpcPath, stop)));
            }
        }
        finally
        {
            Task[] pending;
            lock (_handlers) pending = [.. _handlers];
            await Task.WhenAll(pending).ConfigureAwait(false);
        }
    }

    private void Track(Task task)
    {
        lock (_handlers) _handlers.Add(task);
        _ = task.ContinueWith(t => { lock (_handlers) _handlers.Remove(t); }, TaskScheduler.Default);
    }

    private async Task HandleAsync(HttpListenerContext context, string rpcPath, CancellationTokenSource server)
    {
        var response = context.Response;
        try
        {
            var request = context.Request;
            if (request.Url?.AbsolutePath != rpcPath) { Status(response, 404); return; }
            if (request.HttpMethod != "POST")
            {
                response.AddHeader("Allow", "POST");
                Status(response, 405);
                return;
            }
            if (!Authorized(request.Headers["Authorization"])) { Status(response, 401); return; }

            var body = await ReadBodyAsync(request.InputStream, server.Token).ConfigureAwait(false);
            if (body is null) { Status(response, 413); return; }
            var reply = await ProcessAsync(body, server).ConfigureAwait(false);
            if (reply is null) { Status(response, 204); return; }
            var bytes = JsonSerializer.SerializeToUtf8Bytes(reply, WireJson.Options);
            response.ContentType = "application/json";
            response.ContentLength64 = bytes.Length;
            await response.OutputStream.WriteAsync(bytes).ConfigureAwait(false);
            response.Close();
        }
        catch (Exception)
        {
            try { Status(response, 500); } catch (Exception) { /* client is gone */ }
        }
    }

    private async Task<JsonObject?> ProcessAsync(byte[] body, CancellationTokenSource server)
    {
        JsonObject msg;
        try
        {
            msg = JsonNode.Parse(body) as JsonObject ?? throw new JsonException("not an object");
        }
        catch (JsonException)
        {
            return RpcMessages.Error(null, ErrorCodes.ParseError, "invalid JSON");
        }
        var id = msg["id"];
        var method = msg["method"]?.GetValue<string>();
        if (method is null) return RpcMessages.Error(id, ErrorCodes.InvalidRequest, "method is required");
        if (id is null)
        {
            if (method == "shutdown") await server.CancelAsync().ConfigureAwait(false);
            return null;
        }
        var counted = method == "execute";
        if (counted && !gate.TryEnter())
            return RpcMessages.Error(id, ErrorCodes.Busy, "too many concurrent requests");
        try
        {
            var result = await plugin.HandleAsync(method, msg["params"], _channel, server.Token).ConfigureAwait(false);
            return RpcMessages.Result(id, result);
        }
        catch (Exception ex)
        {
            return RpcMessages.ErrorFrom(id, ex);
        }
        finally
        {
            if (counted) gate.Exit();
        }
    }

    private bool Authorized(string? header)
    {
        var token = plugin.Options.BearerToken;
        if (string.IsNullOrEmpty(token)) return true;
        const string scheme = "Bearer ";
        if (header is null || !header.StartsWith(scheme, StringComparison.OrdinalIgnoreCase)) return false;
        return CryptographicOperations.FixedTimeEquals(
            Encoding.UTF8.GetBytes(header[scheme.Length..]), Encoding.UTF8.GetBytes(token));
    }

    /// <summary>Reads the body, or returns null when it exceeds the message limit.</summary>
    private async Task<byte[]?> ReadBodyAsync(Stream stream, CancellationToken ct)
    {
        var limit = plugin.Options.MaxMessageBytes;
        using var ms = new MemoryStream();
        var buffer = new byte[16 << 10];
        int n;
        while ((n = await stream.ReadAsync(buffer, ct).ConfigureAwait(false)) > 0)
        {
            if (ms.Length + n > limit) return null;
            ms.Write(buffer, 0, n);
        }
        return ms.ToArray();
    }

    private static void Status(HttpListenerResponse response, int code)
    {
        response.StatusCode = code;
        response.Close();
    }
}
