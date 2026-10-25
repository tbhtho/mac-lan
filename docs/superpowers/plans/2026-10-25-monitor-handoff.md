# Windows monitor handoff

The user requested that Windows apps occupying the MacBook tab's display move
to the available monitor while MacBook is open, then return when Windows is
selected. This extends the automatic-control work and preserves its fixes.

Use a pure `DesktopWorkspace` controller with an injected native window adapter.
It saves only windows successfully moved, leaves apps on other monitors alone,
chooses another primary monitor when available, maps normal bounds between work
areas with DPI scaling/clamping, and restores original placement records.
The Win32 adapter excludes mac-lan, shell surfaces, cloaked windows and tool
windows; it stores placements in memory and uses a private window property to
avoid restoring a closed/reused HWND. Move requests must avoid activating apps
or blocking on an unresponsive foreign UI. Restore uses WINDOWPLACEMENT's own
coordinate system and preserves normal/minimized/maximized state.

Activate at full-desktop positioning, detect new windows with a bounded periodic
refresh, and restore on Windows/setup tab, disconnect, quit and display changes.
App Priority does not relocate Windows apps. One available display is a no-op;
failed/blocked window moves do not stop streaming or escalate permissions.

- [x] Red/green controller tests: source-only selection, restoration/idempotence,
  new windows, smaller/negative-origin/mixed-DPI layouts, source changes, single
  monitor, refused moves, closed windows, restore retry and untouched app state.
- [x] Native adapter and full-desktop lifecycle wiring. Review fixes preserve
  focus, leave minimized apps alone and validate window identity before reuse.
- [x] Full headless suite, code review, doc links and a separate packaged smoke.
  Final evidence and candidate identity are in `docs/verification.md`.

Tests use a synthetic window adapter and never reposition real desktop windows.
Physical positioning, app-specific refusal, monitor hot-plug and maximized/DPI
behavior need the user's desktop acceptance check. In-memory snapshots cannot
restore after a hard process/OS crash.
