# mac-lan 0.1.0-alpha.45

**Approve your PC once.** After you allow a Windows PC on your Mac, the two now remember each other:
- The Mac keeps the same identity (Mac ID) across launches. It stores each remembered PC as a hash of that PC's secret, in a file only your account can read.
- Windows keeps the Mac's credential encrypted for your Windows account, and connects automatically whenever that Mac appears on the network.
- Restarting either app, updating, or reconnecting later no longer asks for approval on the Mac. Keyboard and mouse still start only from the MacBook tab.

**To undo it:** choose **Forget Remembered PCs** in the Mac's mac-lan menu while sharing, or **Forget remembered Macs** on the Windows Connection page. If the Mac forgets a PC, Windows notices on its next attempt and asks you to approve again. Choosing **Disconnect** stops automatic connection until you connect again or restart mac-lan.

**Updates no longer wait for an idle connection forever.** Since Windows now stays connected, automatic updates on both computers wait only while you are viewing, controlling or transferring files. An idle connection reconnects by itself after the update.

Your first connection after updating still needs one approval. Remembering needs alpha.45 on both computers; older versions keep asking each time.

These changes passed the headless test suite and a security review on Windows. The Mac launcher is cross-compiled and certificate-signed on Windows. Its AppKit menu and capture helper compile with the Mac's existing Apple command-line tools. The new Mac menu item, the saved Mac identity and automatic connection have not been checked on real hardware and need two-computer testing.
