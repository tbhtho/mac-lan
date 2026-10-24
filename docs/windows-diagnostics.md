# Windows disconnect diagnostics

Alpha.34 adds detailed local logging. Earlier downloads have only the earlier
basic Windows event log. The new Windows logger records the native status messages
already sent by the paired Mac, so it also works with an older Mac host.

Logs start automatically in a normal app session. On the Windows Connection page,
expand **Display and diagnostics** and choose **Open logs folder**. You can also
paste `%LOCALAPPDATA%\MAC-DOW\diagnostics` into File Explorer.

After a black screen or “Mac disconnected,” copy `windows-local.jsonl` and any
`windows-local.jsonl.1`, `.2`, and `.3` files before extended further use. Each
file is limited to 2 MiB, with four files retained (8 MiB total for the new
logger). `.1` is the most recent older segment. A legacy `.previous` file, if
present from an older build, is preserved separately. Files contain one JSON
record per line. UTC timestamps can be matched with Mac diagnostic timestamps;
`run` identifies a Windows process and `session` identifies a connection.

## What is recorded

- App version, build module ID, Windows/.NET/WebView2 versions and architecture.
- Connection and stream phases, chosen codec/dimensions/rate, and native control
  permission/watchdog states received on the authenticated LAN.
- Transport health every five seconds: packet/byte/message totals, outgoing queue
  depth, dropped input, last video/native heartbeat/pong age, RTT, lease timing, and
  queued video/audio work awaiting the UI.
- Process working set/private memory, cumulative CPU time, managed memory and GC
  counts, and thread-pool backlog every five seconds, to help investigate resource
  pressure alongside heartbeat delays.
- UI health every five seconds: focus, control generation, video freshness and
  last presentation age. UI tick gaps of at least 750 ms are recorded on return;
  an independent background sampler also reports stalls of at least one second
  when a session is connected. A stall shorter than the sampling interval may
  only appear as a measured gap on return.
- Lease state transitions, send gaps of at least 750 ms, sends taking at least
  250 ms, the one-second send deadline, close status, and categorized native
  failures. `session-ended` is written on the connection worker, before the UI
  handles the disconnect, preserving evidence if the UI is delayed.
- Network read/write continuations do not depend on the Windows dispatcher;
  a synthetic test holds the caller's UI context completely still and verifies
  status receipt, stall/resource logging and disconnect reporting. This removes
  a UI dependency without extending the native watchdog deadlines.
- Numeric decoder frame rate, encode/decode timing and backlog, keyframe recovery
  reasons, missing WebView acknowledgments, and WebView process/navigation errors.
- Exception types, HRESULT/socket/WebSocket codes, and method names for failure
  call sites. Exception text, source paths and arbitrary Mac status/close text are
  omitted; recognized native messages become fixed categories. Unknown Mac text
  appears as `host-message`, so some native failures still need Mac-side evidence.

`phase` values are 0 new, 1 authenticating, 2 paired, 3 starting, 4 ready and
5 stopped. A `session-ended` record snapshots the phase immediately before
stopping. Null ages mean that event has not happened yet. Queue sizes are
approximate samples, not synchronized performance measurements.

Disk writes use a bounded background queue. Each written record is closed to
avoid keeping evidence in an application buffer; this is not a guarantee against
power loss or an immediate process kill. On queue saturation or write failure,
the cumulative `dropped` count increases and is included in later records. A full
disk or inaccessible folder can prevent logging without stopping sharing.

## Reading a failure

Look at the last `session-ended` for the affected connection, then the preceding
records with that `session` ID. `receive-failed` identifies a transport receive
failure; `remote-close` includes a WebSocket close code when supplied by the Mac.
`send-timeout` identifies the local send deadline. `hostError` preserves a known
Mac error category even if the connection subsequently fails.

An increasing UI tick age alongside timely incoming native heartbeats suggests
the Windows UI was delayed. An increasing native heartbeat age with a responsive
UI points toward the Mac/helper or transport. Decoder backlog/recovery alongside
fresh incoming frames identifies a different boundary from missing packets.
These are investigation clues, not proof of a specific cause.

The alpha.32 report of `lease-expired` / `generation-revoked` with permission
still true confirms that control was revoked by the watchdog. It does not by
itself explain why heartbeats were late or why the stream ended. The watchdog
and deliberate click-to-control requirements remain unchanged.

Alpha.34 treats input pressure as a control event, not a disconnect. A
Mac `capture-failed` record with code `input-overflow` in alpha.32 meant the
native helper exited after its bounded input mailbox overflowed. Now pointer
moves are coalesced per animation frame, excess input is dropped rather than
closing the session, and native pressure releases held keys and reports the
`input-overflow` control state; video and audio keep running until the next
click re-arms control. Windows records the local half as `input-dropped` with a
cumulative `dropped` count. These records show that input was shed, not that the
transport failed.

Windows logs stay on the PC and are not uploaded. They do not record keystrokes,
pointer coordinates, clipboard text, screen/cursor/audio contents, pairing
numbers/tokens, Mac addresses/fingerprints, window/document titles, or transfer
names/contents. Sharing a log is a separate user action.

## Validation

`./scripts/test.ps1` includes isolated log persistence/rotation/error/privacy
checks, synthetic loopback TLS disconnect regressions, and decoder recovery
tests. These tests do not establish the cause of the user's Fluxframe failure or
prove two-computer behavior. The user must reproduce the failure with alpha.34
to collect that evidence.
