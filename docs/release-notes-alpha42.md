# mac-lan 0.1.0-alpha.42

Reliability fixes for both computers:

- A failed automatic update or rollback now reopens the installed mac-lan instead of leaving it closed. Brief file locks, such as an antivirus scan, are retried.
- **Restore previous version** on Windows now holds that update instead of reinstalling it seconds later. A newer release still installs; **Check now** in Updates resumes it.
- The Mac frees its session within about a minute when the Windows PC sleeps or leaves the network, instead of refusing reconnects for up to 5 minutes. While streaming, Windows ends the session after 8 seconds without any data from the Mac instead of showing a frozen picture.
- Brief Wi-Fi stalls no longer end the stream after about 4 seconds.
- A Mac connected to two networks (wired plus Wi-Fi, or a VPN adapter) stays in the Windows Connection list.
- Opening mac-lan again on Windows shows the running copy's Connection page instead of starting a second top bar.
- Finding nearby Macs no longer waits behind an update download.
- Faster video frame handling on Windows, with software H.264 decoding as a fallback after repeated hardware decoder errors.
- The Mac reads at most 256 KB from the clipboard per check, and the menu-bar app no longer quits if the host exits while it is writing to it.

Alpha.41 installs this automatically when **Install updates automatically** is on. Alpha.14–40 need **Restart to update** once, or the installers.

These changes passed the headless test suite on Windows. The Mac launcher is cross-compiled and certificate-signed on Windows. Its AppKit menu and capture helper compile with the Mac's existing Apple command-line tools. The Swift change, automatic Mac relaunch, and the new connection timeouts have not been checked on real hardware and need two-computer testing.
