# Independent review and implementation decisions

The read-only reviewer found six Important issues; all received fixes before publication. Headless Node tests reproduced the metadata/keyframe and stale JPEG races, then passed after correction. A failing Go test preceded the recovery-state parser. Native Swift compilation and non-capturing lease/mailbox tests subsequently passed on Apple Silicon macOS 15.5. Capture/input behavior still requires explicit hardware testing.

1. Input backlog: one scheduled native drain, 32 pending messages, urgent release/EOF flushing, receipt-time leases; overflow fails closed.
2. Scope pause: a revoked control generation cannot be renewed by ordinary heartbeats. Windows increments generation only after a new Control click.
3. Helper failure: graceful fatal shutdown releases held input; native reports held state before posting, and the surviving Go supervisor invokes a validated release-only helper on termination. Entire process-tree/OS hard failure remains a documented limit.
4. Decoder ordering: unchanged metadata is deduplicated; H.264 key packets configure directly from their own SPS, independent of stderr/stdout delivery order.
5. Excluded app UI: focused AX window geometry, owner z-order across layers and pointer hit target are checked; excluded/occluded targets pause control. These conservative checks need actual Mac-app testing.
6. Stale images: render generation invalidates async JPEG/decoder completion, and queued WPF frames require a still-ready current session.

Decisions within the authorized scope:
- TLS/WebSockets avoids additional protocol stacks; TCP loss can stall video, so this does not claim UDP/WebRTC latency behavior.
- Hardware H.264 is implemented with JPEG only as an explicit diagnostic choice. Unsupported Mac hardware encoding fails rather than silently switching to costly CPU encoding.
- App Priority is one independently captured window; excluded child UI is surfaced. Resizing that Mac window stops/reconnects rather than risking wrong input coordinates.
- CI remains manual until no-cost allowance is confirmed. The first Mac asset was a build kit; alpha.2 adds native compiled binaries with non-capturing tests, without claiming capture/control validation.
- Physical desktop visual QA is pending because desktop interaction/capture was not authorized for development.

## Native review, 2026-10-23

- Kept capture behind existing Screen Recording preflight, authenticated TLS pairing and explicit ALLOW. Accessibility is checked without requesting it; leases and scope checks gate input.
- Reviewed bounded capture/transport/input queues, explicit control generations, urgent release, EOF/watchdog shutdown and supervisor held-input recovery. No OS input was posted during review or tests.
- Corrected VideoToolbox copied-property ownership using a retained CF object, used the main-queue watchdog scheduling explicitly, and removed a deprecated activation option.
- Corrected consent to display H.264 bitrate or JPEG quality instead of always claiming JPEG.
- Full process-tree termination/OS failure, real AX/window coordination and actual VideoToolbox encode behavior remain unverified. Compile success does not establish runtime safety or latency.
