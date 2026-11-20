using System.Text.Json;
using System.Text.Json.Nodes;

namespace Engi.Plugin;

/// <summary>Everything a node handler may read about one <c>execute</c> call.</summary>
public sealed class NodeContext
{
    private readonly ExecuteParams _call;
    private readonly IHostChannel _host;
    private readonly CancellationToken _callToken;

    internal NodeContext(ExecuteParams call, IHostChannel host, CancellationToken callToken)
    {
        _call = call;
        _host = host;
        _callToken = callToken;
        Blob = new BlobClient(host, call.CallId, callToken);
        Logger = new PluginLogger((level, message, attrs) => host.Notify("log", BuildLog(call.CallId, level, message, attrs)));
    }

    /// <summary>The node being run.</summary>
    public NodeRef Node => _call.Node;

    /// <summary>The execution this call belongs to.</summary>
    public ExecutionInfo Execution => _call.Execution;

    /// <summary>Number of main input ports with data.</summary>
    public int InputCount => _call.Inputs.Count;

    /// <summary>Number of resolved parameter sets (items of input 0, at least one).</summary>
    public int ParamSetCount => _call.Params.Count;

    /// <summary>Blob access (stdio only).</summary>
    public BlobClient Blob { get; }

    /// <summary>Sends log lines to the host.</summary>
    public PluginLogger Logger { get; }

    /// <summary>Returns deep copies of the items of an input port, so a handler may mutate them.</summary>
    public List<Item> Items(int port = 0)
    {
        if (port < 0 || port >= _call.Inputs.Count) return [];
        return _call.Inputs[port].Select(i => i.Clone()).ToList();
    }

    /// <summary>
    /// Returns a parameter resolved against item <paramref name="itemIndex"/> of input 0, or null when absent.
    /// Throws <see cref="ParamException"/> with the host's message when the host could not resolve it.
    /// </summary>
    public JsonNode? Param(string name, int itemIndex = 0)
    {
        if (itemIndex < 0 || itemIndex >= _call.Params.Count)
            throw new ArgumentOutOfRangeException(nameof(itemIndex), $"item {itemIndex} has no resolved parameters");
        var errors = _call.ParamErrors;
        if (errors is not null && itemIndex < errors.Count && errors[itemIndex].TryGetValue(name, out var message))
            throw new ParamException(name, message, itemIndex);
        return _call.Params[itemIndex].TryGetPropertyValue(name, out var value) ? value?.DeepClone() : null;
    }

    /// <summary>Like <see cref="Param(string, int)"/>, converted to <typeparamref name="T"/>; default when absent.</summary>
    public T? Param<T>(string name, int itemIndex = 0)
    {
        var node = Param(name, itemIndex);
        return node is null ? default : node.Deserialize<T>(WireJson.Options);
    }

    /// <summary>Returns a bound credential, or null when none is bound. Never log it.</summary>
    public Credential? Credential(string name)
    {
        if (_call.CredentialErrors is { } errors && errors.TryGetValue(name, out var message))
            throw new NodeException($"credential \"{name}\": {message}");
        return _call.Credentials is { } map && map.TryGetValue(name, out var c) ? c : null;
    }

    /// <summary>Evaluates an expression (the text inside <c>{{ }}</c>) in the scope of an item (stdio only).</summary>
    public async Task<JsonNode?> EvalAsync(string expression, int itemIndex = 0, CancellationToken ct = default)
    {
        using var linked = CancellationTokenSource.CreateLinkedTokenSource(_callToken, ct);
        var p = new JsonObject { ["callId"] = _call.CallId, ["expression"] = expression, ["itemIndex"] = itemIndex };
        var result = await _host.CallAsync("expression.eval", p, linked.Token).ConfigureAwait(false);
        return result?["value"]?.DeepClone();
    }

    private static JsonObject BuildLog(string callId, LogLevel level, string message, IReadOnlyDictionary<string, object?>? attrs)
    {
        var p = new JsonObject
        {
            ["level"] = level.ToString().ToLowerInvariant(),
            ["message"] = message,
        };
        // The host logs a line that names its call with that run's node logger.
        if (callId.Length > 0) p["callId"] = callId;
        if (attrs is { Count: > 0 }) p["attrs"] = JsonSerializer.SerializeToNode(attrs, WireJson.Options);
        return p;
    }
}
