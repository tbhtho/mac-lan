# Development

The README describes the current preview workflow. This document keeps build instructions and earlier development context out of that introduction. Verify the exact downloaded version against [validation status](verification.md); an older release does not gain a feature just because it appears on the development branch.

The existing repository is now named `tbhtho/mac-lan` (repository ID 1430459188), with public source, releases, and a signed update feed in the same repository. Install alpha.14 on both computers once to move from the withdrawn feed to this distribution. Subsequent updates need no GitHub sign-in.

## Local builds

Windows requires .NET SDK 10, Go 1.24+, and Node 22+:

```powershell
./scripts/test.ps1
./scripts/package-windows.ps1
./scripts/test-standalone.ps1
```

Run these commands from the repository root. The Windows package is one `artifacts/mac-lan.exe`, including its .NET runtime, native loader, viewer, and discovery/updater helper. Internal resources extract to a private application cache. The existing Microsoft WebView2 Runtime is an external prerequisite.

Build the cross-compiled Apple Silicon command-line preview on Windows:

```powershell
./scripts/package-macos-preview.ps1
```

The Mac package is public by default and has no embedded diagnostic destination. `-PublicUpdate` remains an optional compatibility flag. A private bootstrap requires explicit `-PrivateBootstrap`, reads `MACDOW_DIAGNOSTICS_WEBHOOK` or the ignored `.cache/discord-webhook.txt`, and is written only under `artifacts/private-bootstrap`. It saves that configuration locally on the Mac for later public updates. Never publish the private bootstrap or its embedded destination. Existing private diagnostic configuration remains in its legacy per-user location when the public preview is installed.

The Mac preview embeds canonical `native/MacDowCapture.swift`. First launch compiles it into the user's application cache with the Mac's existing Apple command-line tools, runs a non-capturing self-test, and verifies the cached binary on subsequent launches. It does not install tools, accept licenses, or alter OS security. Full bounded compiler output stays in the Mac cache as `last-build-error.txt`; diagnostics contain fixed categories and numeric source locations.

A full native source build requires existing Go and Swift toolchains on Apple Silicon:

```sh
bash scripts/build-macos.sh
```

That script also defaults to no embedded diagnostic destination. A private native bootstrap requires explicit `MACDOW_PRIVATE_BOOTSTRAP=1` and the existing private diagnostic configuration. The source for a menu-bar-only AppKit launcher is present, but the distributed Mac preview is still a Terminal companion, not a built `.app`. Capture and Accessibility permissions must be granted deliberately in macOS System Settings. Keep OS protections enabled; quarantine removal and Gatekeeper bypass are not project setup steps.

The optional [native workflow](../.github/workflows/native-build.yml) has no push/tag trigger. It requires explicit confirmation that sufficient included free Actions minutes exist. It has not provided proof of current native capture or control. Do not start paid CI or broaden account permissions as an incidental publishing step.

## Publishing

Use [signed update publishing](update-feed-publishing.md) for the feed, pinned-key verification, immutable artifact identities, and private-key handling. Publish public artifacts without the Mac diagnostic destination. Do not rewrite published Git history to improve presentation or replace an old asset under its existing version.

Build both packages from clean committed source, then publish from this existing repository using `./scripts/publish-release.ps1 -NotesFile <path>`; there is no separate downloads repository. The local publisher does not build binaries. It coordinates immutable public assets, a source ZIP, checksum list, and the signed feed at `updates/feed.json`. Diagnostic configuration and signing keys stay outside version control and public artifacts. Verify the feed and both binaries anonymously after publication.

See [update implementation](update-plan.md), [standalone packaging](standalone-plan.md), and [standalone verification](standalone-verification.md). Headless tests and package checks establish only their tested behavior; they do not establish measured capture rate, latency, physical monitor behavior, or end-to-end Mac updates.

## Earlier development context

These notes are historical; older ZIP, manual token, and Terminal-only update instructions are not the current quick start.

