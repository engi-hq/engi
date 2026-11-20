namespace Engi.Plugin;

/// <summary>Severity of a plugin log line.</summary>
public enum LogLevel
{
    /// <summary>Debug.</summary>
    Debug,
    /// <summary>Information.</summary>
    Info,
    /// <summary>Warning.</summary>
    Warn,
    /// <summary>Error.</summary>
    Error,
}

/// <summary>Sends structured log lines to the host (a <c>log</c> notification). Ignored over HTTP.</summary>
public sealed class PluginLogger
{
    private readonly Action<LogLevel, string, IReadOnlyDictionary<string, object?>?> _sink;

    internal PluginLogger(Action<LogLevel, string, IReadOnlyDictionary<string, object?>?> sink) => _sink = sink;

    /// <summary>Logs a line. Never log credentials.</summary>
    public void Log(LogLevel level, string message, IReadOnlyDictionary<string, object?>? attrs = null) =>
        _sink(level, message, attrs);

    /// <summary>Logs at debug level.</summary>
    public void Debug(string message, IReadOnlyDictionary<string, object?>? attrs = null) => Log(LogLevel.Debug, message, attrs);

    /// <summary>Logs at info level.</summary>
    public void Info(string message, IReadOnlyDictionary<string, object?>? attrs = null) => Log(LogLevel.Info, message, attrs);

    /// <summary>Logs at warn level.</summary>
    public void Warn(string message, IReadOnlyDictionary<string, object?>? attrs = null) => Log(LogLevel.Warn, message, attrs);

    /// <summary>Logs at error level.</summary>
    public void Error(string message, IReadOnlyDictionary<string, object?>? attrs = null) => Log(LogLevel.Error, message, attrs);
}
