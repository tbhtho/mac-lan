<h1 align="center">mac-lan</h1>

<p align="center"><b>Your Mac desktop and your Windows desktop, one tab apart.</b><br>
View and control a Mac from a Windows PC on the same network, share files and the clipboard, and run commands on either one from the other.</p>

<p align="center">
  <img src="https://img.shields.io/badge/status-preview-orange.svg" alt="Status: preview">
  <img src="https://img.shields.io/badge/viewer-Windows_10%2F11_x64-blue.svg" alt="Viewer: Windows 10/11 x64">
  <img src="https://img.shields.io/badge/host-Apple_Silicon_macOS_14%2B-lightgrey.svg" alt="Host: Apple Silicon macOS 14+">
  <img src="https://img.shields.io/badge/network-LAN_only-8A2BE2.svg" alt="Network: LAN only">
  <img src="https://img.shields.io/badge/license-MIT-green.svg" alt="License: MIT">
</p>

<p align="center"><a href="https://github.com/tbhtho/mac-lan/releases/latest"><b>Download the latest preview</b></a></p>

## What it is

mac-lan puts a slim bar at the top of a Windows display with two tabs, **Windows** and **MacBook**. One click shows the Mac's live desktop full screen, with its sound, and your keyboard and mouse control it. Another click and you are back in Windows. Everything travels directly between the two computers over your own Wi-Fi or Ethernet: there is no account, no cloud relay and no port forwarding.

It is two apps: a Windows viewer and a menu-bar app on the Mac.

## Get started

1. **Windows 10/11 x64:** download and run **mac-lan-setup.exe**. It installs for your account without administrator access. The single portable **mac-lan.exe** works too. Both include .NET; the [Microsoft WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/) must be present, as it is on current Windows.
2. **Apple Silicon Mac, macOS 14 or later:** open **mac-lan-macos-arm64.dmg**, drag **mac-lan** to **Applications** and open it. It lives in the menu bar. The app is signed with its own certificate, not an Apple Developer ID, so the first time choose **Open Anyway** in System Settings → Privacy & Security. The first launch compiles its native parts with Apple's command-line tools, which must already be installed (`xcode-select --install`).
3. **Connect:** put both computers on the same network. On Windows the Mac appears as a card; choose **Connect**, check that the Mac ID matches the one in the Mac's menu, and approve the pairing number on the Mac once. After that they remember each other and connect by themselves.
4. **Allow access on the Mac:** use the menu's **Allow Screen Recording…** for video and **Allow Keyboard & Mouse…** for control, then restart sharing. Files and the clipboard work before either is granted.

> [!NOTE]
> Keep your OS protections on. mac-lan changes no firewall, router or security setting, and installs nothing else.

## Features

- **Two tabs, one bar.** A 32-pixel bar reserves its own space above maximized apps. It can auto-hide, and **Display** moves it and the Mac view between monitors. Windows apps on that display move aside while the Mac is shown and come back afterwards.
- **Full desktop.** Hardware H.264 at up to 1920 pixels wide with a 60 fps target, keeping the Mac's proportions.
- **Fit Mac to this display.** Turn on **Bar → Fit Mac to this display (no bars)** and, while you are viewing it, the Mac's desktop takes the exact shape of your Windows display, so the picture fills the screen with no bars and no stretching. The Mac's own screen shows the same desktop and returns to normal when you stop. Off by default; see Limits.
- **App Priority.** Put one Mac window in its own resizable Windows window instead of the whole desktop.
- **Control.** Selecting **MacBook** hands over keyboard and mouse. Ctrl and left Alt act as Command, the Windows key opens Spotlight, and Ctrl+Alt+Shift+Backspace always releases. Losing focus, switching tabs or a stale picture releases held keys.
- **Sound.** Mac audio plays on Windows while you watch, and the Mac's speakers are muted until you stop.
- **Clipboard.** Text copied on one computer pastes on the other (up to 256 KB).
- **Files.** Drag files or folders to the Mac; they arrive in **Downloads/mac-lan**. **Send File to Windows…** in the Mac menu goes the other way. Transfers are verified with SHA-256 and never overwrite.
- **Reconnects by itself.** After a drop, a sleep or an update, Windows finds the Mac again and resumes without a new approval.
- **Signed automatic updates.** Both apps update from this repository's releases through a signed feed, waiting until you are not viewing, controlling or transferring.

## Terminal and AI agents

Either computer can run commands on the other. It is off until you switch it on. The file, screenshot and folder tools need alpha.51 on both computers, the standby link alpha.52.

