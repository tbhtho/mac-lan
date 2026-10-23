package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestReportsExcludeArbitraryMessagesAndCredentials(t *testing.T) {
	r := NewReport("windows", "pairing-failed", "IOException", map[string]int{"hosts": 0, "token": 123, "clipboard": 99})
	b, _ := json.Marshal(r)
	for _, secret := range []string{"token", "clipboard", "fingerprint", "webhook", "username"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("unexpected field", secret)
		}
	}
	invalid := NewReport("windows", "paste token=abc https://discord.com/api/webhooks/secret", "secret value", nil)
	b, _ = json.Marshal(invalid)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "token=") {
		t.Fatal(string(b))
	}
}
func TestDeliveryRetriesAndDisablesMentions(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(body), "allowed_mentions") || !strings.Contains(string(body), `"parse":[]`) {
			t.Fatal("mentions not disabled")
		}
		if req.URL.Query().Get("wait") != "true" {
			t.Fatal("delivery must wait for server acknowledgement")
		}
		if calls == 1 {
			return nil, errors.New("offline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"123"}`)), Header: make(http.Header)}, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := deliver(ctx, client, "https://discord.com/api/webhooks/123456789012345678/abc", NewReport("windows", "startup", "ok", nil)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestEndpointMustBeDiscordWebhook(t *testing.T) {
	for _, url := range []string{"http://discord.com/api/webhooks/123/a", "https://example.com/api/webhooks/123/a", "https://discord.com.evil.test/api/webhooks/123/a", "https://discord.com/api/webhooks/123/a?x=secret"} {
		if validEndpoint(url) {
			t.Fatal("accepted", url)
		}
	}
}
func TestWindowsReportsNeverUpload(t *testing.T) {
	oldHook, oldTransport := Webhook, http.DefaultTransport
	defer func() { Webhook = oldHook; http.DefaultTransport = oldTransport }()
	Webhook = "https://discord.com/api/webhooks/123456789012345678/fixture"
	http.DefaultTransport = transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("Windows attempted remote diagnostics")
		return nil, errors.New("blocked")
	})
	if result := Record("windows", "startup", "ok", nil); result.Sent || result.Saved {
		t.Fatal("Windows must not enter Mac remote-report pipeline")
	}
}

func TestCompilerReportAllowsOnlyBoundedNumericLocations(t *testing.T) {
	r := NewReport("mac", "native-build-failed", "compile-type", map[string]int{"compilerLine": 167, "compilerColumn": 18, "compilerExit": 1, "compilerOutput": 99, "sourcePath": 3, "token": 1})
	if len(r.Counts) != 3 || r.Counts["compilerLine"] != 167 || r.Counts["compilerColumn"] != 18 || r.Counts["compilerExit"] != 1 {
		t.Fatalf("unexpected compiler fields: %+v", r.Counts)
	}
	invalid := NewReport("mac", "native-build-failed", "compile-failed", map[string]int{"compilerLine": 100001, "compilerColumn": -1, "compilerExit": 100001})
	if len(invalid.Counts) != 0 {
		t.Fatalf("unbounded compiler counters: %+v", invalid.Counts)
	}
}
