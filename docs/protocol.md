# Local session protocol v0.1

TLS 1.3 on the Mac server, RFC 6455 WebSocket at /session. Windows validates the exact SHA-256 DER certificate fingerprint through ClientWebSocket's supported native TLS validation callback. It refuses DNS names/public addresses. Self-signed identity and 256-bit random token exist only in host memory for that launch. No browser certificate warning bypass, trust-store modification, saved token or permanent pairing.

One controller; private IPv4 interface/peers only; no Origin header allowed; JSON messages <=4096 bytes; binary frames <=4 MiB; 5 s authentication deadline. Native capture is a child process with no listening socket. File transfers use a separate authenticated HTTPS request, described in [file-transfer.md](file-transfer.md); file content never enters the video/input WebSocket. No remote executable commands or clipboard payloads.

1. Client text: {"type":"auth","token":"64 hex digits"}.
2. Host: {"type":"catalog","maxFps":60,"windows":[{"id":42,"app":"Editor","title":"document"}]}. New hosts also provide an ephemeral `transferToken` after pairing. When native video setup is unavailable, `maxFps` is 0, `videoError` explains the problem, and pairing/file transfer remain available. This never substitutes a lower frame rate.
3. Client: {"type":"start","window":0,"width":1920,"fps":60,"quality":70,"codec":"h264","bitrate":8}. Window 0 explicitly requests main Mac desktop; nonzero must be in catalog. Width 640..1920, fps exactly 60, JPEG quality 35..85, H264 bitrate 2..20 Mbit/s. Full desktop output uses a 16:9 stream (1920×1080 by default) stretched from the complete Mac desktop. The actual Mac display mode is unchanged. Older hosts that do not advertise 60 fps cannot start video in the new Windows client.
4. Mac terminal displays scope and settings. Host waits for local ALLOW. Then {"type":"ready"} and native helper starts. Any missing Screen Recording access fails without prompting.
5. Video metadata: {"type":"video","codec":"avc1.42...","width":1920,"height":1080,"fps":60,"presentation":"fill"}. Desktop `fill` scales the picture to the complete available viewport; App Priority uses `fit` with aspect-preserving letterboxes. Pointer mapping uses the same mode. Native capture omits its cursor; Windows shows its local pointer. H264 keyframes also carry SPS/PPS, allowing independent configuration/recovery.
6. Binary video envelope: 1 byte kind (1 JPEG, 2 H264 key, 3 H264 delta), u32 BE sequence, u32 BE capture-callback-to-encode-output microseconds, then JPEG bytes or Annex B H264 access unit. Bounded queues preserve encoded H264 dependencies in order, instead of replacing arbitrary delta frames. Queue limits are 64 frames, 8 MiB and 250 ms; pressure drops a broken chain until a keyframe can restart it. JPEG keeps only the latest image. The Windows-to-WebView bridge sends bounded batches with stream epochs and acknowledgment IDs; late acknowledgments or presentations from an old stream cannot revive it. Sequence gaps still discard dependent H264 frames until IDR (nominal interval <=0.5 s). Only already decoded pictures may be coalesced for presentation.
7. Client lease every 250 ms: {"type":"lease","active":false,"view":true,"stamp":123}. Stamp is echoed in {"type":"pong","stamp":123} for measured RTT. It is not synchronized capture-to-display latency. Native control expires after 850 ms with no active lease; capture stops after 15 s without a viewer message, tolerating brief Wi-Fi or driver stalls. View false suppresses encoding. No automatic input rearming.
8. Input: key {code: Mac virtual key, down:bool}, move {x:0..1,y:0..1}, button {button:0 left/1 middle/2 right,down:bool}, scroll {delta:-2400..2400}, release. All include type. Input is ignored without a current active lease, Accessibility permission and applicable scope checks. Release is idempotent. A release can carry an optional fixed `reason` such as `video-stale`, `native-heartbeat-stale`, `foreground-lost` or `viewer-blur`. Only the finite allowlist is accepted, and only on release messages. The Mac records it as a `control-release` diagnostic without input contents or arbitrary client text. Legacy empty reasons and repeated unarmed recovery releases produce no release diagnostic.
9. Status/error messages use {"type":"status","message":"...","paused":true}. Paused tells Windows to revoke input and clear pixels. Disconnect also clears viewer and releases native held input by EOF/watchdog.
10. Native control status uses {"type":"controlStatus","code":"view-only","controlPermission":true,"active":false}. Codes are fixed labels for view-only, active control, missing Accessibility and revoked/expired/stale/scope-blocked leases. Permission checks continue on inactive leases, so a deliberate grant can restore the click-to-control action. Pointer moves are absolute positions and may be coalesced by the viewer, the host or the native mailbox; input pressure releases held keys and reports `input-overflow` instead of ending capture. Only the code and boolean permission/control counts enter Mac diagnostics. The first left click in live remote pixels requests local arming and consumes the gesture; no pointer/key data is routed before arming, fresh video and focus checks. Losing focus/cancelling the activation gesture releases control.

TLS/WebSocket uses reliable ordered TCP. Loss can stall video behind retransmission; no claim of WebRTC/UDP behavior. Write deadlines, encoder gating, newest-frame queues and sequence recovery prevent unbounded app queues, but OS socket buffers still exist. Input travels in the opposite TCP direction; future WebRTC/DTLS-SRTP would improve behavior under loss after measured baseline. No STUN/TURN/cloud relay exists.

