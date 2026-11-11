# Remote commands, terminal and MCP server

First shipped in alpha.48. It lets either computer run shell commands and copy
files on the other one, from mac-lan's own terminal or from an AI agent connected through an
MCP server. Nothing here had run on a Mac when it shipped; see [What is checked](#what-is-checked).

## Using it

**Windows → Mac.** Choose **Terminal** in the top bar. Each line runs on the Mac as one
command in zsh (a login shell, so Homebrew and similar tools are on `PATH`). `cd` changes the
folder later commands run in, **Stop** or Ctrl+C ends a running command, and **Send files
here…** copies files from the PC into the Mac folder shown. Git and builds are ordinary
commands, for example `cd ~/Projects/app` then `git pull && swift build`.

**Mac → Windows.** Choose **Remote Commands… → Open Remote Terminal on Windows** in the Mac
menu (after alpha.48). Terminal opens with a line-based shell on the PC (cmd.exe; `:help` lists
file transfer). In alpha.48, run
`/Applications/mac-lan.app/Contents/Helpers/MacDowHost --remote-shell` in Terminal instead;
that needs the Mac's second switch below.

**Switches.** Two switches exist on each computer and both start off:

| Switch | Windows | Mac |
|---|---|---|
| Let the other computer run commands here | Terminal → Access → *Let my Mac run commands on this PC* | Remote Commands… → *Let the paired PC run commands on this Mac* |
| Let local tools and AI agents use the link | Terminal → Access → *Let AI agents and local tools…* | Remote Commands… → *Let local tools and AI agents use the link* |

The **Remote Commands…** menu holds both switches (after alpha.48). In alpha.48, or on a Mac
run without the menu-bar app, set them in Terminal, then stop and start sharing:

```
/Applications/mac-lan.app/Contents/Helpers/MacDowHost --remote-commands on
/Applications/mac-lan.app/Contents/Helpers/MacDowHost --remote-agents on
```

**AI agents.** mac-lan is a stdio MCP server when started with `--mcp`:

```
claude mcp add mac-lan -- "C:\path\to\mac-lan.exe" --mcp          (Windows)
claude mcp add mac-lan -- "/Applications/mac-lan.app/Contents/Helpers/MacDowHost" --mcp   (Mac)
```

**Copy MCP setup command** in the Windows Terminal window puts the right line for that PC on
the clipboard. Codex and other agents that start stdio MCP servers
use the same program and argument. Hosted assistants that need a public HTTPS MCP endpoint
cannot connect: mac-lan has no cloud relay.

Tools: `list_devices`, `execute_command`, `get_execution_status`, `cancel_execution`,
`transfer_file` (push or pull, files or folders) and `deploy_update` (push a file or folder,
replacing what is there, then optionally run a command in the destination). The tools act on
the other computer, never the one the agent runs on. mac-lan itself is still updated only
through its signed feed.

## Safety model

- **Pairing first.** The link is a second WebSocket, `/remote`, on the Mac's existing listener.
  Windows opens it with the pinned certificate fingerprint and the current pairing's bearer
  token, exactly like file transfer and the clipboard. It ends when that pairing ends.
- **Opt in per computer.** A computer refuses every command, file read and file write until
  its own user turns the first switch on. The check is made on the computer that would run
  the command, not only on the one asking.
- **Agents are a separate opt-in.** Local tools reach mac-lan through a loopback port with a
  token kept in a per-user private file (`remote-endpoint.json`). They are refused until the
  second switch is on, and they can never change a switch or answer an approval prompt: only
  mac-lan's own window can.
- **A person approves risky actions.** Commands that delete files, discard Git work, change
  system settings, need administrator rights, end programs, erase disks, shut down, or
  download and run scripts; replacing existing files; and every `deploy_update` are shown in a
  mac-lan prompt on the computer that sent them and run only if approved within a minute.
  With no mac-lan window to ask, they are refused. The receiving computer also refuses such a
  request unless it is marked approved. The pattern list is a safety net against mistakes,
  not a sandbox: an approved or unlisted command runs with the user's full rights.
- **Files.** A transfer never replaces an existing file unless asked to, which needs approval.
  Each file is written to a temporary name, verified with SHA-256 and then renamed. Links are
  skipped, and names sent by the other computer cannot leave the chosen destination.
- **Audit.** Each computer appends accepted and refused requests to `remote-audit.log` in
  mac-lan's private folder (`%LOCALAPPDATA%\MAC-DOW`, `~/Library/Application Support/MAC-DOW`).

## Reconnection

A running command keeps running for two minutes after its connection drops and buffers up to
1 MiB of output. When Windows reconnects (it does so by itself with the remembered pairing),
the sender reattaches by the command's random id and output continues from the byte offset it
had reached. If the link does not return, the command is ended and reported as `lost`.
Transfers in progress fail and can be repeated. Automatic updates wait while a command or
transfer runs in either direction.

## Limits

- Not a terminal emulator: no full-screen programs, no password prompts (so `sudo` needs
  `sudo -n` or a prior grant), no colors or cursor control.
- One command's output is kept up to 1 MiB; older output is dropped and reported as truncated.
- Up to 8 commands at once per computer, 30 minutes each by default, 12 hours at most.
- A folder transfer is limited to 5000 files; send an archive for more.
- Windows commands run in cmd.exe with code page 65001; `powershell` and `pwsh` can be chosen
  per command through the MCP `shell` argument.

## Wire format

JSON text messages with a type `t`. `hello` announces name, platform, version and whether
commands are accepted. `exec`, `attach`, `cancel`, `stdin`, `resolve`, `list`, `put`, `get`,
`chunk`, `end` and `abort` are requests; `reply` answers the request with the same `rid`;
`out` carries output with an absolute offset and `exit` the final state and code. File data
travels as base64 chunks of 192 KiB. Messages are limited to 4 MiB. Either side may send any
request; each serves them under its own switch.

## What is checked

On Windows, headlessly, with both ends in one process joined by a real pinned TLS WebSocket
on loopback (`go test ./internal/remote/` and `host/remote_host_test.go`): the switches,
streamed output and exit codes, approval and refusal on both ends, cancel and time limit,
a dropped connection during a running command, file and folder transfer in both directions,
refusal to overwrite, deployment, the loopback endpoint's separation of agents from the
window, and the MCP protocol. `mac-lan.exe --mcp` was run over pipes against a real build.
The Mac host cross-compiles for Apple Silicon.

On the user's two computers with alpha.48 (Windows 11, macOS 15.5 on an M3, 2026-11-11):
commands from the Windows Terminal window and from the loopback endpoint ran on the Mac in
zsh with separate stdout, stderr and exit codes; output arrived once a second while a command
was still running; Stop ended a process group of three processes; the time limit ended a
command; a 43 KB file was pushed and its SHA-256 matched on the Mac; an `rm -rf` sent as an
agent waited for approval and was withdrawn. The Mac menu item was compiled there with the
app's own compiler flags and passed the menu self-test.

Not checked: commands from the Mac to Windows, the Mac's approval dialog, the Remote Commands
choices and their Terminal `.command` launcher, a dropped connection during a command on real
hardware (covered on loopback only), folder transfers and deployments on hardware, and the
Windows approval prompt's appearance. The Terminal window opened behind the Mac view on the
MacBook tab in alpha.48; that is fixed in source. The Go race detector could not run here.
