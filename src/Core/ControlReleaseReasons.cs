namespace MacDow.Core;

public static class ControlReleaseReasons
{
    static readonly HashSet<string> values = new(StringComparer.Ordinal)
    {
        "foreground-lost", "window-deactivated", "viewer-blur", "pointer-cancel",
        "video-stale", "native-heartbeat-stale", "tab-change", "monitor-change",
        "manual", "disconnected", "mac-permission", "mac-scope", "mac-watchdog",
        "shutdown", "files-open", "updates-open", "keyboard-unavailable",
        "stream-start", "display-change", "mac-paused"
    };
    public static IReadOnlyCollection<string> All { get; } = Array.AsReadOnly(values.ToArray());
    public static bool IsKnown(string? value) => value != null && values.Contains(value);
    public static string Normalize(string? value) => IsKnown(value) ? value! : "manual";
}