## Alpha.22 additions

- Approving a pairing request on the Mac also approves viewing for that Windows
  address until sharing stops; a separate stream prompt appears only for a peer
  that connected with a saved token and has not been approved in this session.
- The catalog carries `"keyframes": true`. Windows may then send
  `{"type":"keyframe"}` (rate-limited to one per 250 ms) when its decoder resets;
  the capture helper forces the next encoded frame to be a keyframe. Periodic
  keyframes are a 2-second backup. Windows never sends this to hosts without the
  flag, because older hosts end the session on unknown input.

## Alpha.27 additions

- `GET`/`PUT /clipboard` on the session listener share plain UTF-8 text (at most
  256 KiB) with the paired Windows PC. Requests need the current pairing's
  transfer bearer token from the paired peer, exactly like `/transfer`; the
  token is revoked when the session ends. The Mac reads and writes text with
  `pbpaste`/`pbcopy`. Windows pushes on `WM_CLIPBOARDUPDATE`, polls the Mac once
  a second, and remembers the last synced text so nothing echoes back. The Mac
  clipboard present at connection time is not copied to Windows.

## Alpha.30 additions

- A start request may carry `"audio": true`. The host then passes `audio` to the
  capture helper, which adds ScreenCaptureKit audio (48 kHz stereo, excluding its
  own output) and mutes the default output device until it exits, restoring the
  previous state on normal exit and SIGTERM.
- Audio travels as stream packet kind 4: `[4][sequence u32][0 u32][16-bit
  little-endian interleaved stereo PCM]`, with its own sequence so video gap
  detection never sees it. The host keeps up to 24 audio packets in a separate
  queue that drops the oldest when full, writes status lines first, then audio,
  then video. Windows does not count audio as fresh video and plays it through
  Web Audio about 50 ms ahead.
- Windows clipboard access runs on a short-lived STA thread with a 3-second
  limit, so a slow clipboard owner cannot stall the UI thread and its lease.

## Alpha.43 additions

- After authentication the host sends WebSocket pings every 10 seconds and
  closes a connection that has answered none for 75 seconds (longer than the
  45-second viewing prompt, during which the connection is not read).
- A catalog sent to a peer approved in this sharing session may carry
  `"resumeToken"`: 64 lowercase hex characters from `crypto/rand`, bound to the
  peer's IP address, valid for 30 minutes and only for this host process (its
  certificate changes on the next launch). Windows keeps it in memory only.
- `{"type":"resume","token":...}` is accepted as the first message in place of
  `pair`. The host compares it in constant time, requires the same peer address
  and an existing approval, and consumes it; the resumed catalog carries a new
  token. No pairing prompt is shown, and viewing stays approved as before.
  Older hosts never send a token, so Windows falls back to guided pairing.
- When the session slot is held by a connection from the same peer address,
  a new connection closes it (waiting up to 3 seconds) instead of receiving
  409. Other peers still receive 409.
- Windows resumes after network drops, a silent Mac, closing App Priority, or
  switching between the desktop and App Priority, with backoff of 0.5, 1, 2,
  4, 8 and 15 seconds. A user disconnect, quitting, or a second Mac capture
  error within a minute does not resume.

## Alpha.45 additions

- The Mac keeps one TLS identity across launches in the per-user application
  support folder (`MAC-DOW/identity.json`, owner-only, links refused). If it is
  missing or damaged a new identity is created; remembered PCs then need one new
  approval. If the folder is unavailable, a per-launch identity is used as before.
- A `pair` message may carry `"name"` (the PC name, shown on the Mac; at most 64
  printable characters kept). After the user approves a guided pairing, the
  catalog carries `"trustedDevice": {"id": <32 hex>, "token": <64 hex>}`. The Mac
  stores only the SHA-256 of the token (`MAC-DOW/trusted-pcs.json`, owner-only,
  up to 16 PCs, least recently used dropped).
- `{"type":"trusted","device":...,"token":...}` is accepted as the first message.
  The host compares the hash in constant time, keeps the private-LAN peer check,
  treats the PC as approved for viewing, and never issues another credential for
  it. It is not bound to an address, so a PC on DHCP keeps working. It may also
  replace the same address's stale connection, like `resume`. An unknown or wrong
  credential receives `{"type":"error","code":"unknown-device"}` before the close.
  Only clients that send a `name` with `pair` receive a credential.
- Windows keeps the credential DPAPI-encrypted for the current user in
  `%LOCALAPPDATA%\MAC-DOW\trusted-macs.json`, connects automatically when a
  remembered fingerprint is discovered (not after a user Disconnect in the same
  run, and waiting 2, 4, 8 ... up to 30 minutes after each time it gives up), and
  forgets the entry only on `unknown-device`. **Forget Remembered PCs** on the
  Mac revokes every saved PC, this run's approvals and reconnect tokens, and
  disconnects the current PC; **Forget remembered Macs** on Windows removes its
  saved entries.
- Automatic updates on both sides now wait only while video or control runs,
  during transfers, or while a connection attempt is in progress. An idle
  remembered connection (files and clipboard) no longer holds them back; it
  reconnects by itself afterwards.
