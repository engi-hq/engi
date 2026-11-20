using System.Text.RegularExpressions;

namespace Engi.Plugin;

/// <summary>Checks a descriptor against the v1 rules (docs/15 section 15.4), like the host does at load.</summary>
public static partial class DescriptorValidator
{
    private const string ReservedPrefix = "engi.";

    [GeneratedRegex(@"^[a-zA-Z][a-zA-Z0-9_-]*(\.[a-zA-Z][a-zA-Z0-9_-]*)+$")]
    private static partial Regex TypePattern();

    /// <summary>Returns every problem with <paramref name="d"/>; empty when it is valid.</summary>
    public static IReadOnlyList<string> Check(NodeDescriptor d)
    {
        var errs = new List<string>();
        if (!TypePattern().IsMatch(d.Type))
            errs.Add($"node type \"{d.Type}\": use dot-separated names such as acme.text.reverse");
        if (d.Type.StartsWith(ReservedPrefix, StringComparison.Ordinal))
            errs.Add($"node type \"{d.Type}\": the \"{ReservedPrefix}\" prefix is reserved for built-in nodes");
        if (d.Version < 1) errs.Add("version must be at least 1");
        if (d.Group is not (NodeGroup.Action or NodeGroup.Transform or NodeGroup.Flow))
            errs.Add($"group \"{d.Group}\": plugins may declare action, transform and flow nodes only");
        if (d.MergePolicy is not (null or "" or "waitAll" or "any" or "partialOnFlush"))
            errs.Add($"unknown merge policy \"{d.MergePolicy}\"");
        foreach (var p in (d.Inputs ?? []).Concat(d.Outputs ?? []))
        {
            if (p.Kind != "main") errs.Add($"port \"{p.Name}\": plugins may only use main ports, not \"{p.Kind}\"");
        }
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var i = 0;
        foreach (var p in d.Properties ?? [])
        {
            if (p.Name.Length == 0) errs.Add($"properties[{i}]: name must not be empty");
            else if (!seen.Add(p.Name)) errs.Add($"properties[{i}]: duplicate name \"{p.Name}\"");
            i++;
        }
        return errs;
    }
}
