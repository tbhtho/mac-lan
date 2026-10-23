package main

import (
	"crypto/tls"
	"crypto/x509"
	"github.com/gorilla/websocket"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGuidedPairRequiresExplicitMacApproval(t *testing.T) {
	for _, approved := range []bool{false, true} {
		called := 0
		catalogCalls := 0
		s := &server{token: strings.Repeat("b", 64), slot: make(chan struct{}, 1), peerAllowed: func(string) bool { return true },
			pair: func(peer, code string) bool {
				called++
				if peer != "127.0.0.1" || code != "123456" {
					t.Fatal(peer, code)
				}
				return approved
			},
			catalog: func() ([]window, error) { catalogCalls++; return []window{{ID: 1, App: "Synthetic"}}, nil }, consent: func(start) bool { return false }}
		ts := httptest.NewTLSServer(s)
		roots := x509.NewCertPool()
		roots.AddCert(ts.Certificate())
		d := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: roots}}
		c, _, err := d.Dial("wss"+strings.TrimPrefix(ts.URL, "https"), nil)
		if err != nil {
			t.Fatal(err)
		}
		c.WriteJSON(map[string]string{"type": "pair", "code": "123456"})
		c.SetReadDeadline(time.Now().Add(time.Second))
		_, data, err := c.ReadMessage()
		if approved && (err != nil || !strings.Contains(string(data), "catalog")) {
			t.Fatal("approved catalog", err)
		}
		if !approved && (err == nil || catalogCalls != 0) {
			t.Fatal("unapproved catalog exposed")
		}
		if called != 1 {
			t.Fatal(called)
		}
		s.stopActive()
		c.Close()
		ts.Close()
	}
}
func TestMalformedPairNeverPrompts(t *testing.T) {
	for _, code := range []string{"", "123", "12345x", "1234567"} {
		s := &server{slot: make(chan struct{}, 1), peerAllowed: func(string) bool { return true }, pair: func(string, string) bool { t.Fatal("invalid prompt"); return true }}
		ts := httptest.NewTLSServer(s)
		roots := x509.NewCertPool()
		roots.AddCert(ts.Certificate())
		d := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: roots}}
		c, _, err := d.Dial("wss"+strings.TrimPrefix(ts.URL, "https"), nil)
		if err != nil {
			t.Fatal(err)
		}
		c.WriteJSON(map[string]string{"type": "pair", "code": code})
		c.SetReadDeadline(time.Now().Add(time.Second))
		if _, _, err = c.ReadMessage(); err == nil {
			t.Fatal("invalid pair accepted")
		}
		c.Close()
		ts.Close()
	}
}

func TestPairingApprovalAlsoAllowsViewing(t *testing.T) {
	s := &server{token: strings.Repeat("b", 64), slot: make(chan struct{}, 1), peerAllowed: func(string) bool { return true },
		pair:    func(string, string) bool { return true },
		catalog: func() ([]window, error) { return []window{}, nil },
		consent: func(start) bool { t.Error("viewing asked for a second approval after pairing"); return false },
		stream:  func(func([]byte), func(string), <-chan input) error { return nil }}
	ts := httptest.NewTLSServer(s)
	defer ts.Close()
	roots := x509.NewCertPool()
	roots.AddCert(ts.Certificate())
	d := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: roots}}
	c, _, err := d.Dial("wss"+strings.TrimPrefix(ts.URL, "https"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.WriteJSON(map[string]string{"type": "pair", "code": "123456"})
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, data, err := c.ReadMessage(); err != nil || !strings.Contains(string(data), `"keyframes":true`) {
		t.Fatal("catalog must advertise keyframe requests", string(data), err)
	}
	c.WriteJSON(map[string]any{"type": "start", "window": 0, "fps": 60, "width": 1920, "quality": 70, "codec": "h264", "bitrate": 8})
	if _, data, err := c.ReadMessage(); err != nil || !strings.Contains(string(data), "ready") {
		t.Fatal("approved pairing did not start viewing", string(data), err)
	}
	if !s.viewApproved("127.0.0.1") {
		t.Fatal("approval not remembered for this sharing session")
	}
}
