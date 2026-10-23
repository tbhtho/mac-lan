package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The subprocess is this Go test binary, never the native capture helper. No
// screen, permissions, listener, webhook or OS input APIs are exercised.
func TestMain(m *testing.M) {
	if mode := os.Getenv("MACDOW_CAPTURE_FIXTURE"); mode != "" && len(os.Args) > 1 && os.Args[1] == "--stream" {
		if strings.Join(os.Args[1:], " ") != "--stream 0 1600 60 70 h264 8" {
			fmt.Fprintln(os.Stderr, "unexpected capture helper arguments")
			os.Exit(3)
		}
		message := "Hardware H.264 unavailable (-12908). Reconnect with JPEG diagnostic; no silent software fallback."
		if mode == "setup" {
			message = "Capture setup failed: fixture error /Users/private-name secret-token 192.168.1.2"
		}
		json.NewEncoder(os.Stderr).Encode(map[string]any{"type": "status", "paused": true, "message": message})
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestCaptureFailurePreservesNativeReason(t *testing.T) {
	t.Setenv("MACDOW_CAPTURE_FIXTURE", "encoder")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = runCapture(helper, start{Width: 1600, FPS: 60, Quality: 70}, func([]byte) { t.Error("failure fixture sent a video frame") }, func(string) {}, make(chan input))
	if err == nil || !strings.Contains(err.Error(), "Hardware H.264 unavailable (-12908)") {
		t.Fatalf("native failure was replaced by a generic process exit: %v", err)
	}
	if code := captureFailureCode(err); code != "h264-unavailable.-12908" {
		t.Fatalf("diagnostic must identify the native failure stage: %q", code)
	}
}

func TestCaptureFailureDiagnosticExcludesNativePrivateDetails(t *testing.T) {
	t.Setenv("MACDOW_CAPTURE_FIXTURE", "setup")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = runCapture(helper, start{Width: 1600, FPS: 60, Quality: 70}, func([]byte) {}, func(string) {}, make(chan input))
	if err == nil || !strings.Contains(err.Error(), "/Users/private-name") {
		t.Fatalf("authenticated LAN error must retain the native setup reason: %v", err)
	}
	if code := captureFailureCode(err); code != "capture-setup" {
		t.Fatalf("remote diagnostic must contain only an allowlisted stage: %q", code)
	}
}

func TestNativeFailureStatusFiltersAndBounds(t *testing.T) {
	for _, message := range []string{"Control paused: Accessibility missing", "Video encode failed: -12908", "Native input mailbox overflow/EOF /Users/private-name"} {
		line, _ := json.Marshal(map[string]any{"type": "status", "paused": true, "message": message})
		got := nativeFailureStatus(line)
		if strings.HasPrefix(message, "Native input") {
			if got == nil || got.code != "input-pipe" {
				t.Fatal("terminal native message not classified")
			}
		} else if got != nil {
			t.Fatal("nonterminal pause must not become capture exit reason")
		}
	}
	line, _ := json.Marshal(map[string]any{"type": "status", "paused": true, "message": "Capture setup failed:\n" + strings.Repeat("x", 1500)})
	failure := nativeFailureStatus(line)
	if failure == nil || len(failure.reason) > 1027 || strings.Contains(failure.reason, "\n") {
		t.Fatal("native reason must have a bounded single-line local representation")
	}
	if nativeFailureStatus([]byte(strings.Repeat("x", 2049))) != nil || nativeFailureCode("Capture setup failed: private-name token") != "capture-setup" {
		t.Fatal("unbounded status or native detail entered remote code")
	}
}

func TestCaptureSetupNSErrorDiagnosticIsFrameworkAndIntegerOnly(t *testing.T) {
	for _, item := range []struct{ message, want string }{
		{`Capture setup failed: Error Domain=com.apple.ScreenCaptureKit.SCStreamErrorDomain Code=-3808 "private-name" UserInfo={path=/Users/private-name, token=secret-token, ip=192.168.1.2}`, "sck-setup.-3808"},
		{`Capture setup failed: Error Domain=NSOSStatusErrorDomain Code=-50 "private-name"`, "osstatus-setup.-50"},
		{`Capture setup failed: Error Domain=com.example.private-name Code=-3808 "secret-token"`, "capture-setup"},
		{`Capture setup failed: Error Domain=com.apple.ScreenCaptureKit.SCStreamErrorDomain.evil Code=-3808 "secret-token"`, "capture-setup"},
		{`Capture setup failed: Error Domain=com.apple.ScreenCaptureKit.SCStreamErrorDomain Code=-3808secret-token`, "capture-setup"},
		{`Capture setup failed: Error Domain=NSOSStatusErrorDomain Code=-9999999999 "private-name"`, "capture-setup"},
	} {
		line, _ := json.Marshal(map[string]any{"type": "status", "paused": true, "message": item.message})
		failure := nativeFailureStatus(line)
		if failure == nil || captureFailureCode(failure) != item.want {
			t.Fatalf("framework diagnostic: got %+v, want %q", failure, item.want)
		}
	}
}
