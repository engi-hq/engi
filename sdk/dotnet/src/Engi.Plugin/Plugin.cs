using System.Text.Json;
using System.Text.Json.Nodes;

namespace Engi.Plugin;

/// <summary>Runs one node call.</summary>
/// <param name="context">The call's inputs, parameters and host callbacks.</param>
/// <param name="cancellationToken">Cancelled when the host sends <c>$/cancel</c> or the connection closes.</param>
public delegate Task<NodeResult> NodeHandler(NodeContext context, CancellationToken cancellationToken);

/// <summary>
/// An Engi plugin: a set of node types served over stdio or HTTP (Engi Plugin Protocol v1).
/// </summary>
public sealed class Plugin
{
    /// <summary>The protocol version this SDK speaks.</summary>
    public const int ProtocolVersion = 1;

    private readonly List<(NodeDescriptor Descriptor, NodeHandler Handler)> _nodes = [];
    private readonly Lazy<CallGate> _gate;

    /// <summary>Creates a plugin.</summary>
    /// <param name="name">Plugin name (the manifest's name).</param>
    /// <param name="version">Plugin version.</param>
    /// <param name="options">Limits; defaults when null.</param>
    public Plugin(string name, string version, PluginOptions? options = null)
    {
        ArgumentException.ThrowIfNullOrEmpty(name);
        ArgumentException.ThrowIfNullOrEmpty(version);
        Name = name;
        Version = version;
        Options = options ?? new PluginOptions();
        _gate = new Lazy<CallGate>(() => new CallGate(Options.MaxConcurrentCalls));
    }

    /// <summary>Plugin name.</summary>
    public string Name { get; }

    /// <summary>Plugin version.</summary>
    public string Version { get; }

    /// <summary>Limits and settings; change before serving.</summary>
    public PluginOptions Options { get; }

    /// <summary>The registered node descriptors.</summary>
    public IReadOnlyList<NodeDescriptor> Nodes => _nodes.Select(n => n.Descriptor).ToList();

    /// <summary>Registers a node type. Descriptors are validated when serving starts.</summary>
    public Plugin AddNode(NodeDescriptor descriptor, NodeHandler handler)
    {
        ArgumentNullException.ThrowIfNull(descriptor);
        ArgumentNullException.ThrowIfNull(handler);
        if (_nodes.Any(n => n.Descriptor.Type == descriptor.Type))
            throw new ArgumentException($"node type \"{descriptor.Type}\" is already registered", nameof(descriptor));
        _nodes.Add((descriptor, handler));
        return this;
    }

    /// <summary>
    /// Serves the protocol over stdin and stdout until the host sends <c>shutdown</c> or closes stdin.
    /// While serving, <see cref="Console.Out"/> is redirected to stderr so stray writes cannot corrupt the stream.
    /// </summary>
    public Task ServeAsync(CancellationToken cancellationToken = default) =>
        ServeAsync(Console.OpenStandardInput(), Console.OpenStandardOutput(), redirectConsole: true, cancellationToken);

    /// <summary>Serves the protocol over the given streams (used by tests and custom hosts).</summary>
    /// <param name="input">Messages from the host.</param>
    /// <param name="output">Messages to the host.</param>
    /// <param name="redirectConsole">Redirect <see cref="Console.Out"/> to stderr while serving.</param>
    /// <param name="cancellationToken">Stops serving.</param>
    /// <exception cref="MessageTooLargeException">An incoming message exceeded <see cref="PluginOptions.MaxMessageBytes"/>.</exception>
    public async Task ServeAsync(Stream input, Stream output, bool redirectConsole = false, CancellationToken cancellationToken = default)
    {
        Prepare();
        var original = Console.Out;
        if (redirectConsole) Console.SetOut(Console.Error);
        try
        {
            await new StdioSession(this, input, output, _gate.Value).RunAsync(cancellationToken).ConfigureAwait(false);
        }
        finally
        {
            if (redirectConsole) Console.SetOut(original);
        }
    }

    /// <summary>Serves <c>POST {prefix}rpc</c> with an <see cref="System.Net.HttpListener"/> until cancelled.</summary>
    /// <param name="prefix">An HttpListener prefix such as <c>http://localhost:8080/</c>.</param>
    /// <param name="cancellationToken">Stops the server.</param>
    public Task ServeHttpAsync(string prefix, CancellationToken cancellationToken = default)
    {
        Prepare();
        return new HttpTransport(this, _gate.Value).RunAsync(prefix, cancellationToken);
    }

    private void Prepare()
    {
        Options.Validate();
        var errors = _nodes
            .SelectMany(n => DescriptorValidator.Check(n.Descriptor).Select(e => $"node \"{n.Descriptor.Type}\": {e}"))
            .ToList();
        if (_nodes.Count == 0) errors.Add("plugin has no nodes");
        if (errors.Count > 0)
            throw new InvalidOperationException("invalid plugin descriptors:" + string.Concat(errors.Select(e => "\n  - " + e)));
    }

    /// <summary>Handles one request; transports call this and map exceptions with <see cref="RpcMessages.ErrorFrom"/>.</summary>
    internal async Task<JsonNode?> HandleAsync(string method, JsonNode? parameters, IHostChannel host, CancellationToken ct)
    {
        switch (method)
        {
            case "initialize":
                var init = Parse<InitializeParams>(parameters);
                if (init.ProtocolVersion < ProtocolVersion)
                    throw new RpcException(ErrorCodes.InvalidParams, $"unsupported protocol version {init.ProtocolVersion}");
                return RpcMessages.ToNode(new InitializeResult(ProtocolVersion, new PluginInfo(Name, Version), Nodes));
            case "health":
                return new JsonObject();
            case "execute":
                return await ExecuteAsync(Parse<ExecuteParams>(parameters), host, ct).ConfigureAwait(false);
            default:
                throw new RpcException(ErrorCodes.MethodNotFound, $"method not found: {method}");
        }
    }

    private async Task<JsonNode?> ExecuteAsync(ExecuteParams call, IHostChannel host, CancellationToken ct)
    {
        var node = _nodes.FirstOrDefault(n => n.Descriptor.Type == call.Node.Type && n.Descriptor.Version == call.Node.Version);
        if (node.Handler is null)
            throw new RpcException(ErrorCodes.InvalidParams, $"unknown node {call.Node.Type} version {call.Node.Version}");
        if (call.Params.Count == 0) call.Params.Add(new JsonObject());
        var result = await node.Handler(new NodeContext(call, host, ct), ct).ConfigureAwait(false);
        return RpcMessages.ToNode(result.ToWire());
    }

    private static T Parse<T>(JsonNode? parameters)
    {
        try
        {
            return parameters.Deserialize<T>(WireJson.Options)
                ?? throw new RpcException(ErrorCodes.InvalidParams, "params are required");
        }
        catch (JsonException ex)
        {
            throw new RpcException(ErrorCodes.InvalidParams, "invalid params: " + ex.Message);
        }
    }
}
