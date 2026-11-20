using System.Text.Json.Nodes;

namespace Engi.Plugin;

/// <summary>One data item flowing between nodes.</summary>
public sealed class Item
{
    /// <summary>The item's JSON payload.</summary>
    public JsonObject Json { get; set; } = new();

    /// <summary>Binary attachments by field name. Bytes never travel in items, only references.</summary>
    public Dictionary<string, BinaryRef>? Binary { get; set; }

    /// <summary>The input items this item derives from.</summary>
    public List<PairedItem>? PairedItem { get; set; }

    /// <summary>A per-item error, for nodes that continue on failure.</summary>
    public ItemError? Error { get; set; }

    /// <summary>Creates an item with an empty payload.</summary>
    public Item() { }

    /// <summary>Creates an item with the given payload.</summary>
    public Item(JsonObject json) => Json = json;

    /// <summary>Returns a deep copy.</summary>
    public Item Clone() => new()
    {
        Json = (JsonObject)Json.DeepClone(),
        Binary = Binary is null ? null : new Dictionary<string, BinaryRef>(Binary),
        PairedItem = PairedItem is null ? null : new List<PairedItem>(PairedItem),
        Error = Error,
    };
}

/// <summary>A reference to bytes held by the host's blob store.</summary>
public sealed record BinaryRef
{
    /// <summary>Opaque blob reference.</summary>
    public string Ref { get; init; } = "";

    /// <summary>MIME type.</summary>
    public string? Mime { get; init; }

    /// <summary>File name.</summary>
    public string? Name { get; init; }

    /// <summary>Size in bytes.</summary>
    public long? Size { get; init; }
}

/// <summary>Points at an input item an output item derives from.</summary>
/// <param name="Input">Input port index.</param>
/// <param name="Item">Item index within that input.</param>
public sealed record PairedItem(int Input, int Item);

/// <summary>A per-item error.</summary>
public sealed record ItemError
{
    /// <summary>The node that failed.</summary>
    public string? Node { get; init; }

    /// <summary>What went wrong.</summary>
    public string Message { get; init; } = "";

    /// <summary>Further detail.</summary>
    public string? Description { get; init; }

    /// <summary>HTTP status, when the failure came from an HTTP call.</summary>
    public int? HttpStatus { get; init; }
}

/// <summary>A decrypted credential for one call. Never log or persist it.</summary>
public sealed class Credential
{
    /// <summary>Credential type, such as <c>acme.apiKey</c>.</summary>
    public string Type { get; set; } = "";

    /// <summary>Credential id.</summary>
    public string Id { get; set; } = "";

    /// <summary>Credential version.</summary>
    public long Version { get; set; }

    /// <summary>The secret fields.</summary>
    public JsonObject Data { get; set; } = new();

    /// <inheritdoc />
    public override string ToString() => $"Credential({Type}, {Id})";
}

/// <summary>Context of the workflow execution that triggered a call.</summary>
public sealed record ExecutionInfo
{
    /// <summary>Execution id.</summary>
    public string ExecutionId { get; init; } = "";

    /// <summary>Workflow id.</summary>
    public string WorkflowId { get; init; } = "";

    /// <summary>Workflow name.</summary>
    public string WorkflowName { get; init; } = "";

    /// <summary>Execution mode, such as <c>manual</c>.</summary>
    public string Mode { get; init; } = "";

    /// <summary>Name of the node being run.</summary>
    public string NodeName { get; init; } = "";

    /// <summary>Run index of the node.</summary>
    public int RunIndex { get; init; }

    /// <summary>Retry attempt, starting at 1.</summary>
    public int Attempt { get; init; }

    /// <summary>Sub-workflow depth.</summary>
    public int Depth { get; init; }
}

/// <summary>Names a node type and version.</summary>
public sealed record NodeRef(string Type, int Version);

/// <summary>Params of an <c>execute</c> request.</summary>
public sealed class ExecuteParams
{
    /// <summary>Host-chosen id that callbacks carry.</summary>
    public string CallId { get; set; } = "";

    /// <summary>The node to run.</summary>
    public NodeRef Node { get; set; } = new("", 0);

    /// <summary>One item list per main input port.</summary>
    public List<List<Item>> Inputs { get; set; } = new();

    /// <summary>Resolved properties per item of input 0; at least one entry.</summary>
    public List<JsonObject> Params { get; set; } = new();

    /// <summary>Per item, the messages of properties that failed to resolve.</summary>
    public List<Dictionary<string, string>>? ParamErrors { get; set; }

    /// <summary>The declared, bound credentials.</summary>
    public Dictionary<string, Credential>? Credentials { get; set; }

    /// <summary>Declared credentials that could not be loaded, with the reason.</summary>
    public Dictionary<string, string>? CredentialErrors { get; set; }

    /// <summary>The execution this call belongs to.</summary>
    public ExecutionInfo Execution { get; set; } = new();
}

/// <summary>Asks the execution to pause.</summary>
public sealed record Wait
{
    /// <summary>Resume at this time.</summary>
    public DateTimeOffset? Until { get; init; }

    /// <summary>Resume when this token is presented.</summary>
    public string? ResumeToken { get; init; }
}

/// <summary>The wire form of a node's output.</summary>
public sealed record ExecuteResult
{
    /// <summary>One item list per output port.</summary>
    public List<List<Item>> Outputs { get; init; } = new();

    /// <summary>Optional pause request.</summary>
    public Wait? Wait { get; init; }
}

/// <summary>Host or plugin identity.</summary>
public sealed record PluginInfo(string Name, string Version);

/// <summary>Params of the <c>initialize</c> request.</summary>
public sealed record InitializeParams(int ProtocolVersion, PluginInfo? Host);

/// <summary>Result of the <c>initialize</c> request.</summary>
public sealed record InitializeResult(int ProtocolVersion, PluginInfo Plugin, IReadOnlyList<NodeDescriptor> Nodes);
