# mac-lan 0.1.0-alpha.48

## Reconnecting

- After a connection attempt that cannot reach the Mac, Windows now looks the Mac up again and uses its current address and port. A restarted Mac (after an update, or Stop/Start Sharing) listens on a new port, and earlier versions kept retrying the old one from the MacBook and Windows tabs, so **Reconnect** failed until the Connection page was opened.
- Quitting while a connection attempt was still in progress no longer shows the "mac-lan stopped safely" message.

## Remote commands, terminal and MCP server (new, off by default)

- **Terminal** in the Windows top bar runs commands on the Mac with live output, `cd`, Stop and file copies. On the Mac, `MacDowHost --remote-shell` in Terminal does the same for the PC.
- `mac-lan.exe --mcp` and `MacDowHost --mcp` are stdio MCP servers for AI coding agents, with `list_devices`, `execute_command`, `get_execution_status`, `cancel_execution`, `transfer_file` and `deploy_update`.
- Each computer refuses commands and file access until its own user switches that on, and AI agents are a second, separate switch. Destructive or administrator commands, replacing files and deployments ask in mac-lan before they run. Requests are recorded in `remote-audit.log`.
- A command that is running when the connection drops keeps running for two minutes and its output resumes after reconnecting.

The Mac's two switches are set in Terminal in this version; a menu item follows once it has been compiled on a Mac. See [remote commands](remote-commands.md) for setup, the safety rules and limits.

## Validation and limits

The headless Windows suite passes, including new tests that join both ends over a pinned TLS connection on one computer, and `mac-lan.exe --mcp` was exercised against a real build. The Mac host is cross-compiled. No remote command has run on a Mac yet, the Windows Terminal window has not been opened on hardware, and the reconnect change has not been tried against a restarting Mac. Native capture, menu and input sources are unchanged from alpha.47.

The Mac app retains the existing signing identity. It is not Apple Developer ID signed or notarized.
