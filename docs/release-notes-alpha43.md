# mac-lan 0.1.0-alpha.43

**Reconnect without walking to the Mac.** After you approve a Windows PC once, the Mac gives it a single-use reconnect key that lasts while sharing runs (up to 30 minutes between connections). If Wi-Fi drops, the Mac sleeps briefly or the connection stalls, Windows reconnects by itself, with no new approval on the Mac. The MacBook tab shows a **Reconnecting…** card, then **Disconnected** with **Reconnect** and **Back to Windows** if it can't. It no longer leaves a black screen covering the monitor. Reconnecting to the same Mac in the same run skips the "Is this your Mac?" check.

**Switch views without re-pairing.** Opening App Priority while the desktop streams now switches to that window. Closing App Priority keeps files and the clipboard connected.

**Automatic updates wait for you.** A ready update waits while Windows is connected, controlling or transferring files. It installs about a minute after the connection is idle. **Restart to update** still installs right away. Interrupted downloads resume, and leftover update copies and old backups are removed, keeping only what **Restore previous version** needs.

**Keyboard fixes on Windows:**
- Keys are no longer stuck or swallowed after locking Windows, Ctrl+Alt+Del or UAC prompts; these now release control.
- The keyboard hook runs on its own thread, so a busy window can't make Windows silently remove it.
- Character keys are sent by physical position, so the Mac's own input source decides the character (choose the same layout on both computers). The ISO key beside left Shift and Japanese keys now reach the Mac.
- AltGr acts as Option.
- Ctrl+Left/Right and Ctrl+Backspace/Delete move or delete by word.

Requires alpha.42 or alpha.41 with **Install updates automatically** on, or **Restart to update**. Reconnect needs alpha.43 on both computers. With an older Mac, Windows falls back to pairing.

These changes passed the headless test suite and an independent code review on Windows. The Mac launcher is cross-compiled and certificate-signed on Windows. Its AppKit menu and capture helper compile with the Mac's existing Apple command-line tools. The Swift changes, reconnecting, non-US layouts, AltGr, the lock-screen handling and update deferral have not been checked on real hardware and need two-computer testing. On Macs with ISO keyboards, the key left of 1 and the key beside left Shift may be swapped; please report it if so.
