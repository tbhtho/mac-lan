package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClipboardRequiresPairingAndSharesTextBothWays(t *testing.T) {
	mac := "copied on Mac \u2019"
	s := &server{peerAllowed: func(string) bool { return true }, transfers: newTransferReceiver(t.TempDir()),
		clipboard: &clipboard{
			read:  func(context.Context) (string, error) { return mac, nil },
			write: func(_ context.Context, text string) error { mac = text; return nil },
		}}
	token, revoke, err := s.transfers.grant("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(s)
	defer ts.Close()
	client := ts.Client()
	request := func(method, body, bearer string) (*http.Response, map[string]any) {
		r, _ := http.NewRequest(method, ts.URL+"/clipboard", strings.NewReader(body))
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	if resp, _ := request(http.MethodGet, "", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatal("clipboard readable without the pairing token", resp.StatusCode)
	}
	if resp, _ := request(http.MethodGet, "", strings.Repeat("0", 64)); resp.StatusCode != http.StatusForbidden {
		t.Fatal("clipboard readable with a wrong token", resp.StatusCode)
	}
	if _, out := request(http.MethodGet, "", token); out["text"] != "copied on Mac \u2019" {
		t.Fatal("Mac text not returned", out)
	}
	if resp, _ := request(http.MethodPut, `{"text":"copied on Windows"}`, token); resp.StatusCode != http.StatusOK || mac != "copied on Windows" {
		t.Fatal("Windows text not written to the Mac", resp.StatusCode, mac)
	}
	if resp, _ := request(http.MethodPut, `{"text":"`+strings.Repeat("x", maxClipboard+1)+`"}`, token); resp.StatusCode != http.StatusBadRequest {
		t.Fatal("oversized clipboard accepted", resp.StatusCode)
	}
	revoke()
	if resp, _ := request(http.MethodGet, "", token); resp.StatusCode != http.StatusForbidden {
		t.Fatal("clipboard still shared after disconnect", resp.StatusCode)
	}
}
