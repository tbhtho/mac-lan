# mac-lan 0.1.0-alpha.41

Automatic updates now install and reopen mac-lan without a restart confirmation. Both apps check at launch, every 5 seconds for the first 30 seconds, then every 30 seconds. Downloads keep signature, hash and source checks. Installation releases control, cancels active transfers cleanly and briefly disconnects sharing; reconnect and approve viewing afterward. Disable **Install updates automatically** to keep installation manual. Network/feed propagation and download time affect arrival.

Use **Display** beside the top tabs to move mac-lan between **Left / Right** monitors or **Top / Bottom** stacked screens. Larger layouts get numbered choices; settings and hotplug stay synchronized. One-monitor setups disable the selector.

Alpha.14–40 need one last manual **Restart to update**, or the installers, to receive this behavior. The alpha.40 After Effects shortcuts and mouse-wheel changes remain included, with Windows-key tap still opening Spotlight.

The Mac launcher is cross-compiled and certificate-signed on Windows. Its AppKit menu and capture helper compile using the Mac's existing Apple command-line tools. Native compilation, automatic Mac relaunch and physical monitor switching remain unverified and need two-computer testing.