- **Alpha.2:** the Swift capture helper was natively compiled and self-tested on Apple Silicon macOS 15.5 with Swift 6.1.2. Native lease/mailbox and TLS checks passed. This did not prove real capture/control or hardware encoding.
- **Alpha.3:** a cross-compiled Go Mac host added updater staging. Downloads required an already authenticated GitHub CLI; automatic application required signed release attestations and GitHub CLI 2.93+. Those unattested releases could not update themselves automatically. The version requirement followed [GHSA-8xvp-7hj6-mcj9](https://github.com/cli/cli/security/advisories/GHSA-8xvp-7hj6-mcj9). That legacy workflow does not describe the new public signed-feed updater.
- **Alpha.4–5:** Windows moved toward a single self-contained EXE, a separate connection window, readable discovery, and a desktop bar that reserves OS space instead of covering app title bars. Manual connection details were removed. Windows diagnostics became local-only; Mac diagnostics use a private destination. AppKit menu-bar source was added without claiming a packaged `.app`.
- **Alpha.6:** a release/control message could reach the host before the stream-start request and terminate pairing. Client phase checks and the host's idle-message handling fixed that handshake. Capture errors now reach Windows before transport closure, and selectors show readable device labels. Synthetic TLS session and in-memory presentation tests cover these regressions without opening windows or capturing a screen.
- **Alpha.7:** the MacBook tab became a plain video surface below the tab bar, with no white toolbar, footer, floating badge, or extra desktop button. Connection actions moved to the top-bar menu. Native status backpressure and bounded failure reasons replaced unexplained disconnects.
- **Alpha.8:** a temporary 15 fps JPEG compatibility default was rejected because responsiveness requires at least a 60 fps target. It is not the current default.
- **Alpha.9:** the entire pipeline adopted 60 fps, initially at a 1280-pixel width cap. Older hosts without that capability are refused before viewing. The corrected Swift source replaced the old prebuilt helper via verified first-launch native compilation. The encoder still requires hardware at creation; the redundant hardware-status property query became diagnostic-only. Apple's [hardware requirement](https://developer.apple.com/documentation/videotoolbox/kvtvideoencoderspecification_requirehardwareacceleratedvideoencoder) specifies failure when hardware is unavailable. A source target is not proof of sustained output rate.
- **Alpha.10:** explicit SDK resolution and fixed compiler diagnostics followed a native-build failure whose exact original cause was unknown. The user confirmed successful native compilation and live video. Authenticated Windows-to-Mac transfer added files, ZIPs, and folders. Discovery, pairing, and transfer remain usable when video setup fails rather than starting a lower-rate stream.
- **Alpha.11:** full desktop output adopted 1920×1080 at a 60 fps target, stretched to fill without cropping or changing Mac display settings. App Priority retained aspect fit, and pointer mapping follows the presentation mode. Native video omits its cursor so Windows supplies one immediate pointer. A consumed activation click replaced the hidden-menu-only control flow; normal input-pipe EOF releases inputs and exits cleanly.
- **Alpha.12:** the updater work replaces GitHub-authenticated downloads with a signed public feed and public binaries. The private Mac bootstrap preserves its existing diagnostic destination locally, keeping subsequent public Mac updates free of that secret. Current implementation and validation evidence belong in the linked update and verification documents.
- **Alpha.13:** bounded dependency-aware queues and browser batches replace arbitrary encoded-frame dropping. Decoder output coalesces for display, stream epochs reject delayed callbacks, and the Mac reports fixed control-release reasons. The user confirmed native control in alpha.12; the new delivery path has synthetic and real software-codec checks, with hardware responsiveness still awaiting user testing. Distribution at that point remained manual in the original private repository.
- **Alpha.14:** the public app name becomes `mac-lan`. The same repository is renamed and made public, and signed automatic updates use its releases and `updates/feed.json`. Public packages omit private diagnostic destinations; existing settings, cache paths, permissions identity, and `MacDowHost` remain compatible. Earlier private bootstrap assets are not advertised as public downloads. See the current verification record for the distinction between headless checks, cross-compilation, and native execution.

## Runtime boundaries

Direct LAN TLS 1.3 and WebSockets carry video and input, with ephemeral launch identity, local pairing approval, one controller, and separate video approval. There is no cloud relay, STUN/TURN, saved pairing, daemon, or automatic login startup. An older command-line mode printed a temporary token and fingerprint and accepted `--listen`; the normal discovery flow needs neither manual entry nor a selected interface argument.

Hardware H.264 uses ScreenCaptureKit and VideoToolbox on the Mac. Windows WebCodecs prefers hardware decoding, subject to the installed runtime and GPU. Dependency-aware encoded-frame queues, bounded buffers, and sequence-gap recovery preserve decodability; decoded pictures coalesce for display. RTT, encode time, and local decode/submit time are local measurements, not synchronized capture-to-display latency. Reliable TCP may stall behind retransmission; WebRTC/UDP behavior is not claimed.

App Priority captures one selected window, not all windows belonging to an app. Conservative scope checks can pause input; a closed, minimized, or resized target stops capture without falling back to the desktop. Input leases, EOF handling, and the supervisor recover from normal disconnects and failures; a simultaneous hard kill of the entire process tree or OS failure cannot guarantee synthetic-key release.

Transfer staging uses bounded buffers and separate HTTPS, but shares LAN bandwidth with video. Windows needs temporary space for a folder ZIP, and the Mac needs staging and extraction space. See the full [transfer contract](file-transfer.md) for limits and destination rules. There is no Mac-to-Windows transfer, audio, shared clipboard, IME, multi-touch, stylus, or non-US physical-layout support.

[Protocol](protocol.md) · [Architecture](design.md) · [Hardware checklist](manual-validation.md) · [Third-party notices](../THIRD_PARTY_NOTICES.md)
