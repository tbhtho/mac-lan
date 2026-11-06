# mac-lan 0.1.0-alpha.44

**Windows apps no longer stay stuck behind the Mac picture.** When you select MacBook, mac-lan moves Windows apps off that display. It skipped windows without a standard title bar or sizing frame, so frameless apps such as the League of Legends client stayed behind the Mac picture and kept taking focus, releasing control. App windows now follow the same rule as Alt+Tab. Tool windows, overlays and menus still stay put. An app that moves itself back onto the MacBook display is moved off again within about a second, and selecting Windows still returns it to its original position.

Also fixed: a crash when mac-lan quit while a session was ending.

Alpha.41–43 install this automatically when **Install updates automatically** is on. Updates wait until the connection is idle in alpha.43, so disconnect or choose **Restart to update**.

These changes passed the headless test suite on Windows. The exact window that stayed behind was identified on the user's PC with a read-only window listing; the move itself still needs a check on that PC.
