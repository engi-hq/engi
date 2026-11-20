using System.Text.Json.Nodes;

namespace Engi.Plugin;

/// <summary>Where a node appears in the palette. Plugins may use Action, Transform and Flow only.</summary>
public enum NodeGroup
{
    /// <summary>Starts a workflow (not allowed for plugins in v1).</summary>
    Trigger,
    /// <summary>Does something in the outside world.</summary>
    Action,
    /// <summary>Reshapes data.</summary>
    Transform,
    /// <summary>Controls the flow of execution.</summary>
    Flow,
    /// <summary>AI node (not allowed for plugins in v1).</summary>
    Ai,
}

/// <summary>Types a node parameter may have.</summary>
public enum PropertyType
{
    /// <summary>Text.</summary>
    String,
    /// <summary>Number.</summary>
    Number,
    /// <summary>Boolean.</summary>
    Boolean,
    /// <summary>One choice out of <see cref="Property.Options"/>.</summary>
    Options,
    /// <summary>Several choices out of <see cref="Property.Options"/>.</summary>
    MultiOptions,
    /// <summary>Arbitrary JSON.</summary>
    Json,
    /// <summary>Source code.</summary>
    Code,
    /// <summary>Date and time.</summary>
    DateTime,
    /// <summary>A group of optional children.</summary>
    Collection,
    /// <summary>Repeatable groups of children.</summary>
    FixedCollection,
    /// <summary>A resource picked by id, URL or name.</summary>
    ResourceLocator,
    /// <summary>A credential picker.</summary>
    CredentialsSelect,
    /// <summary>A static notice.</summary>
    Notice,
}

/// <summary>An input or output port. v1 plugins only have <c>main</c> ports.</summary>
public sealed record Port
{
    /// <summary>Label of the port.</summary>
    public string Name { get; init; } = "main";

    /// <summary>Port kind; only <c>main</c> is allowed in v1.</summary>
    public string Kind { get; init; } = "main";

    /// <summary>Label shown in the editor.</summary>
    public string? DisplayName { get; init; }

    /// <summary>Whether an input must be connected.</summary>
    public bool? Required { get; init; }

    /// <summary>Creates a main port.</summary>
    public static Port Main(string name = "main") => new() { Name = name };
}

/// <summary>A credential a node can use.</summary>
public sealed record CredentialRef
{
    /// <summary>Key in the call's credentials map.</summary>
    public string Name { get; init; } = "";

    /// <summary>Credential type.</summary>
    public string Type { get; init; } = "";

    /// <summary>Whether the node needs it.</summary>
    public bool? Required { get; init; }
}

/// <summary>One choice of an options property.</summary>
public sealed record Option
{
    /// <summary>Label.</summary>
    public string Name { get; init; } = "";

    /// <summary>Value; any JSON.</summary>
    public JsonNode? Value { get; init; }

    /// <summary>Help text.</summary>
    public string? Description { get; init; }
}

/// <summary>Shows or hides a property depending on other parameter values.</summary>
public sealed record DisplayOptions
{
    /// <summary>Parameter name to the values that show the property.</summary>
    public Dictionary<string, List<JsonNode?>>? Show { get; init; }

    /// <summary>Parameter name to the values that hide the property.</summary>
    public Dictionary<string, List<JsonNode?>>? Hide { get; init; }
}

/// <summary>One entry of a node's parameter schema.</summary>
public sealed record Property
{
    /// <summary>Parameter name; unique within the node.</summary>
    public string Name { get; init; } = "";

    /// <summary>Label shown in the editor.</summary>
    public string? DisplayName { get; init; }

    /// <summary>Parameter type.</summary>
    public PropertyType Type { get; init; } = PropertyType.String;

    /// <summary>Default value; any JSON.</summary>
    public JsonNode? Default { get; init; }

    /// <summary>Whether a value is required.</summary>
    public bool? Required { get; init; }

    /// <summary>Placeholder text.</summary>
    public string? Placeholder { get; init; }

    /// <summary>Help text.</summary>
    public string? Description { get; init; }

    /// <summary>Choices of options and multiOptions properties.</summary>
    public List<Option>? Options { get; init; }

    /// <summary>Nested schema of collection properties.</summary>
    public List<Property>? Children { get; init; }

    /// <summary>Visibility rules.</summary>
    public DisplayOptions? DisplayOptions { get; init; }
}

/// <summary>Static metadata of a node type.</summary>
public sealed record NodeDescriptor
{
    /// <summary>Namespaced type such as <c>acme.text.reverse</c>; must not start with <c>engi.</c>.</summary>
    public string Type { get; init; } = "";

    /// <summary>Node version, at least 1.</summary>
    public int Version { get; init; } = 1;

    /// <summary>Label shown in the editor.</summary>
    public string DisplayName { get; init; } = "";

    /// <summary>Help text.</summary>
    public string? Description { get; init; }

    /// <summary>Palette group.</summary>
    public NodeGroup Group { get; init; } = NodeGroup.Action;

    /// <summary>Key into Engi's icon set.</summary>
    public string? Icon { get; init; }

    /// <summary>Input ports.</summary>
    public IReadOnlyList<Port>? Inputs { get; init; }

    /// <summary>Output ports.</summary>
    public IReadOnlyList<Port>? Outputs { get; init; }

    /// <summary>Parameter schema.</summary>
    public IReadOnlyList<Property>? Properties { get; init; }

    /// <summary>Credentials the node may use.</summary>
    public IReadOnlyList<CredentialRef>? Credentials { get; init; }

    /// <summary>When a node with several inputs may run: waitAll, any or partialOnFlush.</summary>
    public string? MergePolicy { get; init; }
}
