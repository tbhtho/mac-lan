//go:build nativeembed

package main

import (
	_ "embed"
	"encoding/base64"
)

// Release builds embed the Mac sources from files written by package-macos-preview.ps1,
// instead of passing them as linker flags that outgrew the Windows command-line limit.
// Only their SHA-256 values remain linker flags.
var (
	//go:embed nativeembed/capture.swift.gz
	embeddedCaptureSource []byte
	//go:embed nativeembed/menu.swift.gz
	embeddedMenuSource []byte
	//go:embed nativeembed/Info.plist
	embeddedMenuInfo []byte
)

func init() {
	NativeCaptureSourceGzip = base64.StdEncoding.EncodeToString(embeddedCaptureSource)
	NativeMenuSourceGzip = base64.StdEncoding.EncodeToString(embeddedMenuSource)
	NativeMenuInfoBase64 = base64.StdEncoding.EncodeToString(embeddedMenuInfo)
}
