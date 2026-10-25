# Hardware acceptance checklist

## Alpha.36 Windows checks

- Enable **Bar → Auto-hide top bar**: it should tuck away after leaving, reveal
  at the selected display's top edge, and stay open for its menu and keyboard
  navigation. Disable it to restore the full reserved bar. Restart to check the
  saved choice. Test Windows/MacBook tabs, both displays/DPI scales, Escape and
  App Priority suspend/return without stealing focus from another application.
- Select MacBook with approved sharing, Accessibility and fresh video: keyboard
  and mouse must work without another desktop activation click. Missing permission
  or stale video must still block input. Returning to Windows and the emergency
  shortcut release held input; manual Stop must stay stopped across timer ticks.
- With two monitors, put normal and maximized Windows apps on the MacBook display.
  Select MacBook: those apps must move to the other display without taking focus.
  Apps already on the other display must keep their positions. Select Windows:
  moved windows must return to their saved positions and sizes without waking a
  minimized app. Test actual snapped geometry; Snap groups are not recreated.
- Minimized apps before switching must remain untouched. Restore one while MacBook
  is open and verify relocation; minimize a moved app and verify its return after
  unminimizing while mac-lan is still running.
- Repeat switches, open/close source-display apps, change the selected monitor,
  disconnect, and quit normally. Check restoration, different DPI, negative origins
  and monitor unplug/replug. A one-monitor setup must not move other apps.
- Open FluxFrame on each computer. Record video recovery/control behavior and the
  Windows disconnect log if streaming ends. Headless tests do not prove this run.

## Earlier acceptance notes

The older click-activation and stretched full-desktop expectations below describe
earlier previews. The current candidate checks above and current README supersede
those particular expectations; the remaining platform checks still matter.

The user reported successful native compilation, permission checks, pairing and live video in alpha.10. Actual delivered fps was not measured, and input was not armed because the user clicked directly into the view-only desktop. Alpha.11 adds click activation, a single local cursor and a filled 1080p stream; those changes still require Mac validation. Development checks remain headless and have not captured a real screen, installed input hooks, injected input or changed permissions/firewall/router settings. Current Accessibility permission is unknown.

After native Apple Silicon compilation succeeds and the user explicitly grants the necessary Mac access:

- Run --check; missing Screen Recording must fail with instructions and no permission prompt. Missing Accessibility must remain view only.
- Compare fingerprint directly, use temporary token, deny ALLOW and verify no capture. Wrong fingerprint/token, browser Origin, public IP and second controller must fail.
- Test desktop tab on physical second display at 100%, 150%, 200% scale, mixed DPI, negative origins, monitor removal/reconnect and primary-only fallback. Check monitor selection and keep unrelated desktop windows usable.
- First left click inside the fresh Mac picture must activate control without clicking the Mac. Subsequent clicks, motion and typing must route; a canceled/blurred activation must release control. App Priority letterbox clicks must not activate. Verify clear Accessibility guidance, grant detection and deliberate reactivation if permission is missing.
- Verify the full desktop stream is 1920×1080 by default, fills the available tab area without layout bars, and keeps the full menu bar/Dock visible. Different aspect ratios intentionally stretch. Confirm pointer targets at edges/center match the picture. Only the Windows pointer should appear in the stream; the physical Mac cursor should remain unchanged. App Priority must retain aspect fit.
- Switch Windows/Mac 100 times while holding Shift/Command/mouse buttons. Focus another Windows app, minimize, disconnect Wi-Fi, close host, kill client process. Verify all Mac held input releases within lease timeout and returning always needs explicit rearm.
- Confirm Alt+Tab switches Mac applications only while armed. While armed, the Windows key, Win+R/Win+D/Win+E, Ctrl+Esc, Menu and Print Screen must not open anything on Windows, and the Windows key must act as Option on the Mac. Hold the Windows key, release control, then let go: Start must not open. Ctrl+Alt+Shift+Backspace must release immediately; Ctrl+Alt+Delete must reach Windows and release control.
- In the Mac menu, a granted Screen Recording or Accessibility item must show a check and be inactive; a missing one must be clickable and open System Settings. Refresh Permissions appears only while something is missing.
- On the Mac, double-click mac-lan-macos-arm64.dmg: it must mount with a fixed window, large icons and the drag-to-Applications background; the copied app must not be reported as damaged (an unverified-developer prompt with Open Anyway is expected), must open, and must keep working permissions. Run `codesign --verify --deep --strict --verbose=2 /Applications/mac-lan.app`, `codesign -d -r- /Applications/mac-lan.app` (expect the certificate-leaf requirement) and `spctl -a -vv /Applications/mac-lan.app` and record the output. After granting Screen Recording and Accessibility once on alpha.20, update to the next release in the app: both grants must still apply without removing and re-adding mac-lan. On Windows, mac-lan-setup.exe must install without administrator access, open from Start, update in place, and uninstall from Settings → Apps while keeping settings.
- With mac-lan open on the Mac, the Windows setup page must list it as a card without any button press, keep the selection across refreshes, and connect on Connect or double-click. App Priority must open with the Mac app name as its title, no inner toolbar, and a size matching the Mac window shape.
- On Windows 11 22H2+, the tab bar, setup, Files and Updates windows must show a dark translucent backdrop with readable text; on Windows 10 they must show the same dark palette on a solid background.
- App Priority must hide the tab shell entirely; move/resize/maximize/minimize its normal Windows window on either monitor. Verify only the selected Mac window is visible. Close/minimize chosen Mac window: clear image, stop capture, no desktop fallback. Open a modal/menu/child window: no invisible interaction; pause control when scope check detects another foremost window. Validate coordinate mapping after moving/resizing Mac window.
- Check US-layout editing, repeats, modifiers, drag, scroll, double clicks; non-US layouts, IME, clipboard, stylus and audio are unsupported pending explicit implementation.
- Measure H264 VideoToolbox hardware state and Windows actual hardware decoder using runtime diagnostics. Hardware preference is not proof of hardware decode. Compare 960/1280/1600/1920 width at required 60 fps, 2/4/8/12/20 Mbit/s over Wi-Fi. Record RSS, CPU/GPU, fps, RTT, encode and local decode/submit timing.
- Measure real key-to-photon latency with a camera or instrumented application. RTT + encode + local decode is not end-to-end latency; report P50/P95/P99. Test packet loss/congestion and recovery; no unbounded stale frames/input.
- Quit both apps; confirm no listeners/children, hooks, autostart entries, credentials or persisted screen images remain.

Do not treat CI compile/self-tests as this hardware validation.
