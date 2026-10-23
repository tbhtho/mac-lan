namespace MacDow.Core;

public sealed class ControlGate
{
    bool connected, remote, focused, armed;
    public event Action<string?>? Released;
    public bool CanRoute => connected && remote && focused && armed;
    public void SetContext(bool connected, bool remote, bool focused, string? reason = null)
    {
        if (this.connected != connected || this.remote != remote || this.focused != focused)
            Disarm(reason ?? (!connected ? "disconnected" : !remote ? "tab-change" : "foreground-lost"));
        this.connected = connected; this.remote = remote; this.focused = focused;
    }
    public bool Arm() { armed = connected && remote && focused; return CanRoute; }
    public void Disarm(string reason = "manual")
    {
        // Keep idempotent recovery releases, but explain only an actual loss of
        // armed ownership. Repeated view-only heartbeats must not spam diagnostics.
        string? actualReason = CanRoute ? ControlReleaseReasons.Normalize(reason) : null;
        armed = false; Released?.Invoke(actualReason);
    }
}
