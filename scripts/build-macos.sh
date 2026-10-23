#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin && "$(uname -m)" == arm64 ]] || { echo "Build on an Apple Silicon Mac; cross-compilation is not native validation." >&2; exit 1; }
xcode-select -p >/dev/null 2>&1 || { echo "Existing Xcode/Command Line Tools SDK required; nothing will be installed." >&2; exit 1; }
command -v swiftc >/dev/null || { echo "An existing Xcode/Command Line Tools Swift compiler is required. Nothing will be installed." >&2; exit 1; }
command -v go >/dev/null || { echo "An existing Go 1.24+ toolchain is required. Nothing will be installed." >&2; exit 1; }
export GOMAXPROCS=2
export CLANG_MODULE_CACHE_PATH="${CLANG_MODULE_CACHE_PATH:-$PWD/.cache/swift}"
export SWIFT_MODULECACHE_PATH="${SWIFT_MODULECACHE_PATH:-$CLANG_MODULE_CACHE_PATH}"
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.cache/go-mod}"
mkdir -p "$CLANG_MODULE_CACHE_PATH" "$GOCACHE" "$GOMODCACHE"
diagnostic_flags=()
if [[ "${MACDOW_PRIVATE_BOOTSTRAP:-0}" == 1 ]]; then
  diagnostic_destination="${MACDOW_DIAGNOSTICS_WEBHOOK:-}"
  if [[ -z "$diagnostic_destination" && -f .cache/discord-webhook.txt ]]; then diagnostic_destination="$(cat .cache/discord-webhook.txt)"; fi
  [[ "$diagnostic_destination" =~ ^https://discord\.com/api/webhooks/[0-9]{17,22}/[A-Za-z0-9_-]+$ ]] || { echo "Private diagnostic destination missing/invalid." >&2; exit 1; }
  diagnostic_flags=(-ldflags "-X github.com/tbhtho/MAC-DOW/host/internal/diagnostics.Webhook=$diagnostic_destination")
fi
app="$PWD/artifacts/macos-app-arm64/MAC-DOW Host.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Helpers" "$app/Contents/Resources" .cache/mac-app-build
swiftc -swift-version 5 -O -target arm64-apple-macos14.0 native/MacDowCapture.swift -o "$app/Contents/Helpers/MacDowCapture" -framework ApplicationServices -framework Foundation -framework AppKit -framework ScreenCaptureKit -framework CoreGraphics -framework CoreMedia -framework CoreImage -framework VideoToolbox -framework ImageIO -framework UniformTypeIdentifiers
(cd host && go test -p 1 ./... && go build -trimpath "${diagnostic_flags[@]}" -o "$app/Contents/Helpers/MacDowHost" .)
cp native/MacDowHostApp.swift .cache/mac-app-build/main.swift
swiftc -swift-version 5 -O -target arm64-apple-macos14.0 .cache/mac-app-build/main.swift native/HostUpdater.swift -o "$app/Contents/MacOS/MAC-DOW Host" -framework AppKit -framework Foundation -framework ApplicationServices -framework CoreGraphics
"$app/Contents/Helpers/MacDowCapture" --self-test
"$app/Contents/Helpers/MacDowHost" --self-test
"$app/Contents/MacOS/MAC-DOW Host" --self-test
cp README.md LICENSE THIRD_PARTY_NOTICES.md "$app/Contents/Resources/"
cp -R licenses "$app/Contents/Resources/"
git rev-parse HEAD > "$app/Contents/Resources/COMMIT.txt"
version="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' host/internal/updater/config.json)"
[[ -n "$version" ]] || { echo "Missing update build version" >&2; exit 1; }
numeric_version="${version%%-*}"
sed "s/@NUMERIC_VERSION@/$numeric_version/g" native/Info.plist > "$app/Contents/Info.plist"
plutil -lint "$app/Contents/Info.plist"
printf '{"product":"MAC-DOW","repositoryId":1430459188,"version":"%s","os":"darwin","arch":"arm64","layout":"app-bundle"}\n' "$version" > "$app/Contents/Resources/MAC-DOW-INSTALL.json"
# Ad-hoc signatures provide local bundle integrity only, not Developer ID/notarization.
codesign --force --sign - --timestamp=none "$app/Contents/Helpers/MacDowCapture"
codesign --force --sign - --timestamp=none "$app/Contents/Helpers/MacDowHost"
codesign --force --sign - --timestamp=none "$app"
codesign --verify --deep --strict "$app"
(cd artifacts/macos-app-arm64 && tar -czf ../MAC-DOW-macos-arm64-unsigned.tar.gz 'MAC-DOW Host.app')
ditto -c -k --sequesterRsrc --keepParent "$app" artifacts/MAC-DOW-macos-app.zip
echo "Native app bundle build + non-capturing self-tests passed. Double-click MAC-DOW Host.app; no Terminal or --listen needed. Ad-hoc signed, not Developer ID/notarized; real capture/control remain untested."
