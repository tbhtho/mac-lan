package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
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
		if mode == "disconnect" {
			var e input
			dec := json.NewDecoder(os.Stdin)
			for dec.Decode(&e) == nil {
			}
			json.NewEncoder(os.Stderr).Encode(map[string]any{"type": "status", "paused": false, "message": "Windows disconnected; capture stopped"})
			os.Stdout.Close()
			time.Sleep(100 * time.Millisecond) // Supervisor must allow a clean helper exit.
			os.Exit(0)
		}
		json.NewEncoder(os.Stderr).Encode(map[string]any{"type": "status", "paused": true, "message": message})
		if mode == "secondary-pipe" {
			json.NewEncoder(os.Stderr).Encode(map[string]any{"type": "status", "paused": true, "message": "Native input mailbox overflow/EOF"})
		}
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestCaptureFailurePreservesFirstNativeReason(t *testing.T) {
	t.Setenv("MACDOW_CAPTURE_FIXTURE", "secondary-pipe")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	err = runCapture(helper, start{Width: 1600, FPS: 60, Quality: 70}, func([]byte) {}, func(string) {}, make(chan input))
	if err == nil || captureFailureCode(err) != "h264-unavailable.-12908" {
		t.Fatalf("secondary input pipe shutdown hid the original native error: %v", err)
	}
}

func TestCaptureDisconnectAllowsCleanNativeExit(t *testing.T) {
	t.Setenv("MACDOW_CAPTURE_FIXTURE", "disconnect")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan input)
	close(events)
	if err = runCapture(helper, start{Width: 1600, FPS: 60, Quality: 70}, func([]byte) {}, func(string) {}, events); err != nil {
		t.Fatalf("normal viewer disconnect must not become capture-failed: %v", err)
	}
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

func TestNativeControlStatusOnlyAllowsFixedLabelsAndBooleans(t *testing.T) {
	missing, ok := nativeControlStatus([]byte(`{"type":"controlStatus","code":"view-only","controlPermission":false,"active":false}`))
	if !ok || missing.Permission || missing.Active {
		t.Fatal("view-only must retain missing Accessibility permission")
	}
	for _, code := range []string{"view-only", "ready", "accessibility-missing", "scope-blocked", "lease-expired", "stale-input", "generation-revoked"} {
		line, _ := json.Marshal(map[string]any{"type": "controlStatus", "code": code, "controlPermission": true, "active": code == "ready", "message": "/Users/private-name secret-token 192.168.1.2", "x": 0.23})
		state, ok := nativeControlStatus(line)
		if !ok || state.Code != code || !state.Permission || state.Active != (code == "ready") {
			t.Fatalf("fixed state not retained: %+v, %v", state, ok)
		}
		encoded, _ := json.Marshal(state)
		if strings.Contains(string(encoded), "private-name") || strings.Contains(string(encoded), "secret-token") {
			t.Fatal("private detail entered control diagnostic")
		}
	}
	for _, line := range []string{
		`{"type":"controlStatus","code":"ready","controlPermission":1,"active":true}`,
		`{"type":"controlStatus","code":"ready","active":true}`,
		`{"type":"controlStatus","code":"private-name","controlPermission":true,"active":true}`,
		`{"type":"status","code":"ready","controlPermission":true,"active":true}`,
		strings.Repeat("x", 2049),
	} {
		if state, ok := nativeControlStatus([]byte(line)); ok {
			t.Fatalf("invalid control diagnostic accepted: %+v", state)
		}
	}
}

func TestNativeInputFailureStagesDistinguishLegacyEOFAndOverflow(t *testing.T) {
	for _, item := range []struct{ message, code string }{
		{"Native input mailbox overflow/EOF", "input-pipe"},
		{"Native input mailbox overflow", "input-overflow"},
		{"Native input message invalid", "input-invalid"},
		{"Windows disconnected; capture stopped", ""},
	} {
		if got := nativeFailureCode(item.message); got != item.code {
			t.Fatalf("%q: got %q want %q", item.message, got, item.code)
		}
	}
}
