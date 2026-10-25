namespace MacDow.Core;

public sealed class ControlGate
{
    bool connected, remote, focused, armed, automatic;
    public event Action<string?>? Released;
    public bool CanRoute => connected && remote && focused && armed;
    public void SetContext(bool connected, bool remote, bool focused, string? reason = null)
    {
        if (this.connected != connected || this.remote != remote || this.focused != focused)
            Release(reason ?? (!connected ? "disconnected" : !remote ? "tab-change" : "foreground-lost"));
        if (!remote || this.connected && !connected) automatic = false;
        this.connected = connected; this.remote = remote; this.focused = focused;
    }
    public bool Arm() { armed = connected && remote && focused; return CanRoute; }
    public void RequestAutomaticControl() { automatic = true; }
    public bool TryAutomaticArm(bool? permission,bool videoReady)
    {
        if (!automatic || permission != true || !videoReady || CanRoute) return false;
        return Arm();
    }
    public void Disarm(string reason = "manual")
    {
        if (reason is "manual" or "tab-change" or "disconnected" or "shutdown" or "keyboard-unavailable") automatic = false;
        Release(reason);
    }
    void Release(string reason)
    {
        // Keep idempotent recovery releases, but explain only an actual loss of
        // armed ownership. Repeated view-only heartbeats must not spam diagnostics.
        string? actualReason = CanRoute ? ControlReleaseReasons.Normalize(reason) : null;
        armed = false; Released?.Invoke(actualReason);
    }
}
