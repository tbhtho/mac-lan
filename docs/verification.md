# Verification record

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
