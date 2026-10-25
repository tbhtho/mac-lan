# MacBook tab and Windows apps

Alpha.36 adds this Windows behavior, automatic MacBook-tab control, and the
video/audio/input stability fixes, plus optional top-bar auto-hide. These Windows features are also compatible
with the alpha.35 Mac host; the alpha.36 Mac package carries the matching release
identity without changing the native capture or menu sources.

Selecting MacBook temporarily moves ordinary Windows app windows off the display
used by the Mac picture. Another primary display is preferred, otherwise the
first other available display. Apps already on other displays stay put. With
only one display, no windows move. App Priority does not move Windows apps.

Selecting Windows returns moved windows to their saved positions and sizes.
Setup, disconnect, normal quit and display changes also attempt restoration.
Closed windows are skipped, and a new window reusing an old Windows handle never
gets that app's saved placement. New source-display windows are checked about
once per second while MacBook is open.

Windows are moved without activation or changing Z order. Normal bounds are
scaled for DPI and clamped to the destination work area. The adapter saves both
native placement and visible outer bounds, so normal/snapped geometry can return
without deliberately activating restored windows. A maximized window keeps its
current display state. Snap groups and app-specific docking relationships are
not recreated.

Already minimized apps stay untouched until the user restores them. If an app is
minimized after mac-lan moved it, restoration waits until it is unminimized while
mac-lan is still running. Shell/tool windows, other virtual desktops and mac-lan's
own windows are excluded. Apps or OS permissions may refuse movement; mac-lan does
not elevate privileges or bypass those restrictions. Saved placements are held
in memory, so a hard process/OS crash cannot restore them automatically.

Control starts on the focused MacBook tab as soon as stream approval, confirmed
Accessibility permission and fresh picture/native heartbeat permit it. Returning
to Windows releases input. Manual Stop remains stopped until MacBook is selected
again or control is explicitly resumed. Ctrl+Alt+Shift+Backspace releases control
and returns to Windows. App Priority retains click activation.

## Auto-hide top bar

Choose **Bar → Auto-hide top bar** to enable it. The default remains an
always-visible bar. The setting is stored separately from updater preferences in
the existing `%LOCALAPPDATA%/MAC-DOW` settings directory.

With auto-hide enabled, only a two-physical-pixel reveal edge is reserved. Moving
the pointer to the selected display's top edge reveals the full bar without
activating it. It hides about 600 ms after the pointer leaves, while menus,
button capture and keyboard navigation keep it accessible. Revealing the bar
overlays the apps/Mac picture instead of resizing their work area each time.
The Mac picture keeps a fixed thin inset so its full contents remain available.
Turning auto-hide off restores the normal 32-DIP reservation. App Priority still
suspends the bar entirely.

Preference writes are atomic. Missing or malformed preferences use the visible
bar; a save failure keeps the choice for the current session and reports a local
diagnostic. Normal close and App Priority suspension stop the edge polling timer.
Native reveal, DPI, focus and fullscreen interaction still need a desktop check.

## Verification boundary

`tests/Desktop.Tests` uses synthetic windows and exercises monitor selection,
restoration, DPI/negative-origin mapping, failure retry, new windows and handle
reuse. The Windows adapter compiles and was reviewed against Microsoft's
[placement coordinate rules](https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-windowplacement)
and [show-state behavior](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-showwindow).
No real desktop windows were moved by development checks. Physical multi-monitor,
maximized, mixed-DPI, app-specific and hotplug behavior still need the user's
[hardware checks](manual-validation.md).
