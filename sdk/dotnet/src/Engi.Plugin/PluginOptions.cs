namespace Engi.Plugin;

/// <summary>Limits and settings of a served plugin. Defaults mirror the host's.</summary>
public sealed class PluginOptions
{
    /// <summary>Default for <see cref="MaxMessageBytes"/>: 32 MiB, the host default.</summary>
    public const int DefaultMaxMessageBytes = 32 << 20;

    /// <summary>Default for <see cref="MaxConcurrentCalls"/>: 16, the host default.</summary>
    public const int DefaultMaxConcurrentCalls = 16;

    /// <summary>Largest message in either direction. A larger incoming message closes the connection.</summary>
    public int MaxMessageBytes { get; set; } = DefaultMaxMessageBytes;

    /// <summary>Execute calls run at once; above it a call is answered with -32004.</summary>
    public int MaxConcurrentCalls { get; set; } = DefaultMaxConcurrentCalls;

    /// <summary>When set, HTTP requests must carry this bearer token.</summary>
    public string? BearerToken { get; set; }

    internal void Validate()
    {
        if (MaxMessageBytes < 1) throw new ArgumentOutOfRangeException(nameof(MaxMessageBytes), "must be positive");
        if (MaxConcurrentCalls < 1) throw new ArgumentOutOfRangeException(nameof(MaxConcurrentCalls), "must be positive");
    }
}
