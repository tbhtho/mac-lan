# Third-party components

- Gorilla WebSocket v1.5.3: BSD-2-Clause. Full notice in licenses/gorilla-websocket.txt.
- Microsoft.Web.WebView2 SDK 1.0.4258.31: BSD-3-Clause-style Microsoft SDK license. Full SDK license in licenses/webview2-LICENSE.txt. The runtime is a separate Microsoft prerequisite; MAC-DOW does not install or bundle it.
- .NET 10 runtime: MIT and other third-party notices; self-contained Windows package includes runtime license/notice files from the SDK.
- Go standard library/runtime: BSD-style Go license, included in licenses/go-LICENSE.txt.
- Apple ScreenCaptureKit, VideoToolbox, AppKit and system frameworks are linked from the OS and are not redistributed. Requires macOS 14+ on Apple Silicon.
- H.264 uses OS-provided codec implementations. No FFmpeg or external codec binary is bundled. This project makes no separate patent-license grant or codec licensing assurance.
- MAC-DOW source: MIT, LICENSE.

Technology references:
- https://developer.apple.com/documentation/screencapturekit
- https://developer.apple.com/documentation/videotoolbox/encoding-video-for-low-latency-conferencing
- https://www.w3.org/TR/webcodecs/
- https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution
- https://github.com/gorilla/websocket
- https://docs.github.com/en/actions/reference/runners/github-hosted-runners
