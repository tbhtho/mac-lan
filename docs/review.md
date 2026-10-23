# Independent review and implementation decisions

The read-only reviewer found six Important issues; all received fixes before publication. Headless Node tests reproduced the metadata/keyframe and stale JPEG races, then passed after correction. A failing Go test preceded the recovery-state parser. Native code fixes cannot yet be executable-verified because no Mac compiler is authorized/available.

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
- CI remains manual until no-cost allowance is confirmed. The Mac asset is a build kit, explicitly not a validated binary release.
- Physical desktop visual QA is pending because desktop interaction/capture was not authorized for development.
