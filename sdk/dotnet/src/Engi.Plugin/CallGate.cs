namespace Engi.Plugin;

/// <summary>Caps the execute calls running at once.</summary>
internal sealed class CallGate(int limit)
{
    private int _running;

    public bool TryEnter()
    {
        if (Interlocked.Increment(ref _running) <= limit) return true;
        Interlocked.Decrement(ref _running);
        return false;
    }

    public void Exit() => Interlocked.Decrement(ref _running);
}
