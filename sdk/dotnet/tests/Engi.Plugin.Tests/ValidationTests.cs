using Engi.Plugin;

namespace Engi.Plugin.Tests;

public class ValidationTests
{
    private static NodeDescriptor Valid => TestPlugins.Descriptor();

    public static TheoryData<string, NodeDescriptor> Invalid() => new()
    {
        { "reserved prefix", Valid with { Type = "engi.http.request" } },
        { "not dotted", Valid with { Type = "reverse" } },
        { "trigger group", Valid with { Group = NodeGroup.Trigger } },
        { "ai group", Valid with { Group = NodeGroup.Ai } },
        { "sub port", Valid with { Inputs = [new Port { Name = "model", Kind = "ai.model" }] } },
        { "version zero", Valid with { Version = 0 } },
        { "duplicate property", Valid with { Properties = [new Property { Name = "a" }, new Property { Name = "a" }] } },
        { "empty property name", Valid with { Properties = [new Property()] } },
        { "bad merge policy", Valid with { MergePolicy = "sometimes" } },
    };

    [Fact]
    public void ValidDescriptor_HasNoProblems() => Assert.Empty(DescriptorValidator.Check(Valid));

    [Theory]
    [MemberData(nameof(Invalid))]
    public void InvalidDescriptor_IsReported(string why, NodeDescriptor descriptor)
    {
        Assert.NotEmpty(DescriptorValidator.Check(descriptor));
        Assert.False(string.IsNullOrEmpty(why));
    }

    [Theory]
    [MemberData(nameof(Invalid))]
    public async Task Serve_RefusesInvalidDescriptors(string why, NodeDescriptor descriptor)
    {
        var plugin = TestPlugins.New().AddNode(descriptor, TestPlugins.ReverseHandler);
        var ex = await Assert.ThrowsAsync<InvalidOperationException>(() => plugin.ServeAsync(Stream.Null, Stream.Null));
        Assert.Contains(descriptor.Type, ex.Message);
        Assert.False(string.IsNullOrEmpty(why));
    }

    [Fact]
    public async Task Serve_RefusesAPluginWithoutNodes()
    {
        await Assert.ThrowsAsync<InvalidOperationException>(() => TestPlugins.New().ServeAsync(Stream.Null, Stream.Null));
    }

    [Fact]
    public void AddNode_RejectsDuplicateTypes()
    {
        var plugin = TestPlugins.With(TestPlugins.ReverseHandler);
        Assert.Throws<ArgumentException>(() => plugin.AddNode(Valid, TestPlugins.ReverseHandler));
    }

    [Fact]
    public async Task Serve_RefusesNonPositiveLimits()
    {
        var plugin = TestPlugins.With(TestPlugins.ReverseHandler, new PluginOptions { MaxConcurrentCalls = 0 });
        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(() => plugin.ServeAsync(Stream.Null, Stream.Null));
    }
}
