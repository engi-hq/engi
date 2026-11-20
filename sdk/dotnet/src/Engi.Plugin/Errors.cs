using System.Text.Json.Nodes;

namespace Engi.Plugin;

/// <summary>JSON-RPC error codes used by the protocol.</summary>
public static class ErrorCodes
{
    /// <summary>Invalid JSON.</summary>
    public const int ParseError = -32700;
    /// <summary>Not a valid request object.</summary>
    public const int InvalidRequest = -32600;
    /// <summary>Unknown method.</summary>
    public const int MethodNotFound = -32601;
    /// <summary>Invalid params.</summary>
    public const int InvalidParams = -32602;
    /// <summary>Internal error.</summary>
    public const int InternalError = -32603;
    /// <summary>A node failure shown to the user.</summary>
    public const int NodeError = -32001;
    /// <summary>The request was cancelled.</summary>
    public const int Cancelled = -32002;
    /// <summary>Method or transport not supported.</summary>
    public const int Unsupported = -32003;
    /// <summary>Above the concurrency cap.</summary>
    public const int Busy = -32004;
}

/// <summary>A JSON-RPC error, sent or received.</summary>
public class RpcException : Exception
{
    /// <summary>Creates the error.</summary>
    public RpcException(int code, string message, JsonNode? data = null) : base(message)
    {
        Code = code;
        Data = data;
    }

    /// <summary>The JSON-RPC error code.</summary>
    public int Code { get; }

    /// <summary>Optional error data.</summary>
    public new JsonNode? Data { get; }
}

/// <summary>A callback the transport does not offer, such as blob access over HTTP.</summary>
public sealed class UnsupportedException : RpcException
{
    /// <summary>Creates the error for <paramref name="what"/>.</summary>
    public UnsupportedException(string what)
        : base(ErrorCodes.Unsupported, what + " is not supported over this transport") { }
}

/// <summary>
/// A failure shown to the user like a built-in node's error (code -32001).
/// </summary>
public class NodeException : Exception
{
    /// <summary>Creates the error.</summary>
    public NodeException(string message, string? description = null, int? httpStatus = null, int? itemIndex = null)
        : base(message)
    {
        Description = description;
        HttpStatus = httpStatus;
        ItemIndex = itemIndex;
    }

    /// <summary>Further detail.</summary>
    public string? Description { get; }

    /// <summary>HTTP status of the failed call, if any.</summary>
    public int? HttpStatus { get; }

    /// <summary>Index of the item that failed, if known.</summary>
    public int? ItemIndex { get; }
}

/// <summary>A parameter the host could not resolve; the message is the host's.</summary>
public sealed class ParamException : NodeException
{
    /// <summary>Creates the error.</summary>
    public ParamException(string name, string message, int itemIndex)
        : base(message, null, null, itemIndex) => ParamName = name;

    /// <summary>The parameter that failed.</summary>
    public string ParamName { get; }
}

/// <summary>A message exceeded <see cref="PluginOptions.MaxMessageBytes"/>; the connection is closed.</summary>
public sealed class MessageTooLargeException : IOException
{
    /// <summary>Creates the error.</summary>
    public MessageTooLargeException(int limit) : base($"plugin message too large (limit {limit} bytes)") { }
}
