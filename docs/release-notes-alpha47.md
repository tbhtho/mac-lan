# mac-lan 0.1.0-alpha.47

This corrective release restores the alpha.45 runtime implementation on Windows and Mac after reports of intermittent control and unusable scrolling with alpha.46.

All alpha.46 audio/video synchronization changes are reverted. The shared media-clock protocol, added video playout delay, and new audio scheduling/queue behavior are removed. Existing alpha.45 input, scrolling, capture, and playback behavior returns.

The version increases so existing installations can receive the restoration through their in-app updater. Settings, pairing identifiers, update trust, and the Mac signing identity are preserved. No new synchronization improvement or measured hardware performance is claimed.

This is a conservative restoration of the preceding implementation, not a diagnosed fix for the exact underlying regression. Native/headless checks do not establish live two-computer control or After Effects behavior.
