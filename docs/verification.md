# Verification record

## Alpha.15 - proportional desktop and Mac menu-bar app

The full Windows headless suite passed: 49 core, 28 encoded-video, 34 session,
9 update-workflow and 46 transfer checks, presentation, geometry/race, real
headless DOM and real software H.264 tests, all Go package tests, `go vet`, and
Windows Release compilation with zero warnings/errors. New tests cover menu
cache reuse and tamper recovery, safe app migration with preserved source and
neighbors, proportional mapping and noninteractive bars, app apply/rollback,
and public app manifest binding. An actual ARM64 compatibility package was
copied into a validated temporary `.app` without executing native code.
Final clean package checks and public release verification are pending.

The Mac primary package is intended to contain `mac-lan.app`, with a menu-bar
icon for sharing, permission settings, updates, and Quit, without a Dock icon,
main window or Terminal session. Its arm64 Go launcher is cross-compiled on
Windows. First launch builds verified embedded AppKit source with existing
Apple command-line tools, and video setup builds the capture helper. The app
starts discovery, pairing and file service without Screen Recording; video
still requires deliberate permission and separate consent. macOS may require
fresh Screen Recording and Accessibility grants under the app's identity.

Full-desktop native capture now preserves source proportions within a 1920-pixel
width and 2160-pixel height cap without enlargement. A 16:10 source can produce
1920×1200. Windows aspect-fits the complete picture, excludes surrounding bars
from activation/input, and maps the pointer within the displayed picture even
when an old host labels it `fill`. Correcting pixels already stretched by an
old native helper requires the new helper. Sixty fps remains a target.

The intended release includes Windows, the canonical app archive, a byte-identical
legacy app alias, the command-line update bridge, source ZIP, and checksums.
The shared signed feed uses the three asset names recognized by alpha.14.
After its bridge update, the old host's next normal launch installs and opens a
separate `~/Applications/mac-lan.app`, preserving the extracted folder. Settings,
cache paths, private Mac diagnostic configuration and Downloads/MAC-DOW remain
in their existing locations. Public artifacts must contain no private destination
or signing material.

No alpha.15 native Swift compilation, visible menu, permission prompt, capture,
input, Mac update/relaunch or measured LAN performance has been verified here.
The Mac SSH connection was refused, so a native check was unavailable. No
computer-use test, toolchain installation, OS protection bypass or paid CI was
performed. The user's confirmation that alpha.14 works is inherited runtime
evidence for that version, not a native alpha.15 acceptance result.

## Alpha.14 - mac-lan public distribution and signed updates

The public app name is `mac-lan`. Source history remains in the original
repository (ID 1430459188), renamed to `tbhtho/mac-lan`. Public assets use
`mac-lan.exe` and `mac-lan-macos-arm64-commandline-preview.tar.gz`; the Mac
preview retains its `MacDowHost` executable and legacy configuration/cache
paths. The public Mac package must contain no embedded diagnostic destination.
Existing private Mac diagnostic configuration stays local, and Windows
diagnostics remain local-only.

Windows headless checks passed: 49 core, 28 encoded-video, 34 session,
9 update-workflow, and 46 transfer checks; presentation, pointer/decoder race,
real headless DOM, and software H.264 regressions; all Go package tests and
`go vet`. The Windows Release build has zero warnings and errors. A non-UI
package probe loads the renamed assembly's actual embedded theme, viewer,
and updater without starting windows, capture, or input hooks. Updater tests
cover both legacy and renamed install filenames, preserved neighboring files,
and renewal of authenticated expired publisher history while clients still
reject expired or forged feeds.

The signed feed and public binaries share this repository. Alpha.14 is the
one-time manual installation needed on both computers after the old feed was
withdrawn. Future signed updates check at startup and every six hours and offer
restart while idle. Publication verification must bind the public feed and both
downloaded artifacts to the intended source commit, sizes and SHA-256 hashes.

Publication completed and was checked anonymously on 2026-10-23:

- Public repository `tbhtho/mac-lan` retains ID 1430459188. Release
  `v0.1.0-alpha.14`, both package markers, provenance, tag, source ZIP, and signed
  feed bind binary source commit `848768e7a9b13657c0a45190f9045116183c213e`.
  The feed was published in the following commit, `db5561d`.
