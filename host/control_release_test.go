package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

var controlReleaseReasons = []string{
	"foreground-lost", "window-deactivated", "viewer-blur", "pointer-cancel",
	"video-stale", "native-heartbeat-stale", "tab-change", "monitor-change",
	"manual", "disconnected", "mac-permission", "mac-scope", "mac-watchdog",
	"shutdown", "files-open", "updates-open", "keyboard-unavailable",
	"stream-start", "display-change", "mac-paused",
}

func releaseInput(t *testing.T, kind, reason string) input {
	t.Helper()
	data, err := json.Marshal(map[string]any{"type": kind, "reason": reason})
	if err != nil {
		t.Fatal(err)
	}
	var event input
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestReleaseReasonsAreFiniteAndOnlyAcceptedOnRelease(t *testing.T) {
	for _, reason := range controlReleaseReasons {
		event := releaseInput(t, "release", reason)
		if !event.valid() {
			t.Fatalf("fixed release reason rejected: %q", reason)
		}
		data, _ := json.Marshal(event)
		var encoded map[string]any
		_ = json.Unmarshal(data, &encoded)
		if encoded["reason"] != reason {
			t.Fatalf("release reason lost on the native input boundary: %q", reason)
		}
	}
	for _, reason := range []string{"secret-token", "/Users/private-name", "192.168.1.2", "viewer-blur\nsecret", strings.Repeat("x", 4097)} {
		if releaseInput(t, "release", reason).valid() {
			t.Fatal("arbitrary release detail accepted")
		}
	}
	for _, kind := range []string{"lease", "key", "move", "button", "scroll"} {
		if releaseInput(t, kind, "viewer-blur").valid() {
			t.Fatalf("release reason accepted on %s input", kind)
		}
	}
	if !releaseInput(t, "release", "").valid() || !(input{Type: "lease"}).valid() {
		t.Fatal("legacy clients without release reasons must remain compatible")
	}
}

func TestAuthenticatedReleaseReasonReachesNativeWithoutEndingVideo(t *testing.T) {
	forwarded := make(chan input, 4)
	s := &server{token: strings.Repeat("r", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
		catalog: func() ([]window, error) { return nil, nil },
		consent: func(start) bool { return true },
		stream: func(_ func([]byte), _ func(string), events <-chan input) error {
			for event := range events {
				forwarded <- event
			}
			return nil
		},
	}
	c := connectFixture(t, s)
	if err := c.WriteJSON(start{Type: "start", FPS: 60, Width: 1280, Quality: 70}); err != nil {
		t.Fatal(err)
	}
	var ready struct{ Type string }
	if err := c.ReadJSON(&ready); err != nil || ready.Type != "ready" {
		t.Fatalf("stream readiness: %+v, %v", ready, err)
	}
	for _, message := range []map[string]any{{"type": "release", "reason": "video-stale"}, {"type": "lease", "active": false, "view": true}} {
		if err := c.WriteJSON(message); err != nil {
			t.Fatal(err)
		}
	}
	for index, want := range []string{"release", "lease"} {
		select {
		case event := <-forwarded:
			if event.Type != want {
				t.Fatalf("forwarded %q, expected %q", event.Type, want)
			}
			if index == 0 {
				data, _ := json.Marshal(event)
				if !strings.Contains(string(data), `"reason":"video-stale"`) {
					t.Fatal("validated reason did not reach the native input boundary")
				}
			}
		case <-time.After(time.Second):
			t.Fatal("valid release diagnostic disconnected the active session")
		}
	}
	if err := c.WriteJSON(map[string]any{"type": "release", "reason": "/Users/private-name secret-token"}); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, err := c.ReadMessage()
		if err == nil {
			continue // A preceding lease may already have queued its pong.
		}
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatal("invalid release detail did not close the authenticated session")
		}
		break
	}
	select {
	case event := <-forwarded:
		t.Fatalf("private detail reached the native process as %q", event.Type)
	default:
	}
}
