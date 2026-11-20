using System.Text.Json;
using System.Text.Json.Serialization;

namespace Engi.Plugin;

/// <summary>The serializer settings every wire shape uses: camelCase names, null members omitted.</summary>
public static class WireJson
{
    /// <summary>Shared options. Do not mutate.</summary>
    public static JsonSerializerOptions Options { get; } = Create();

    private static JsonSerializerOptions Create()
    {
        var options = new JsonSerializerOptions(JsonSerializerDefaults.Web)
        {
            DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
            PropertyNameCaseInsensitive = true,
        };
        options.Converters.Add(new JsonStringEnumConverter(JsonNamingPolicy.CamelCase));
        options.MakeReadOnly(populateMissingResolver: true);
        return options;
    }
}