- All four uploaded assets were downloaded without authentication; byte counts
  and SHA-256 hashes matched the local packages and GitHub asset metadata.
  Windows is 149,552,674 bytes, SHA-256
  `db85e99a2209ce1076b6c577a35c02f0515f9702cb700e31b66ecfd2c0dff0b2`.
  The Mac archive is 3,592,469 bytes, SHA-256
  `d4f53bf427dbce2dbb880486c8ef86ce73606de80c162d4a8acdbd68a0d90659`.
  See the release's `SHA256SUMS-0.1.0-alpha.14.txt` for the source ZIP hash.
- The production signed-feed test passed against public HTTPS: a temporary copy
  of the real alpha.13 Windows EXE was staged and replaced with alpha.14. Its
  source EXE, backup identity, and neighboring sentinel survived; no downloaded
  program was launched. The same production engine downloaded and verified the
  Mac archive, exact layout, source identity, and ARM64 Mach-O header, with no
  Darwin execution.
- The actual single EXE passed isolated package/runtime diagnostics, including
  its embedded theme/viewer/updater, with no visible windows, capture, or hooks.
  Its bundled `--update-check` read the public signed feed and reported alpha.14
  current, without GitHub sign-in.
- Reachable source history and actual public package/source contents were
  checked for the local diagnostic destination and signing-key material, with
  no findings. All twelve older credential-bearing release sets remain private
  drafts; an anonymous request for the alpha.13 Mac bootstrap returned 404.
  No Actions run was triggered for the binary source commit.

The branding and distribution change does not establish new native capture,
input, permission, Mac update-worker/relaunch, or hardware performance evidence.
The Mac remains a cross-compiled command-line preview whose capture helper
builds on first launch using existing Apple tools. A configured 1920×1080/60 fps
target remains distinct from measured throughput and end-to-end latency.
See the alpha.13 record below for the inherited frame-delivery evidence and the
[hardware checklist](manual-validation.md) for the user's two-computer checks.

Earlier entries below are historical records for their named releases. Their
former repository names, manual distribution decisions, withdrawn feeds, and
private bootstrap paths do not describe current public distribution.

## Alpha.13 - preserve encoded frames through temporary viewer delays

The user confirmed working native video and mouse/keyboard control in alpha.12,
with the physical Mac responding ahead of the Windows picture and repeated
returns to click-to-control. Their log retains one video session throughout and
reports native `view-only`, with Accessibility granted. Native expiry and scope
failures have separate codes. This identifies Windows-initiated control releases;
the old log cannot distinguish a local video stall from focus loss.

Reproductions found encoded H.264 frames being replaced at the Go transport,
Windows dispatcher and WebView bridge. Dropping a delta breaks later dependent
frames. The real sequence gate then waits for a keyframe: a simulated 18–40 ms
bridge acknowledgment reduced the old path to about two decoded frames per
second. An independent server test lost the first eleven frames of a twelve-frame
burst. A separate viewer test decoded only two of ten frames during a short
decoder backlog. These are code-level reproductions, not hardware measurements.

The updated path preserves encoded dependencies in bounded queues and bridge
batches. Pressure discards an unusable chain and resumes at a keyframe. Decoded
pictures coalesce at presentation; the canvas dimensions change only when needed.
Epochs, batch identities and a reset acknowledgment prevent delayed work from an
old stream entering a new one. A reset disarms synchronously. Original one-second
presentation and 1.5-second native-heartbeat safety limits remain, with no
automatic input rearming. The Mac now records a finite control-release reason
such as `video-stale` or `viewer-blur`; Windows diagnostic files are not uploaded.

Verified headlessly: 49 core checks, 28 video queue/lifecycle checks, cross-language
delivery of all 600 synthetic frames at a 60 fps input cadence through 18/25/40 ms
bridge delays, 34 TLS session checks, 9 updater workflow checks, 46 TLS transfer
checks, presentation checks, 23 viewer race groups and actual headless Edge DOM
activation. All Go packages and vet pass; Windows Release compilation has zero
warnings/errors. The new real-codec check uses existing FFmpeg to generate twenty
96×64 baseline pictures and real Edge software H.264 decoding. All twenty decode
in order, actual pixels paint, and every frame closes. Holding decoded outputs
across a clear cannot repaint or restore control. The test explicitly overrides
only acceleration preference because its isolated browser disables GPU access.
FFmpeg and codec availability are optional for that additional check.