- **Windows → Mac:** **Terminal** in the top bar. Each line runs on the Mac in zsh with live output; `cd` changes folder, **Stop** ends a command, **Send files here…** copies files into the current Mac folder.
- **Mac → Windows:** **Remote Commands… → Open Remote Terminal on Windows** in the Mac menu.
- **AI agents:** mac-lan is a stdio [MCP](https://modelcontextprotocol.io) server, so a coding agent on one computer can read and edit a project, build, test and look at the screen on the other:

  ```
  claude mcp add mac-lan -- "C:\path\to\mac-lan.exe" --mcp
  claude mcp add mac-lan -- "/Applications/mac-lan.app/Contents/Helpers/MacDowHost" --mcp
  ```

  **Copy MCP setup command** in the Terminal window or the Remote Commands menu gives the exact line; other agents register the same program with `--mcp` as a stdio server. Agents that need a public HTTPS endpoint cannot connect; there is no relay.

| Tool | What it does on the other computer |
|---|---|
| `list_devices` | Names it: operating system, user, home folder, shell, whether it accepts commands |
| `execute_command`, `get_execution_status`, `cancel_execution`, `send_input` | Runs a shell command with live output and exit code; long commands are polled, stopped, or answered on standard input |
| `read_file`, `write_file`, `edit_file`, `list_directory` | Reads, creates and changes text files and lists folders without shell quoting |
| `take_screenshot` | Returns a picture of its main display |
| `transfer_file`, `deploy_update` | Copies files or folders either way; pushes an update and runs an install or build step |

| Switch (each computer has both, both start off) | What it allows |
|---|---|
| Let the other computer run commands here | Commands, file reads or writes and pictures of the screen on this computer, with your account's rights |
| Let AI agents and local tools use the link | Programs on this computer may use the link to the other one |

Commands that delete files, discard Git work, change system settings, need administrator rights, end programs or download and run scripts, any replacement of existing files, and every deployment are shown in a mac-lan prompt and run only if you approve. So that an agent can work on a project without a prompt for every edit, the prompt for replacing a file offers **Always allow** for that folder, and the list of such folders is in the Terminal window's access section (Windows) or the Remote Commands menu (Mac); the home folder or a whole disk cannot be added. A command prompt offers the same for that exact command until mac-lan restarts. Agents cannot flip a switch, add a folder or answer a prompt. Each computer keeps a `remote-audit.log` of what it was asked to do. The prompt list is a safety net, not a sandbox: an approved or ordinary command has your full rights.

**Standby link (optional, Mac).** Turn on **Remote Commands… → Standby link** on the Mac (or run `MacDowHost --standby on`) and a small mac-lan service starts at login and stays in the background. The PC learns it the next time the two connect, and from then on commands, files and agents keep working while the mac-lan app on the Mac is restarting, updating, closed, or failing to open, which is what lets the PC repair or update mac-lan on the Mac. It also works when mac-lan is not open on Windows: the MCP server then reaches the Mac through the standby link by itself, without approval prompts, so anything that needs approval is refused until mac-lan is open. The standby link serves only the paired PC on your local network, obeys the "run commands here" switch, and is removed by turning it off. **Forget remembered Macs** on Windows drops it there too.

A command that is running when the connection drops keeps going for two minutes and its output resumes on reconnect. A Mac does not go to sleep for being idle while a command runs on it. It is a command runner, not a terminal emulator: no full-screen programs, password prompts or colors. Agents can see the other screen but cannot click or type in its apps.

## Privacy and security

- Traffic is TLS 1.3 between the two computers on private addresses only. Windows pins the Mac's certificate fingerprint; the Mac remembers only a hash of each PC's secret.
- Pairing needs a physical approval on the Mac. **Forget Remembered PCs** (Mac) and **Forget remembered Macs** (Windows) undo it.
- Diagnostics on Windows stay on the PC in `%LOCALAPPDATA%\MAC-DOW\diagnostics`. Mac diagnostics contain fixed event codes and counters, never screen contents, input or file names.

## Limits

- Apple Silicon Macs only; one PC controls a Mac at a time.
- Without **Fit Mac to this display**, a Mac whose screen has a different shape from your monitor shows bars at the sides or top. The fit option uses a macOS display interface that Apple does not document, so a macOS update can break it; mac-lan then falls back to the normal desktop with bars. Windows may be rearranged on the Mac while it is on.
- No image or file clipboard, microphone, IME, multi-touch or stylus. Keys are sent by physical position, so use the same keyboard layout on both.
- The 60 fps figure is a target, not a measurement; actual smoothness depends on both computers and the network.
- This is a preview. Releases are checked with headless tests and on the author's two computers, not on a range of hardware.

## Building

Windows packaging needs the .NET 10 SDK, Go 1.24+, Node 22+ and Python 3:

```powershell
./scripts/test.ps1              # headless tests; starts no UI or capture
./scripts/package-windows.ps1   # artifacts/mac-lan.exe
./scripts/package-macos-app.ps1 # cross-compiled Mac app archive
```

On an Apple Silicon Mac with Go and Apple's command-line tools, `bash scripts/build-macos.sh` builds and self-tests the native parts. The Go code under `host` is shared: the Mac host, and a small helper bundled into the Windows app for discovery, updates and remote commands.

## License

[MIT](LICENSE). Third-party components and their licenses are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and shipped with the apps.
