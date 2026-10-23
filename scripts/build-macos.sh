#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin && "$(uname -m)" == arm64 ]] || { echo "Build on an Apple Silicon Mac; cross-compilation is not native validation." >&2; exit 1; }
command -v swiftc >/dev/null || { echo "An existing Xcode/Command Line Tools Swift compiler is required. Nothing will be installed." >&2; exit 1; }
command -v go >/dev/null || { echo "An existing Go 1.24+ toolchain is required. Nothing will be installed." >&2; exit 1; }
export GOMAXPROCS=2
mkdir -p artifacts/macos-arm64
swiftc -swift-version 5 -O -target arm64-apple-macos14.0 native/MacDowCapture.swift -o artifacts/macos-arm64/MacDowCapture -framework ApplicationServices -framework Foundation -framework AppKit -framework ScreenCaptureKit -framework CoreGraphics -framework CoreMedia -framework CoreImage -framework VideoToolbox -framework ImageIO -framework UniformTypeIdentifiers
(cd host && go test -p 1 ./... && go build -trimpath -o ../artifacts/macos-arm64/MacDowHost .)
artifacts/macos-arm64/MacDowCapture --self-test
artifacts/macos-arm64/MacDowHost --self-test
cp README.md LICENSE THIRD_PARTY_NOTICES.md artifacts/macos-arm64/
cp -R licenses artifacts/macos-arm64/
if command -v git >/dev/null && git rev-parse HEAD >/dev/null 2>&1; then git rev-parse HEAD > artifacts/macos-arm64/COMMIT.txt; fi
(cd artifacts && tar -czf MAC-DOW-macos-arm64-unsigned.tar.gz macos-arm64)
echo "Native build + non-capturing self-tests passed. This is unsigned/not notarized; real capture/control remain untested."

