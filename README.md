# MAC-DOW

Your Windows desktop and your Mac, in two tabs on the second monitor. Or stream one selected Mac window in a normal Windows window with **App Priority**.

**Private development prerelease.** Windows compiles and headless tests pass. Real Mac capture/control, hardware codec behavior and Wi-Fi editing latency have not been validated. The Mac is offline. This release includes a Windows portable app and a **Mac build kit**, not a verified ready-to-run Mac companion.

[Prereleases](https://github.com/tbhtho/MAC-DOW/releases) · [Validation status](docs/verification.md) · [Hardware checklist](docs/manual-validation.md)

## What is implemented

- A restrained 44-DIP top strip on the first non-primary Windows monitor, with explicit monitor selection and primary fallback. **Windows** reveals the local desktop; **MacBook** fills that monitor's working area. This is an overlay; it does not reserve OS desktop space.
- **App Priority** hides that tab shell entirely and opens a movable, resizable, minimizable/maximizable Windows window. Only the explicitly selected Mac window is captured. Its application keeps running on the Mac; the streamed Mac controls retain their Mac appearance.
- A foreground Mac companion with ScreenCaptureKit capture, VideoToolbox hardware H.264 encoding and a Windows WebCodecs decoder that prefers hardware acceleration. Actual Windows hardware decoding depends on the installed runtime/GPU and is unverified. JPEG is an explicit diagnostic option.
- Direct LAN TLS 1.3/WebSockets, ephemeral 256-bit token, manual SHA-256 fingerprint verification and per-session ALLOW on the Mac. No cloud relay/discovery, daemon, autostart or saved pairing.
- View-only by default. Click **Control Mac** while focused to arm. Left Alt+Tab maps to Command+Tab; right Alt maps to Option. **Ctrl+Alt+Shift+Backspace** releases control. Focus/tab/monitor changes, disconnect and expired leases revoke input.
- Width caps 960/1280/1600/1920, 10/15/30 fps and 2–20 Mbit/s H.264 choices before a stream starts. Bounded queues and sequence-gap recovery favor recent frames. RTT, encoder time and local decode/submit time are shown; these are not end-to-end latency.

## Windows

Extract the complete Windows ZIP and run **MAC-DOW.exe**. It is a self-contained x64 .NET application. It requires an existing Microsoft **WebView2 Runtime**. Nothing is installed silently. If the runtime is absent, the app reports that prerequisite; obtain it yourself from [Microsoft](https://developer.microsoft.com/microsoft-edge/webview2/).

The application has not been opened on your desktop during development. Physical monitor behavior remains on the acceptance checklist.

## Mac companion

Apple Silicon, macOS 14+. There is no Mac tab UI.

**Build-kit status:** the Go host is cross-compiled for darwin/arm64 and has not been executed on macOS. The native Swift capture helper is supplied as source and must be compiled on a Mac before use. No native build or notarization is claimed.

On a Mac with an **existing** Swift/Xcode Command Line Tools compiler, extract the build kit, open Terminal there, and run:

```sh
bash build-native-helper.sh
./MacDowHost --check
./MacDowHost --listen YOUR_PRIVATE_MAC_IPV4:4433
```

The build script installs nothing. If the compiler is absent, stop until the user chooses to obtain it or a verified CI build is available.

The host checks existing Screen Recording access without asking for it. The user must choose and grant required macOS permissions in System Settings. Accessibility is additionally needed for remote input; absent access keeps control off. macOS may attribute terminal-launched programs to Terminal; verify the actual permission identity on the Mac. These permission flows remain untested.

The host prints its temporary token and certificate fingerprint. On Windows:

1. Open **Pair / App Priority**, enter the private IPv4/port, compare the fingerprint directly with the Mac, and enter its temporary token.
2. Connect, choose full desktop or a specific window and streaming settings.
3. On the Mac, check the requested scope/settings and type **ALLOW**.
4. Wait for video, then click **Control Mac** when you want input routed remotely. Returning after release always needs another click.

Quit the foreground Mac host with Ctrl+C. Restarting it changes both pairing token and TLS identity. No firewall or router settings are changed by this project.

## App Priority limits

This prerelease captures **one window**, not every window belonging to an app. Menus, popovers, child windows and modal dialogs are excluded. Conservative Accessibility/window-order and pointer-hit checks pause input if its target is outside the captured scope; some apps may therefore remain view-only or require rearming. Validation on the actual app is pending.

Closing/minimizing the selected Mac window stops the stream, with no desktop fallback. Resizing that Mac window also stops the stream to avoid unverified coordinate mapping; reconnect to renegotiate its geometry. Resizing/moving the Windows viewer is supported by aspect-fit mapping. Native-Windows seamlessness is not claimed.

No audio, clipboard, file transfer, IME, multi-touch, stylus or non-US physical-layout support. Windows reserved security shortcuts remain OS-owned. A simultaneous hard kill of the entire host/helper process tree or OS failure cannot guarantee release of synthetic input; normal disconnects, client loss and helper failure have lease/EOF/supervisor recovery paths.

## Build and test

Windows: .NET SDK 10, Go 1.24+, Node 22+.

```powershell
.\scripts\test.ps1
.\scripts\package-windows.ps1
```

Native Apple Silicon from a full checkout with existing Go and Swift toolchains:

```sh
bash scripts/build-macos.sh
```

The manual [native build workflow](.github/workflows/native-build.yml) requires explicit confirmation that sufficient included free Actions minutes exist. It has no push/tag trigger and has not run. Current credentials cannot read the account's billing allowance. No permissions were expanded and no paid CI was started. A successful workflow verifies its exact commit's native compile and non-capturing self-tests; it still does not validate real screen/control behavior.

Unsigned/developer-unsigned and not notarized. No signing identities or certificates have been configured. If OS security blocks an artifact, keep protection enabled and use a trusted source build or wait for a properly signed distribution.

See [protocol](docs/protocol.md), [architecture](docs/design.md) and [third-party notices](THIRD_PARTY_NOTICES.md).

