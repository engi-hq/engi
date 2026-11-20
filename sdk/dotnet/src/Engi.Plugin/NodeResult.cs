namespace Engi.Plugin;

/// <summary>What a node handler returns.</summary>
public sealed class NodeResult
{
    private NodeResult(List<List<Item>> outputs, Wait? wait)
    {
        Outputs = outputs;
        Wait = wait;
    }

    /// <summary>One item list per output port.</summary>
    public IReadOnlyList<IReadOnlyList<Item>> Outputs { get; }

    /// <summary>Optional pause request.</summary>
    public Wait? Wait { get; }

    /// <summary>A result for a node with one output port.</summary>
    public static NodeResult Single(IEnumerable<Item> items) => new([items.ToList()], null);

    /// <summary>A result with one item list per output port.</summary>
    public static NodeResult Ports(params IEnumerable<Item>[] outputs) =>
        new(outputs.Select(o => o.ToList()).ToList(), null);

    /// <summary>Returns a copy of this result that pauses the execution.</summary>
    public NodeResult WithWait(Wait wait) => new(Outputs.Select(o => o.ToList()).ToList(), wait);

    internal ExecuteResult ToWire() => new()
    {
        Outputs = Outputs.Select(o => o.ToList()).ToList(),
        Wait = Wait,
    };
}
