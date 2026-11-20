using System.Text.Json;
using System.Text.Json.Nodes;

namespace Engi.Plugin;

/// <summary>Builds and maps JSON-RPC 2.0 messages.</summary>
internal static class RpcMessages
{
    public static JsonObject Result(JsonNode? id, JsonNode? result) => new()
    {
        ["jsonrpc"] = "2.0",
        ["id"] = id?.DeepClone(),
        ["result"] = result ?? new JsonObject(),
    };

    public static JsonObject Error(JsonNode? id, int code, string message, JsonNode? data = null)
    {
        var error = new JsonObject { ["code"] = code, ["message"] = message };
        if (data is not null) error["data"] = data;
        return new JsonObject { ["jsonrpc"] = "2.0", ["id"] = id?.DeepClone(), ["error"] = error };
    }

    public static JsonObject Request(long id, string method, JsonNode? parameters) => new()
    {
        ["jsonrpc"] = "2.0",
        ["id"] = id,
        ["method"] = method,
        ["params"] = parameters,
    };

    public static JsonObject Notification(string method, JsonNode? parameters) => new()
    {
        ["jsonrpc"] = "2.0",
        ["method"] = method,
        ["params"] = parameters,
    };

    /// <summary>Maps any exception a handler can throw to a wire error.</summary>
    public static JsonObject ErrorFrom(JsonNode? id, Exception ex)
    {
        switch (ex)
        {
            case RpcException rpc:
                return Error(id, rpc.Code, rpc.Message, rpc.Data);
            case NodeException node:
                var data = new JsonObject();
                if (node.Description is not null) data["description"] = node.Description;
                if (node.HttpStatus is not null) data["httpStatus"] = node.HttpStatus;
                if (node.ItemIndex is not null) data["itemIndex"] = node.ItemIndex;
                return Error(id, ErrorCodes.NodeError, node.Message, data.Count > 0 ? data : null);
            case OperationCanceledException:
                return Error(id, ErrorCodes.Cancelled, "cancelled");
            default:
                return Error(id, ErrorCodes.InternalError, ex.Message);
        }
    }

    /// <summary>Key under which a request id is tracked; numbers and strings never collide.</summary>
    public static string Key(JsonNode id) => id.ToJsonString();

    public static JsonNode? ToNode<T>(T value) => JsonSerializer.SerializeToNode(value, WireJson.Options);
}
