# Auto-hide top bar

The user requested a saved option like the Windows taskbar: hide the top bar and
reveal it at the display edge. Include it in the authorized alpha.36 release.

Default to the existing visible bar. `TabBarAutoHide` decides visibility from
edge/bar pointer presence, menu/capture/keyboard interaction, and a 600 ms leave
delay. `TabBar` polls cursor position only while docked and enabled, reserves a
stable two-physical-pixel edge, and overlays the full 32-DIP bar when revealed.
The Mac view uses a fixed thin inset while enabled, so reveal/hide does not resize
it. A **Bar → Auto-hide top bar** option and right-click menu expose the setting.
Atomic preference writes use a separate file under the legacy settings directory.

- [x] Red/green policy tests for visible default, delayed hide, edge reveal,
  interactions, timer cancellation, disabling and saved-enabled startup.
- [x] Red/green preference persistence and corruption fallback checks.
- [x] Native bar integration, menu, fixed work-area reservation, event wiring,
  close/suspend timer cleanup and current-user preference path.
- [x] Read-only review and final full suite. Inactive-bar focus clearing found
  during review was fixed and re-reviewed. Final source suite passes.
- [x] Signed alpha.36 packaging, eight anonymous downloads, feed signature and
  live updater verification. See `docs/verification.md` for exact identities.

No real desktop UI is opened or operated during development validation. Physical
edge reveal, mixed DPI, fullscreen and focus behavior need the user's test.
