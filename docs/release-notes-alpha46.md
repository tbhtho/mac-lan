# mac-lan 0.1.0-alpha.46

Audio and video now share capture timestamps and one Windows playout clock when both computers run this version. The default 60 ms video buffer can increase with reported audio-device latency, up to 200 ms. This trades a little video responsiveness for alignment; it is not a measured end-to-end latency or sync guarantee.

- Preserve capture times through JPEG, H.264, PCM audio, and static recovery keyframes.
- Stop old scheduled sound on stream resets, overflow, inactive views, and output suspension. Reject duplicate audio and trim or discard late samples instead of overlapping playback.
- Keep missing audio as a timed gap. Discard host audio waiting longer than 120 ms, and prevent bursts from starving video.
- Retain legacy packet compatibility with older endpoints. Update both computers for shared timing. Hover/input behavior is unchanged.

## Validation and limits

Deterministic protocol, queue, media-clock, and audio/video scheduling tests cover synchronization edge cases. Native Mac capture helper compilation and self-tests are checked separately from the cross-compiled ARM64 launcher. Release package identities, source commit, signature, and archive layouts are verified.

Browser DOM/codec tests remain blocked by browser startup crashes/timeouts also seen on the unchanged baseline. No two-computer speaker/display timing measurements or live After Effects preview checks were performed. See the [manual validation checklist](manual-validation.md#alpha46-audiovideo-alignment).

The Mac app retains the existing signing identity. It is not Apple Developer ID signed or notarized. Existing updater compatibility and older-version limits remain described in [update publishing](update-feed-publishing.md).
