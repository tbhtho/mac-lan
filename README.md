# MAC-DOW

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE) ![Windows x64 and Apple Silicon](https://img.shields.io/badge/platform-Windows_x64_%7C_Apple_Silicon-blue.svg)

Your Mac desktop and Windows desktop, one tab apart on the same LAN.

[**Download the latest preview**](https://github.com/tbhtho/MAC-DOW-Downloads/releases) · [Validation status](docs/verification.md)

## Get started

1. On **Windows 10/11 x64**, download and run **MAC-DOW.exe**. It is one executable with its .NET runtime included. An existing [Microsoft WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/) is required.
2. On **Apple Silicon macOS 14+**, extract **MAC-DOW-macos-arm64-commandline-preview.tar.gz** and open **MacDowHost**. Keep its Terminal window open. This is a command-line preview; the menu-bar `.app` is not packaged yet. First launch builds the capture helper using your existing Apple command-line tools.
3. Put both computers on the same LAN. On Windows, choose **Connection → Find my Mac → Connect**, compare the pairing number, and approve it on the Mac. Open the **MacBook** tab and allow video on the Mac.
4. Allow **Screen Recording** on the Mac for video and **Accessibility** for control in **System Settings → Privacy & Security**. Click the live Mac desktop once to activate keyboard and mouse; the activation click is consumed. **Ctrl+Alt+Shift+Backspace** releases control.

The unsigned preview may be blocked by OS security. Keep those protections enabled and use a trusted [source build](docs/development.md) if needed. No firewall, router, security setting, or Apple developer tool is changed or installed automatically.

## In the app

- **Windows / MacBook tabs:** switch between your local desktop and the live Mac picture. The desktop bar reserves space above maximized Windows apps, with a monitor selector and primary-monitor fallback.
- **Full desktop:** a **1920×1080, 60 fps target**, filling the tab with the complete Mac desktop. Different display proportions stretch the picture; the Mac's display mode stays unchanged. Only the immediate Windows cursor appears in the stream.
- **App Priority:** put one selected Mac window in a movable, resizable Windows window. It preserves the window's aspect ratio. Menus, dialogs, and other Mac windows remain outside that view; closing, minimizing, or resizing the selected Mac window stops its stream.
- **Files:** send files, ZIPs, or whole folders from Windows to the paired Mac, with drag-and-drop, progress, and cancellation. They arrive in **Downloads/MAC-DOW** with existing items preserved. Folders arrive as folders; ZIPs stay ZIPs. See [transfer limits](docs/file-transfer.md).
- **Control:** the first click activates input. Focus loss, tab or monitor changes, and disconnect release it. Left Alt+Tab maps to Command+Tab; right Alt maps to Option. Windows security shortcuts remain local. Connection also provides explicit control, release, and disconnect actions.
- **Streaming settings:** choose a width cap, H.264 bitrate, or compatible JPEG before starting video. Both codecs target 60 fps. JPEG uses more bandwidth; no 15/30 fps fallback starts automatically.

There is no audio, shared clipboard, IME, multi-touch, stylus, or non-US physical keyboard layout support. Capture performance and latency depend on the Mac, Windows decoder, and LAN; a 60 fps target is not a measured throughput guarantee. The user confirmed live Mac video in alpha.10. Current native control and updater behavior still require the two-computer checks in the [hardware checklist](docs/manual-validation.md).

## Updates

Both apps check for updates in the background and download a verified update before offering **Restart to update**. On Windows, use **Updates** in the desktop bar. The Mac command-line companion offers a native restart prompt while idle. Restart is your choice; reconnect afterwards and activate control again.

Updates come from the public [downloads repository](https://github.com/tbhtho/MAC-DOW-Downloads/releases), with no GitHub sign-in. A pinned Ed25519 publisher signature and SHA-256 hashes verify the feed and downloaded files. Settings stay outside the replaced executable. These publisher signatures do not make the preview OS code-signed or notarized.

Existing alpha.11 installations need **one final manual upgrade to alpha.12** before they can use this updater. Download **both apps** from the private [alpha.12 bootstrap release](https://github.com/tbhtho/MAC-DOW/releases/tag/v0.1.0-alpha.12) once; later updates use the public feed. The private Mac bootstrap saves its Mac-only diagnostic destination locally. Public updates contain no embedded webhook or other private credential. Mac diagnostics contain fixed event codes and counters, never screen pixels, input contents, pairing secrets, or transfer names and contents. Windows diagnostics stay local.

[Development and earlier releases](docs/development.md) · [Protocol](docs/protocol.md) · [Architecture](docs/design.md) · [Third-party notices](THIRD_PARTY_NOTICES.md) · [MIT license](LICENSE)
