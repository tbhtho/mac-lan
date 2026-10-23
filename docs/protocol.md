# Local session protocol v0.1

TLS 1.3 on the Mac server, RFC 6455 WebSocket at /session. Windows validates the exact SHA-256 DER certificate fingerprint through ClientWebSocket's supported native TLS validation callback. It refuses DNS names/public addresses. Self-signed identity and 256-bit random token exist only in host memory for that launch. No browser certificate warning bypass, trust-store modification, saved token or permanent pairing.

One controller; private IPv4 interface/peers only; no Origin header allowed; JSON messages <=4096 bytes; binary frames <=4 MiB; 5 s authentication deadline. Native capture is a child process with no listening socket. No remote executable paths, commands, files or clipboard payloads.

1. Client text: {"type":"auth","token":"64 hex digits"}.
2. Host: {"type":"catalog","windows":[{"id":42,"app":"Editor","title":"document"}]}.
3. Client: {"type":"start","window":0,"width":1600,"fps":30,"quality":70,"codec":"h264","bitrate":8}. Window 0 explicitly requests main Mac desktop; nonzero must be in catalog. Width 640..1920, fps 5..30, JPEG quality 35..85, H264 bitrate 2..20 Mbit/s.
4. Mac terminal displays scope and settings. Host waits for local ALLOW. Then {"type":"ready"} and native helper starts. Any missing Screen Recording access fails without prompting.
5. Video metadata: {"type":"video","codec":"avc1.42...","width":1600,"height":900}. H264 keyframes also carry SPS/PPS, allowing independent configuration/recovery.
6. Binary video envelope: 1 byte kind (1 JPEG, 2 H264 key, 3 H264 delta), u32 BE sequence, u32 BE capture-callback-to-encode-output microseconds, then JPEG bytes or Annex B H264 access unit. At most one waiting Go video frame and one WebView message; sequence gaps discard dependent H264 frames until IDR (nominal interval <=0.5 s).
7. Client lease every 250 ms: {"type":"lease","active":false,"view":true,"stamp":123}. Stamp is echoed in {"type":"pong","stamp":123} for measured RTT. It is not synchronized capture-to-display latency. Native control expires after 850 ms with no active lease; viewer transport expires at 2 s. View false suppresses encoding. No automatic input rearming.
8. Input: key {code: Mac virtual key, down:bool}, move {x:0..1,y:0..1}, button {button:0 left/1 middle/2 right,down:bool}, scroll {delta:-2400..2400}, release. All include type. Input is ignored without a current active lease, Accessibility permission and applicable scope checks. Release is idempotent.
9. Status/error messages use {"type":"status","message":"...","paused":true}. Paused tells Windows to revoke input and clear pixels. Disconnect also clears viewer and releases native held input by EOF/watchdog.

TLS/WebSocket uses reliable ordered TCP. Loss can stall video behind retransmission; no claim of WebRTC/UDP behavior. Write deadlines, encoder gating, newest-frame queues and sequence recovery prevent unbounded app queues, but OS socket buffers still exist. Input travels in the opposite TCP direction; future WebRTC/DTLS-SRTP would improve behavior under loss after measured baseline. No STUN/TURN/cloud relay exists.
