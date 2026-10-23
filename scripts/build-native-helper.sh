#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
[[ "$(uname -s)" == Darwin && "$(uname -m)" == arm64 ]] || { echo "Requires Apple Silicon macOS 14+" >&2; exit 1; }
command -v swiftc >/dev/null || { echo "Existing Swift/Xcode Command Line Tools required; nothing installed." >&2; exit 1; }
swiftc -swift-version 5 -O -target arm64-apple-macos14.0 MacDowCapture.swift -o MacDowCapture -framework ApplicationServices -framework Foundation -framework AppKit -framework ScreenCaptureKit -framework CoreGraphics -framework CoreMedia -framework CoreImage -framework VideoToolbox -framework ImageIO -framework UniformTypeIdentifiers
chmod u+x MacDowHost
./MacDowCapture --self-test
echo "Native helper compiled; no capture/input started. Real Mac validation is still required."
