namespace MacDow.Core;

public sealed class ControlGate
{
    bool connected, remote, focused, armed;
    public event Action? Released;
    public bool CanRoute => connected && remote && focused && armed;
    public void SetContext(bool connected, bool remote, bool focused)
    {
        if (this.connected != connected || this.remote != remote || this.focused != focused) Disarm();
        this.connected = connected; this.remote = remote; this.focused = focused;
    }
    public bool Arm() { armed = connected && remote && focused; return CanRoute; }
    public void Disarm() { armed = false; Released?.Invoke(); }
}