Current Swift capture source is unchanged from alpha.12. No native Mac capture,
input, compilation, permission prompt, webhook test send, visible UI or paid CI
was run for this release. Delivered 1080p/60 fps, hardware decode, LAN latency and
the user's two-computer experience remain unmeasured. Distribution remains manual
through the original private repository; the withdrawn public feed is not used
for publishing.

## Alpha.12 - signed automatic updates and click activation ordering

The user's alpha.11 diagnostics show Accessibility granted and repeated
control-ready followed immediately by view-only. Activation now commits after
mouse-up and local pointer capture release. Browser-originated activation no
longer refocuses the native WebView2, and repeated status messages do not refocus
the canvas. A real isolated headless Edge DOM regression fails against alpha.11
at the capture-order assertion and passes with this change. The exact native
focus event from the user's session was not logged.

Fresh local checks pass: 33 core assertions, 34 TLS session checks, 9 Windows
update workflow checks, 46 TLS transfer checks, presentation and pointer geometry,
9 viewer race groups, the headless Edge DOM regression, all Go packages and Go
vet. Windows Release compilation has zero warnings/errors. Signed-feed tests
cover tampering, wrong keys, expiry, pinned download locations, interrupted
downloads, stage mutation, preview layout and fresh verification at apply.
Worker tests cover copied executable integrity, waiting and safe replacement;
pending stages are reused only after a fresh preflight. Diagnostic migration
tests use fake destinations and isolated paths. Publisher tests reject private
bootstrap archives and embedded webhook credentials.

Windows and the actual Mac command-line preview now download signed updates in
the background and offer Restart to update. The Mac preserves its private
diagnostic configuration before replacing the private bootstrap with a public
binary. Restart offers wait for an idle connection and reserve the host during
worker preparation; failure restores pairing availability.

No visible UI, OS input hooks, real capture, permission requests, webhook test
sends or paid CI were used. Native macOS update worker/relaunch and end-to-end
click control remain unverified here. Historical records below describe their
respective releases, including the earlier blocked updater design.

## Alpha.11 - click control, single cursor and filled 1080p stream

The user reported alpha.10 native-build-ready/compiled, Screen Recording granted,
successful pairing/viewing approval and visible H.264 video. This is user-reported
Mac execution evidence, not a measured 60 fps result. The user confirmed that
input attempts were direct clicks into the view-only desktop, without using the
hidden Connection → Control Mac action. Alpha.11 makes the first desktop click
an explicit activation gesture and consumes its press/release.

Fresh headless checks pass: 33 core assertions, 34 actual TLS session checks
including the 1920/60 default request, 46 TLS transfer checks, in-memory
presentation checks, pointer geometry and six viewer race/activation groups,
all Go packages, Go vet and Windows Release build with zero warnings/errors.
Tests exercise activation without click-through; pending/armed gesture cancel,
blur and capture loss; fit letterbox rejection versus filled desktop targeting;
clean helper EOF; original fatal reason surviving secondary pipe teardown;
strict control-state codes/booleans and sanitized Mac diagnostic counts.
Independent review found no outstanding blocker after cancellation fixes.

