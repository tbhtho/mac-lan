package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrivateLANOnly(t *testing.T) {
	for _, s := range []string{"10.1.2.3", "172.16.1.1", "192.168.1.5"} {
		if !privateIP(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"0.0.0.0", "127.0.0.1", "8.8.8.8", "localhost", "::1", "172.32.0.1", "192.168.1.1:443"} {
		if privateIP(s) {
			t.Fatal(s)
		}
	}
}
func TestFreshTLSIdentity(t *testing.T) {
	a, fp, err := identity()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := identity()
	sum := sha256.Sum256(a.Certificate[0])
	if fp != hex.EncodeToString(sum[:]) {
		t.Fatal("fingerprint")
	}
	if bytes.Equal(a.Certificate[0], b.Certificate[0]) {
		t.Fatal("reused identity")
	}
	cert, err := x509.ParseCertificate(a.Certificate[0])
	if err != nil || cert.NotAfter.Before(time.Now()) {
		t.Fatal("invalid cert")
	}
}
func TestInputValidation(t *testing.T) {
	for _, m := range []input{{Type: "lease", Active: true}, {Type: "release"}, {Type: "key", Code: 55, Down: true}, {Type: "move", X: .5, Y: .5}, {Type: "button", Button: 0, Down: true}, {Type: "scroll", Delta: -120}} {
		if !m.valid() {
			t.Fatalf("rejected %+v", m)
		}
	}
	for _, m := range []input{{Type: "shell"}, {Type: "key", Code: 65536}, {Type: "move", X: 1.5}, {Type: "button", Button: 3}, {Type: "scroll", Delta: 100000}} {
		if m.valid() {
			t.Fatalf("accepted %+v", m)
		}
	}
}
func TestAuthenticatedConsentAndFrames(t *testing.T) {
	consent := 0
	s := &server{token: strings.Repeat("a", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
		catalog: func() ([]window, error) { return []window{{ID: 42, Title: "Editor", App: "Example"}}, nil },
		consent: func(start) bool { consent++; return true },
		stream: func(send func([]byte), status func(string), events <-chan input) error {
			send([]byte{0xff, 0xd8, 0xff, 0xd9})
			for e := range events {
				if e.Type == "release" {
					break
				}
			}
			return nil
		},
	}
	ts := httptest.NewTLSServer(s)
	defer ts.Close()
	d := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()}}
	d.TLSClientConfig.RootCAs.AddCert(ts.Certificate())
	connect := func() *websocket.Conn {
		c, _, err := d.Dial("wss"+strings.TrimPrefix(ts.URL, "https"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := connect()
	c.WriteJSON(map[string]string{"type": "auth", "token": "wrong"})
	_, _, err := c.ReadMessage()
	c.Close()
	if err == nil || consent != 0 {
		t.Fatal("unauthenticated access")
	}
	c = connect()
	defer c.Close()
	c.WriteJSON(map[string]string{"type": "auth", "token": s.token})
	_, catalog, err := c.ReadMessage()
	if err != nil || !bytes.Contains(catalog, []byte("Editor")) {
		t.Fatal("catalog", err)
	}
	c.WriteJSON(start{Type: "start", Window: 42, FPS: 10, Width: 1280, Quality: 70})
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	gotFrame := false
	for i := 0; i < 4; i++ {
		kind, data, err := c.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.BinaryMessage {
			gotFrame = bytes.Equal(data, []byte{0xff, 0xd8, 0xff, 0xd9})
			break
		}
	}
	if !gotFrame || consent != 1 {
		t.Fatal("missing authenticated consented frame")
	}
	c.WriteJSON(input{Type: "release"})
}
func TestNoBrowserOrigins(t *testing.T) {
	s := &server{token: "x", peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1)}
	ts := httptest.NewServer(s)
	defer ts.Close()
	d := websocket.Dialer{}
	h := mapHeader("https://evil.example")
	c, resp, err := d.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), h)
	if c != nil {
		c.Close()
	}
	if err == nil || resp.StatusCode != 403 {
		t.Fatal("origin accepted")
	}
}
func mapHeader(origin string) map[string][]string { return map[string][]string{"Origin": {origin}} }
func TestStartBoundsAndClosedWindow(t *testing.T) {
	ws := []window{{ID: 42}}
	for _, s := range []start{{Type: "start", FPS: 15, Width: 1600, Quality: 70}, {Type: "start", Window: 42, FPS: 5, Width: 800, Quality: 40}} {
		if !s.valid(ws) {
			t.Fatal(s)
		}
	}
	for _, s := range []start{{Type: "start", Window: 99, FPS: 15, Width: 1600, Quality: 70}, {Type: "start", FPS: 120, Width: 1600, Quality: 70}, {Type: "start", FPS: 15, Width: 8000, Quality: 70}} {
		if s.valid(ws) {
			b, _ := json.Marshal(s)
			t.Fatal(string(b))
		}
	}
}
