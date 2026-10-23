#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin && "$(uname -m)" == arm64 ]] || { echo "Build on an Apple Silicon Mac; cross-compilation is not native validation." >&2; exit 1; }
xcode-select -p >/dev/null 2>&1 || { echo "Existing Apple command-line tools are required. Nothing will be installed." >&2; exit 1; }
command -v go >/dev/null || { echo "Existing Go 1.24+ is required." >&2; exit 1; }
command -v python3 >/dev/null || { echo "Existing Python 3 is required for packaging." >&2; exit 1; }
export GOMAXPROCS=2 CGO_ENABLED=0
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$PWD/.cache/go-mod}"
mkdir -p .cache/mac-app-build artifacts "$GOCACHE" "$GOMODCACHE"
compiler="$(xcrun --sdk macosx --find swiftc)"
sdk="$(xcrun --sdk macosx --show-sdk-path)"
module_cache="$PWD/.cache/mac-app-build/modules"
mkdir -p "$module_cache"
cat native/HostUpdater.swift > .cache/mac-app-build/main.swift
printf '\n' >> .cache/mac-app-build/main.swift
cat native/MacDowHostApp.swift >> .cache/mac-app-build/main.swift
"$compiler" -swift-version 5 -O -num-threads 2 -target arm64-apple-macos14.0 -sdk "$sdk" -module-cache-path "$module_cache" .cache/mac-app-build/main.swift -o .cache/mac-app-build/MacLanMenu -framework AppKit -framework Foundation -framework ApplicationServices -framework CoreGraphics
.cache/mac-app-build/MacLanMenu --self-test
"$compiler" -swift-version 5 -O -num-threads 2 -target arm64-apple-macos14.0 -sdk "$sdk" -module-cache-path "$module_cache" native/MacDowCapture.swift -o .cache/mac-app-build/MacDowCapture -framework ApplicationServices -framework Foundation -framework AppKit -framework ScreenCaptureKit -framework CoreGraphics -framework CoreMedia -framework CoreImage -framework VideoToolbox -framework ImageIO -framework UniformTypeIdentifiers
.cache/mac-app-build/MacDowCapture --self-test
capture="$(gzip -c native/MacDowCapture.swift | base64 | tr -d '\n')"
capture_hash="$(shasum -a 256 native/MacDowCapture.swift | awk '{print $1}')"
menu="$(gzip -c .cache/mac-app-build/main.swift | base64 | tr -d '\n')"
menu_hash="$(shasum -a 256 .cache/mac-app-build/main.swift | awk '{print $1}')"
info="$(base64 < native/Info.plist | tr -d '\n')"
flags="-s -w -X main.NativeCaptureSourceGzip=$capture -X main.NativeCaptureSourceSHA256=$capture_hash -X main.NativeMenuSourceGzip=$menu -X main.NativeMenuSourceSHA256=$menu_hash -X main.NativeMenuInfoBase64=$info"
public=true
if [[ "${MACDOW_PRIVATE_BOOTSTRAP:-0}" == 1 ]]; then
  destination="${MACDOW_DIAGNOSTICS_WEBHOOK:-}"
  if [[ -z "$destination" && -f .cache/discord-webhook.txt ]]; then destination="$(cat .cache/discord-webhook.txt)"; fi
  [[ "$destination" =~ ^https://discord\.com/api/webhooks/[0-9]{17,22}/[A-Za-z0-9_-]+$ ]] || { echo "Private diagnostic configuration is invalid." >&2; exit 1; }
  flags+=" -X github.com/tbhtho/MAC-DOW/host/internal/diagnostics.Webhook=$destination"
  public=false
fi
build_root="$(mktemp -d "$PWD/artifacts/macos-menu-native-XXXXXX")"
app="$build_root/mac-lan.app"
resources="$app/Contents/Resources"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Helpers" "$resources"
(cd host && go test -p 1 ./... && go build -trimpath -ldflags "$flags" -o "$app/Contents/Helpers/MacDowHost" .)
cp "$app/Contents/Helpers/MacDowHost" "$app/Contents/MacOS/mac-lan"
"$app/Contents/Helpers/MacDowHost" --self-test
cp README.md LICENSE THIRD_PARTY_NOTICES.md "$resources/"
cp -R licenses "$resources/"
commit="$(git rev-parse HEAD)"
dirty=false
[[ -z "$(git status --porcelain)" ]] || dirty=true
printf '%s' "$commit" > "$resources/COMMIT.txt"
version="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' host/internal/updater/config.json)"
[[ -n "$version" ]] || { echo "Missing update build version" >&2; exit 1; }
sed "s/@NUMERIC_VERSION@/${version%%-*}/g" native/Info.plist > "$app/Contents/Info.plist"
plutil -lint "$app/Contents/Info.plist"
printf '{"product":"MAC-DOW","repositoryId":1430459188,"version":"%s","commit":"%s","os":"darwin","arch":"arm64","layout":"app-bundle"}\n' "$version" "$commit" > "$resources/MAC-DOW-INSTALL.json"
printf '{"hostCommit":"%s","dirty":%s,"publicUpdate":%s,"captureSourceSha256":"%s","menuSourceSha256":"%s","host":"native arm64 Go build","nativeMenuSelfTest":true,"nativeCaptureSelfTest":true,"realCapture":"unverified","appBundle":"Go launcher with first-launch cached AppKit"}\n' "$commit" "$dirty" "$public" "$capture_hash" "$menu_hash" > "$resources/BUILD-PROVENANCE.json"
# Private bootstrap archives stay separate; no OS protection is changed.
output="$PWD/artifacts/update-public"
if [[ "$public" == false ]]; then output="$PWD/artifacts/private-bootstrap"; fi
mkdir -p "$output"
python3 scripts/package-macos-app.py "$app" "$output/mac-lan-macos-arm64.tar.gz"
echo "Native build and non-capturing self-tests passed. Open mac-lan.app; no Terminal is required. Unsigned/unnotarized; actual sharing remains untested."