Native desktop dimensions now map width 1920 to 1920×1080, with complete-content
stretching, a fill presentation and matching normalized input mapping. App
Priority retains fit. Apple documents
[`preservesAspectRatio`](https://developer.apple.com/documentation/screencapturekit/scstreamconfiguration/preservesaspectratio)
for macOS 14+, and cursor omission uses the existing ScreenCaptureKit
[`showsCursor`](https://developer.apple.com/documentation/screencapturekit/scstreamconfiguration/showscursor)
property. No Mac display mode or physical cursor setting is changed. Pure
native dimension/mailbox self-checks are included in the source; they have not
been executed for alpha.11 on a Mac.

All local checks remain headless. Actual alpha.11 Swift compilation, cursor
appearance, CGEvent input, 1080p/60 delivery and responsiveness require Mac
validation. No computer-use test, native capture/input, permission prompt,
webhook test send or paid CI was performed. Alpha.10 package/OS symlink/race
detector limitations below still apply; updater and built .app delivery remain
unresolved.

## Alpha.10 - Windows to Mac file transfer and native build diagnostics

Fresh Windows headless suite passes: 33 core assertions, 34 session checks,
46 transfer checks over loopback TLS, in-memory presentation checks, viewer/race
regressions, all Go packages and Go vet. Windows Release compilation has zero
warnings/errors. No visible UI, capture, OS input hooks, or webhook test sends.

Transfer coverage includes certificate pins, paired capabilities, byte-for-byte
files/ZIPs, SHA-256 receipts, Unicode names, empty/nested folders, safe no-overwrite
names, cancellation, session revocation and process shutdown cleanup, malformed
and oversized metadata, traversal/symlink/duplicate ZIP entries, ZIP64 and bounded
actual central-directory scans. A 342-byte ZIP generated with the production
Windows `FileTransferPackage.CreateFolderAsync` is accepted by the production Go
receiver with its Unicode file and empty directory intact. Real TLS tests also
verify file-only pairing after native setup/catalog failure and cancellation of
a stalled upload. Independent review findings on shutdown/reconnect, publication
and ZIP allocation limits were resolved before packaging.

Some OS symlink-creation tests skip on this Windows account because it lacks
the required privilege; ZIP symlink rejection tests run. Go's race detector
cannot run in the current environment because cgo is disabled. No race-detector
pass is claimed.

The Mac bootstrap now resolves the selected macOS SDK explicitly and records
fixed compiler categories plus numeric source line/column and exit status.
Full bounded compiler output stays in `last-build-error.txt` in the Mac cache.
Synthetic SDK/cache/compiler tests pass. **The user's alpha.9 report contains
no compiler stderr, so its exact cause remains unconfirmed.** Current Swift
drivers can already infer an SDK; explicit selection is a robustness change,
not proof that this specific native compilation failure is fixed.

Native compilation, actual Mac transfer/filesystem behavior, capture/input,
delivered 60 fps and latency while transferring remain unverified. Video still
requires 60 fps; capture setup failure leaves file transfer available instead
of substituting 15/30 fps. The AppKit menu-bar launcher remains source-only,
and secure automatic update application prerequisites remain unresolved.

Windows development machine, 2026-10-23 UTC. Dedicated project was created in C:\Users\hunnid\Documents\Codex\2026-10-22\task-6 and explicitly moved to C:\Users\hunnid\Documents\code\01_My_Projects\WIP\MAC-DOW. Source/Git hashes verified for 43 files at move time; map in move-map.json. No other project was changed.

## Passing headless checks

- 26 .NET core assertions: explicit arming, view-only default, focus/tab/connection revocation, repeated release (100 cycles), Alt+Tab mapping, right Alt, reserved Windows key exclusion, letterbox pointer mapping, second-monitor selection, preferred monitor, disconnect/reconnect fallback, DPI/negative-origin geometry, and private IPv4 pairing validation.
- Node frame-envelope, sequence-gap/IDR recovery and pointer mapping tests.
- Mock-runtime viewer regressions: repeated H.264 configuration after keyframe preserves following delta; delayed JPEG completion cannot repaint after clear.
- Go synthetic TLS/WebSocket tests: fresh identity/fingerprint, private-address rejection, event limits, authenticated catalog and consented synthetic frame, Origin rejection, stream bounds/unknown window, and validated held-input recovery metadata.
- go vet.
- .NET 10 Windows Release build: zero warnings, zero errors.
- Non-UI diagnostics read WebView2 runtime 155.0.4283.62 and two actual monitors; selection chose DISPLAY2 at physical origin (-1920,-47), 1920x1032 work area, DPI 96. Diagnostic reported UI/hooks/capture all false.
- Independent read-only review identified six Important issues. Fixes address bounded/receipt-timed native mailbox, explicit control generations, graceful/helper-supervisor key release, metadata/keyframe order, excluded-input targets and stale asynchronous images. Native portions of these fixes remain source-inspected pending a Mac compiler.

Tests never opened a visible application, installed actual input hooks, injected OS input or captured a real screen.

## Native Apple Silicon validation - 2026-10-23 UTC

Dedicated checkout on the connected MacBook Pro at the alpha.1 commit. macOS 15.5 (24F74), arm64; existing Apple Command Line Tools, Swift 6.1.2 and Go 1.24.1. No installation or license acceptance required. Build concurrency capped at two for the 8 GB machine; compiler/dependency caches confined to this task workspace.

- Native optimized arm64 macOS 14+ Swift helper and Go host compile. Swift build is warning-free after native review fixes.
- Native lease expiration/release, bounded mailbox, urgent release and overflow self-tests pass, without permission queries, capture or event posting.
- Host fresh TLS identity self-test passes.
- Native Go synthetic TLS/WebSocket tests and consent codec regression pass; go vet and race detector pass.
- Node frame/recovery/mapping and asynchronous viewer regressions pass on Node 26.
- MacDowHost --check reports Screen Recording missing and Accessibility missing, returns 1 (helper returns 2), without requesting either permission.
- Mach-O arm64 executable and deployment metadata inspected. Ad-hoc linker signatures are not Developer ID signing or notarization.
- Windows alpha.1 binary is carried forward byte-for-byte, with its original SHA-256. Windows is unavailable in this environment, so no new Windows build or desktop validation is claimed.

## Not verified / remaining steps

No screen capture, window enumeration, event injection, visible app UI, listener/remote host, pairing, permission grant, persistent service, signing credentials, paid CI or security bypass was started. Synthetic test servers only bound ephemeral loopback ports.

Screen Recording and Accessibility need the user's deliberate grants if they choose to proceed. Follow README instructions for the Terminal identity, foreground launch, fingerprint/token pairing and ALLOW, then manual-validation.md with both machines. Hardware H.264/decoder state, real app scope behavior, physical monitors and end-to-end Wi-Fi latency remain untested. No performance measurements are claimed.

The existing manual Actions workflow remains unrun; no billing scope was added. Native local builds replace the need for CI compilation for this prerelease. Distribution is unsigned/developer-unsigned and not notarized; macOS protections remain enabled.

## Alpha.3 updater validation - Windows, 2026-10-23 UTC

- Full headless suite passes: 26 core checks, viewer/decoder race regressions, Go host and updater fixtures, go vet, .NET Windows Release compile (zero warnings/errors).
- Synthetic updater tests cover channels/platform/architecture and malformed versions, missing/ambiguous digest metadata, traversal/links/reserved paths/case collisions, partial downloads, tampered previews/archives/release metadata, signed fixture apply and rollback, preserved external settings, forged rollback paths/hashes, executable mode checks, and vulnerable GitHub CLI rejection. Test signatures are injected only inside Go tests; no production trust bypass exists.
- Independent review findings fixed: rollback authenticated from fresh older release and bound to caller install; recovery-needed state distinguished; required updater/marker packaging; Darwin executable bits; PowerShell BOM; canonical Mac fixture paths; rollback provenance regression actually invokes verifier.
- Repository API verified authenticated tbhtho, private tbhtho/MAC-DOW, ID1430459188. No PR, force push, credential/global config, autostart or firewall change.
- Installed gh 2.88.0 is below the safe signature-verification prerequisite. A prior alpha.2 verification probe used it and returned no attestations. After finding GHSA-8xvp-7hj6-mcj9, no further signature commands used that version. User-managed CLI upgrade/security review is separate from this project; no credentials were rotated or changed.
- Alpha.3 Mac Go host is cross-compiled for darwin/arm64, **unrun**. Swift capture source unchanged from alpha.2; packaged helper is reused byte-for-byte from the SHA-256 checked alpha.2 native archive, with provenance recorded inside the package. No alpha.3 native execution or end-to-end streaming validation is claimed.
- Current releases lack signed GitHub provenance: secure automatic apply/rollback remain blocked. The implemented usable path is check + explicit authenticated staging + clear manual fallback.
