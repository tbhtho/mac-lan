# MAC-DOW

Your Windows desktop and your Mac, in two tabs on the second monitor. Or stream one selected Mac window in a normal Windows window with **App Priority**.

**Private development prerelease.** Windows compiles and headless tests pass. The Swift capture helper was natively compiled and self-tested on macOS 15.5 for alpha.2. The alpha.3 Go host adds updates and is cross-compiled for arm64 but has not been run on macOS. Real capture/control, hardware codec behavior and Wi-Fi editing latency remain untested. This release includes a Windows portable app and an **Apple Silicon command-line companion**, with those validation limits.

[Prereleases](https://github.com/tbhtho/MAC-DOW/releases) � [Validation status](docs/verification.md) � [Hardware checklist](docs/manual-validation.md)

## What is implemented

- A restrained 44-DIP top strip on the first non-primary Windows monitor, with explicit monitor selection and primary fallback. **Windows** reveals the local desktop; **MacBook** fills that monitor's working area. This is an overlay; it does not reserve OS desktop space.
- **App Priority** hides that tab shell entirely and opens a movable, resizable, minimizable/maximizable Windows window. Only the explicitly selected Mac window is captured. Its application keeps running on the Mac; the streamed Mac controls retain their Mac appearance.
- A foreground Mac companion with ScreenCaptureKit capture, VideoToolbox hardware H.264 encoding and a Windows WebCodecs decoder that prefers hardware acceleration. Actual Windows hardware decoding depends on the installed runtime/GPU and is unverified. JPEG is an explicit diagnostic option.
- Direct LAN TLS 1.3/WebSockets, ephemeral 256-bit token, manual SHA-256 fingerprint verification and per-session ALLOW on the Mac. No cloud relay/discovery, daemon, autostart or saved pairing.
- View-only by default. Click **Control Mac** while focused to arm. Left Alt+Tab maps to Command+Tab; right Alt maps to Option. **Ctrl+Alt+Shift+Backspace** releases control. Focus/tab/monitor changes, disconnect and expired leases revoke input.
- Width caps 960/1280/1600/1920, 10/15/30 fps and 2-20 Mbit/s H.264 choices before a stream starts. Bounded queues and sequence-gap recovery favor recent frames. RTT, encoder time and local decode/submit time are shown; these are not end-to-end latency.

## Windows

Extract the complete Windows ZIP and run **MAC-DOW.exe**. It is a self-contained x64 .NET application. It requires an existing Microsoft **WebView2 Runtime**. Nothing is installed silently. If the runtime is absent, the app reports that prerequisite; obtain it yourself from [Microsoft](https://developer.microsoft.com/microsoft-edge/webview2/).

The application has not been opened on your desktop during development. Physical monitor behavior remains on the acceptance checklist.

## Mac companion

Apple Silicon, macOS 14+. There is no Mac tab UI.

**Build status:** The unchanged Swift helper comes byte-for-byte from the alpha.2 native build on Apple Silicon macOS 15.5 (Swift 6.1.2). The alpha.3 Go host is cross-compiled on Windows with Go 1.27 and remains unrun on macOS. Alpha.2 native lease/mailbox and TLS tests passed; alpha.3 updater tests use synthetic fixtures on Windows. This is a foreground Terminal companion, unsigned and not notarized; real capture/input and hardware encoding are still untested.

Extract `MAC-DOW-macos-arm64-unsigned.tar.gz`, open Terminal in its `macos-arm64` folder, and first run:

```sh
./MacDowHost --check
```

This only reads existing permission status. Missing Screen Recording returns a nonzero status. When you choose to test with both machines, grant Screen Recording yourself under **System Settings  Privacy & Security  Screen & System Audio Recording** (wording may vary). Grant **Accessibility** only if you want remote control. Verify the identity macOS attributes to these Terminal-launched programs; restart them after changing permissions. Then start the foreground host yourself:

```sh
./MacDowHost --listen YOUR_PRIVATE_MAC_IPV4:4433
```

No service or autostart is installed. Keep macOS security protections enabled. If the downloaded unsigned binary is blocked, build the trusted source using `bash scripts/build-macos.sh`; no quarantine removal or Gatekeeper bypass is part of this project.

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

## Updates (alpha.3)

On Windows, open **Updates** from the tab strip or App Priority toolbar. Checks run at startup and every six hours while open; the dialog can disable automatic checking and choose stable/preview. **Update** downloads to staging only. On the Mac, run `./MacDowHost --updates` for the foreground Check/Update/Apply/Rollback console; `--update-check` checks once, and `--no-update-check` disables the normal host startup check. No listener or capture starts in update-only mode.

Private downloads use an **already authenticated GitHub CLI** with access to this repository. MAC-DOW never requests, embeds, logs or stores a token. Without existing access, it reports the manual private-release path. GitHub CLI 2.93.0+ is required for signature verification because older versions are affected by [GHSA-8xvp-7hj6-mcj9](https://github.com/cli/cli/security/advisories/GHSA-8xvp-7hj6-mcj9). Nothing is installed or authenticated automatically.

Version/channel/platform/architecture, fixed private repository identity, exact asset, SHA-256, bounded extraction and commit identity are checked. Automatic **Apply** additionally requires [GitHub's signed release attestation](https://cli.github.com/manual/gh_release_verify-asset). Current releases have no such attestation, so Apply is disabled; authenticated download/staging and manual review remain available. A checksum alone is not presented as a signature. No repository security setting, certificate or signing credential was changed.

For a future attested release, Apply requires a separate confirmation, waits for the Windows app to exit, revalidates, swaps a recognized portable installation and keeps the previous folder. Relaunch is opt-in and reopens view-only; it never restarts capture/control. Settings live outside the installation. A detached worker result is saved and shown the next time Updates opens. Failed swaps attempt immediate restoration and report recovery-needed when restoration fails.

Explicit rollback downloads and verifies the older published release again, preserving external settings. It requires network/private access and signed provenance for that older version; it never executes an editable cached backup based on journal hashes. Unattested old versions retain a **manual recovery folder only**. Root folders, development checkouts and linked install/cache paths are refused. No installed user application was replaced during testing.
