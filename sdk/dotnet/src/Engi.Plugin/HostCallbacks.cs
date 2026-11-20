using System.Text.Json;
using System.Text.Json.Nodes;

namespace Engi.Plugin;

/// <summary>How a call reaches the host: log notifications and callback requests.</summary>
internal interface IHostChannel
{
    /// <summary>Sends a notification; transports without a back channel drop it.</summary>
    void Notify(string method, JsonNode? parameters);

    /// <summary>Sends a request to the host and returns its result.</summary>
    Task<JsonNode?> CallAsync(string method, JsonNode? parameters, CancellationToken ct);
}

/// <summary>The channel of a transport without callbacks (HTTP).</summary>
internal sealed class NoHostChannel : IHostChannel
{
    public void Notify(string method, JsonNode? parameters) { }

    public Task<JsonNode?> CallAsync(string method, JsonNode? parameters, CancellationToken ct) =>
        throw new UnsupportedException(method);
}

/// <summary>Reads and writes blobs in the run's blob store (stdio only).</summary>
public sealed class BlobClient
{
    private readonly IHostChannel _host;
    private readonly string _callId;
    private readonly CancellationToken _callToken;

    internal BlobClient(IHostChannel host, string callId, CancellationToken callToken)
    {
        _host = host;
        _callId = callId;
        _callToken = callToken;
    }

    /// <summary>Reads the bytes behind <paramref name="reference"/>.</summary>
    public async Task<byte[]> ReadAsync(BinaryRef reference, CancellationToken ct = default)
    {
        using var linked = CancellationTokenSource.CreateLinkedTokenSource(_callToken, ct);
        var p = new JsonObject
        {
            ["callId"] = _callId,
            ["ref"] = JsonSerializer.SerializeToNode(reference, WireJson.Options),
        };
        var result = await _host.CallAsync("blob.read", p, linked.Token).ConfigureAwait(false);
        var data = result?["data"]?.GetValue<string>() ?? throw new RpcException(ErrorCodes.InternalError, "blob.read: result has no data");
        return Convert.FromBase64String(data);
    }

    /// <summary>Stores <paramref name="data"/> and returns its reference.</summary>
    public async Task<BinaryRef> WriteAsync(string name, string mime, byte[] data, CancellationToken ct = default)
    {
        using var linked = CancellationTokenSource.CreateLinkedTokenSource(_callToken, ct);
        var p = new JsonObject
        {
            ["callId"] = _callId,
            ["name"] = name,
            ["mime"] = mime,
            ["data"] = Convert.ToBase64String(data),
        };
        var result = await _host.CallAsync("blob.write", p, linked.Token).ConfigureAwait(false);
        return result.Deserialize<BinaryRef>(WireJson.Options) ?? throw new RpcException(ErrorCodes.InternalError, "blob.write: empty result");
    }
}
