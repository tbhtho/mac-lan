# mac-lan

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE) ![Windows x64 and Apple Silicon](https://img.shields.io/badge/platform-Windows_x64_%7C_Apple_Silicon-blue.svg)

Your Mac desktop and Windows desktop, one tab apart on the same LAN.

[**Download the latest preview**](https://github.com/tbhtho/mac-lan/releases) · [Validation status](docs/verification.md)

## Get started

1. On **Windows 10/11 x64**, download and run **mac-lan.exe**. It is one executable with its .NET runtime included. An existing [Microsoft WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/) is required.
2. On **Apple Silicon macOS 14+**, extract **mac-lan-macos-arm64-commandline-preview.tar.gz** and open **MacDowHost**. Keep its Terminal window open. This is a command-line preview; the menu-bar `.app` is not packaged yet. First launch builds the capture helper using your existing Apple command-line tools.
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

There is no audio, shared clipboard, IME, multi-touch, stylus, or non-US physical keyboard layout support. Capture performance and latency depend on the Mac, Windows decoder, and LAN; a 60 fps target is not a measured throughput guarantee. The user confirmed live video and input control in alpha.12, with video lag and intermittent control releases. Frame delivery changes still require the two-computer checks in the [hardware checklist](docs/manual-validation.md).

## Updates

Install **alpha.14 or later on both computers once** from this repository's [public releases](https://github.com/tbhtho/mac-lan/releases). Earlier builds used a withdrawn feed and cannot discover this update. Close both apps before replacing them; settings and the Mac's existing private diagnostic configuration stay in their current locations.

From alpha.14, both apps check for signed updates at startup and every six hours, download a newer preview automatically, and offer **Restart to update**. No GitHub sign-in is needed. An active connection is not interrupted; the Mac offers restart while idle. Windows update checks can be disabled in Updates. After restarting, pair and approve viewing again before activating control. Only the latest public build is advertised here; earlier private diagnostic bootstrap packages are not public downloads.

The public Mac package has no embedded diagnostic destination. Existing Mac-only diagnostic configuration is preserved locally. Mac diagnostics contain fixed event codes and counters, never screen pixels, input contents, pairing secrets, or transfer names and contents. Windows diagnostics stay local.

[Development and earlier releases](docs/development.md) · [Protocol](docs/protocol.md) · [Architecture](docs/design.md) · [Third-party notices](THIRD_PARTY_NOTICES.md) · [MIT license](LICENSE)
