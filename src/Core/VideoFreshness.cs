namespace MacDow.Core;

public static class VideoFreshness
{
    // Presentation and receive stamps use the same monotonic Windows clock.
    // A static desktop needs a live native heartbeat, not redundant new pixels.
    public static string? BlockReason(bool? controlPermission, long presented, long nativeAlive,
        long received, long now, long frequency)
    {
        if (controlPermission == false) return "mac-permission";
        if (presented <= 0 || frequency <= 0) return "video-stale";
        if (nativeAlive <= 0 || (now - nativeAlive) / (double)frequency >= 1.5)
            return "native-heartbeat-stale";
        if (received > presented && (now - presented) / (double)frequency >= 1)
            return "video-stale";
        return null;
    }
}
