# mac-lan 0.1.0-alpha.40

Use Ctrl+Space from Windows to send Mac Control+Space for FX Console in After Effects. Tapping either Windows key still opens Spotlight. Ctrl+C/V and Ctrl+Shift+Space keep their existing mappings.

Forwarded scrolling now identifies as discrete mouse-wheel input instead of continuous trackpad scrolling, preserving its distance, modifiers and pointer location. This does not install a virtual mouse or change macOS or After Effects settings. After Effects zoom behavior still depends on its preferences.

Update both computers to test both changes. Mac-to-Windows file transfers and the alpha.38 input/cursor fixes are included.

The Mac launcher is cross-compiled and certificate-signed on Windows. The updated native capture helper compiles on the Mac using its existing Apple command-line tools. Native compilation, wheel-event self-tests and interactive After Effects/FX Console behavior for these changes remain unverified; this preview is for the user's two-computer testing.
