# Development

The README describes the current preview workflow. This document keeps build instructions and earlier development context out of that introduction. Verify the exact downloaded version against [validation status](verification.md); an older release does not gain a feature just because it appears on the development branch.

The existing repository is now named `tbhtho/mac-lan` (repository ID 1430459188), with public source, releases, and a signed update feed in the same repository. Install alpha.14 on both computers once to move from the withdrawn feed to this distribution. Subsequent updates need no GitHub sign-in.

## Local builds

Windows development and packaging require .NET SDK 10, Go 1.24+, Node 22+, and Python 3:

```powershell
./scripts/test.ps1
./scripts/package-windows.ps1
./scripts/test-standalone.ps1
```

Run these commands from the repository root. The Windows package is one `artifacts/mac-lan.exe`, including its .NET runtime, native loader, viewer, and discovery/updater helper. Internal resources extract to a private application cache. The existing Microsoft WebView2 Runtime is an external prerequisite.

Build the cross-compiled Apple Silicon menu-bar app and command-line update bridge on Windows:

```powershell
./scripts/package-macos-app.ps1
```

The app packager always produces public packages with no embedded diagnostic destination. Its underlying `package-macos-preview.ps1` retains optional `-PublicUpdate` and explicit `-PrivateBootstrap` modes. A private bootstrap reads `MACDOW_DIAGNOSTICS_WEBHOOK` or the ignored `.cache/discord-webhook.txt`, and is written only under `artifacts/private-bootstrap`. It saves that configuration locally on the Mac for later public updates. Never publish the private bootstrap or its embedded destination. Existing private diagnostic configuration remains in its legacy per-user location when the public preview is installed.

The primary archive is `artifacts/update-public/mac-lan-macos-arm64.tar.gz`, containing `mac-lan.app`. Its arm64 Go entry point compiles embedded AppKit sources into the user's application cache using existing Apple command-line tools and launches the verified native menu. It stays alive to preserve the app's LaunchServices identity. The app has a status icon without a Dock icon or main window. Sharing starts discovery and pairing/file-transfer service; screen access and separate video consent remain required for capture. Menu actions expose sharing, permission settings, updates, and Quit.

Both Mac packages embed canonical `native/MacDowCapture.swift`. Video setup compiles it into the existing application cache, runs a non-capturing self-test, and verifies the cached binary on subsequent launches. Menu compilation uses a separate verified cache. No tool installation, license acceptance, or OS security change occurs. Full bounded compiler output stays in the Mac cache as `last-build-error.txt`; diagnostics contain fixed categories and numeric source locations. A Windows cross-build and archive check do not prove native Swift compilation or visible menu behavior.

`package-macos-app.ps1` signs the app with the mac-lan certificate (`scripts/macos_codesign.py`; key in `.release-private/macos-codesign.pem`, see [publishing](update-feed-publishing.md)) so macOS privacy grants survive updates; without the key it signs ad hoc for local testing. `./scripts/package-installers.ps1` then builds the first-install downloads from those packages: `artifacts/mac-lan-setup.exe` (per-user NSIS setup; requires an existing NSIS 3) and `artifacts/update-public/mac-lan-macos-arm64.dmg` (drag-to-Applications image written on Windows as ISO 9660 with Rock Ridge; its macOS mount still needs a Mac check).

`package-macos-app.ps1` also builds `mac-lan-macos-arm64-commandline-preview.tar.gz`. Alpha.14's updater still recognizes this layout. A normal launch of its updated `MacDowHost` installs a separate matching app in `~/Applications/mac-lan.app`, opens it, and exits; it preserves the old extracted folder and refuses unrelated existing app installations. Explicit diagnostic/command-line modes remain available. `MAC-DOW-macos-arm64-unsigned.tar.gz` is a byte-identical app archive alias needed by alpha.14's strict feed validator; the canonical app name must not be inserted into that shared feed as an unknown asset.

A full native source build requires existing Go, Swift, and Python 3 on Apple Silicon:

```sh
bash scripts/build-macos.sh
```

That script also defaults to no embedded diagnostic destination. A private native bootstrap requires explicit `MACDOW_PRIVATE_BOOTSTRAP=1` and the existing private diagnostic configuration. The native build compiles and self-tests the menu and capture code in the workspace before packaging the same launcher layout; first app launch prepares its separate user cache. Screen Recording and Accessibility must be granted deliberately in macOS System Settings, potentially again for the app even if Terminal was allowed. Keep OS protections enabled; quarantine removal and Gatekeeper bypass are not project setup steps. The app is not Developer ID signed or notarized.

The optional [native workflow](../.github/workflows/native-build.yml) has no push/tag trigger. It requires explicit confirmation that sufficient included free Actions minutes exist. It has not provided proof of current native capture or control. Do not start paid CI or broaden account permissions as an incidental publishing step.

## Publishing

