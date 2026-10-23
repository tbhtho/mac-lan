# Verification record

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

## Not verified / blockers

- Native Swift source has not compiled or run. No authorized Mac is online.
- GitHub APIs user/settings/billing/usage and users/tbhtho/settings/billing/usage returned 404; account plan was unavailable with the current token scope. No scope expansion was attempted. Native Actions workflow remains manual and unrun because zero-cost capacity cannot be confirmed.
- Apple Silicon Go host is cross-compiled, not executed or native-validated. Build kit is not a runnable Mac release.
- No native capture, permissions, app/window/menus, physical monitor/DPI behavior, actual keyboard hook, hardware H.264 path, hardware Windows decoder or Wi-Fi latency/quality measurement.
- No code-signing or notarization; no signing credentials available/configured. OS protections were not disabled.

## Remaining path

Verify sufficient included Actions minutes at no cost and run manual native workflow for the release commit, or compile on an authorized Apple Silicon Mac with existing toolchains. Resolve any compiler errors before replacing the clearly labeled build-kit asset with a native binary artifact. Then the user grants required permissions, performs explicit pairing/ALLOW, and runs manual-validation.md. Do not advertise measured editing performance until that checklist produces evidence.
