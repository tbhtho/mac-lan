# Hardware acceptance checklist — NOT YET EXECUTED

The Mac is offline/not authorized. Development has not opened the Windows UI, installed hooks, captured either real desktop, injected input, requested permissions or changed firewall/router settings.

After native Apple Silicon compilation succeeds and the user explicitly grants the necessary Mac access:

- Run --check; missing Screen Recording must fail with instructions and no permission prompt. Missing Accessibility must remain view only.
- Compare fingerprint directly, use temporary token, deny ALLOW and verify no capture. Wrong fingerprint/token, browser Origin, public IP and second controller must fail.
- Test desktop tab on physical second display at 100%, 150%, 200% scale, mixed DPI, negative origins, monitor removal/reconnect and primary-only fallback. Check monitor selection and keep unrelated desktop windows usable.
- Switch Windows/Mac 100 times while holding Shift/Command/mouse buttons. Focus another Windows app, minimize, disconnect Wi-Fi, close host, kill client process. Verify all Mac held input releases within lease timeout and returning always needs explicit rearm.
- Confirm Alt+Tab switches Mac applications only while armed. Ctrl+Alt+Shift+Backspace must release immediately; Windows reserved/security shortcuts remain local.
- App Priority must hide the tab shell entirely; move/resize/maximize/minimize its normal Windows window on either monitor. Verify only the selected Mac window is visible. Close/minimize chosen Mac window: clear image, stop capture, no desktop fallback. Open a modal/menu/child window: no invisible interaction; pause control when scope check detects another foremost window. Validate coordinate mapping after moving/resizing Mac window.
- Check US-layout editing, repeats, modifiers, drag, scroll, double clicks; non-US layouts, IME, clipboard, stylus and audio are unsupported pending explicit implementation.
- Measure H264 VideoToolbox hardware state and Windows actual hardware decoder using runtime diagnostics. Hardware preference is not proof of hardware decode. Compare 960/1280/1600/1920 width, 10/15/30 fps, 2/4/8/12/20 Mbit/s over Wi-Fi. Record RSS, CPU/GPU, fps, RTT, encode and local decode/submit timing.
- Measure real key-to-photon latency with a camera or instrumented application. RTT + encode + local decode is not end-to-end latency; report P50/P95/P99. Test packet loss/congestion and recovery; no unbounded stale frames/input.
- Quit both apps; confirm no listeners/children, hooks, autostart entries, credentials or persisted screen images remain.

Do not treat CI compile/self-tests as this hardware validation.