Use [signed update publishing](update-feed-publishing.md) for the feed, pinned-key verification, immutable artifact identities, and private-key handling. Publish public artifacts without the Mac diagnostic destination. Do not rewrite published Git history to improve presentation or replace an old asset under its existing version.

Build Windows and both Mac layouts from clean committed source, then publish from this existing repository using `./scripts/publish-release.ps1 -NotesFile <path>`; there is no separate downloads repository. The publisher validates existing packages rather than building them. It coordinates six immutable assets: Windows EXE, canonical app archive, byte-identical app compatibility alias, command-line bridge archive, source ZIP, and checksum list. The signed feed at `updates/feed.json` uses the three alpha.14-compatible binary asset names. Diagnostic configuration and signing keys stay outside version control and public artifacts. Verify the feed and every uploaded asset anonymously after publication.

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
- **Alpha.15:** the Mac package becomes a menu-bar `.app`, with first-launch native AppKit compilation through existing Apple tools, deliberate app permission grants, and a compatibility update bridge from alpha.14. Full-desktop capture preserves source proportions within a 1920-pixel width and 2160-pixel height cap without enlargement. Windows fits the complete picture, rejects input in surrounding bars, and maps input within the picture even when an older host announces `fill`. Pixels already stretched by an older native helper require the new capture helper to correct. Native menu/capture/update execution and measured responsiveness remain separate from headless checks.

## Runtime boundaries

Alpha.34 has [local disconnect diagnostics](windows-diagnostics.md)
with background disk writes, connection/run correlation, bounded file rotation,
and transport/UI/decoder timing. Use `./scripts/test.ps1` for the headless logger,
synthetic disconnect and viewer regressions. No current-source logging change
establishes that the Fluxframe disconnect is fixed.

The alpha.32 Mac diagnostic `capture-failed` with code `input-overflow` recorded the
native helper leaving capture because its bounded input mailbox overflowed. Alpha.34
removes that fatal exit at every hop: the viewer coalesces absolute pointer
moves to one per animation frame, the Windows session and the Mac host drop excess
input instead of disconnecting, and the native mailbox coalesces moves and answers
pressure by releasing held input and pausing control with `input-overflow`. Video,
audio and the session stay up; the next click resumes control. The pressure source
for a specific report is still inferred from the fixed code, not measured.

Alpha.33 shipped the same input fix but its native helper failed the new self-test
on first launch, so video could not start; alpha.34 corrects that check (the button
message that follows a move cleared the recorded pointer coordinate) and saves a
failing self-test's output to the Mac's `last-build-error.txt`. Alpha.34 also fixes
the remaining "MAC-DOW" wording in Windows discovery and shows the actual Mac setup
failure instead of Screen Recording steps when permissions are not the cause.

Direct LAN TLS 1.3 and WebSockets carry video and input, with ephemeral launch identity, local pairing approval, one controller, and separate video approval. There is no cloud relay, STUN/TURN, saved pairing, daemon, or automatic login startup. An older command-line mode printed a temporary token and fingerprint and accepted `--listen`; the normal discovery flow needs neither manual entry nor a selected interface argument. A lease gap releases synthetic input after 850 ms; capture keeps running for up to 15 s without a viewer message, so brief Wi-Fi or driver stalls do not end the stream. A truly closed viewer still exits promptly.

Hardware H.264 uses ScreenCaptureKit and VideoToolbox on the Mac. Windows WebCodecs prefers hardware decoding, subject to the installed runtime and GPU. Dependency-aware encoded-frame queues, bounded buffers, and sequence-gap recovery preserve decodability; decoded pictures coalesce for display. RTT, encode time, and local decode/submit time are local measurements, not synchronized capture-to-display latency. Reliable TCP may stall behind retransmission; WebRTC/UDP behavior is not claimed.

App Priority captures one selected window, not all windows belonging to an app. Conservative scope checks can pause input; a closed, minimized, or resized target stops capture without falling back to the desktop. Input leases, EOF handling, and the supervisor recover from normal disconnects and failures; a simultaneous hard kill of the entire process tree or OS failure cannot guarantee synthetic-key release.

Transfer staging uses bounded buffers and separate HTTPS, but shares LAN bandwidth with video. Windows needs temporary space for a folder ZIP, and the Mac needs staging and extraction space. See the full [transfer contract](file-transfer.md) for limits and destination rules. There is no Mac-to-Windows transfer, audio, shared clipboard, IME, multi-touch, stylus, or non-US physical-layout support.

[Protocol](protocol.md) · [Architecture](design.md) · [Hardware checklist](manual-validation.md) · [Third-party notices](../THIRD_PARTY_NOTICES.md)


The Mac host embeds the capture and menu Swift sources and Info.plist with `go:embed` under the `nativeembed` build tag. `package-macos-preview.ps1` writes them, gzip-compressed, to the ignored `host/nativeembed` folder before building; only their SHA-256 values are linker flags. Ordinary builds and tests use no tag and embed nothing.
