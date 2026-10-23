package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Video setup and window enumeration can fail independently of pairing/files.
// Exercise both through real loopback TLS connections, without native capture.
func TestFileOnlyPairingCanTransferAfterRefusedVideoAndRevokesOnDisconnect(t *testing.T) {
	for _, catalogFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "native-build-failed", true: "window-list-failed"}[catalogFailure], func(t *testing.T) {
			var nativeCalls atomic.Int32
			receiver := newTransferReceiver(filepath.Join(t.TempDir(), "received"))
			s := &server{token: strings.Repeat("d", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1), transfers: receiver,
				videoError: "Native video setup failed. File transfers are available.",
				catalog: func() ([]window, error) {
					if catalogFailure {
						return nil, errors.New("synthetic enumeration failure")
					}
					return nil, nil
				},
				consent: func(start) bool { nativeCalls.Add(1); return false },
				stream:  func(func([]byte), func(string), <-chan input) error { nativeCalls.Add(1); return nil },
			}
			ts := httptest.NewTLSServer(s)
			defer ts.Close()
			defer s.stopActive()
			roots := x509.NewCertPool()
			roots.AddCert(ts.Certificate())
			dialer := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: roots}}
			c, _, err := dialer.Dial("wss"+strings.TrimPrefix(ts.URL, "https")+"/session", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err = c.WriteJSON(map[string]string{"type": "auth", "token": s.token}); err != nil {
				t.Fatal(err)
			}
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			var catalog struct {
				Type          string   `json:"type"`
				MaxFPS        int      `json:"maxFps"`
				VideoError    string   `json:"videoError"`
				TransferToken string   `json:"transferToken"`
				Windows       []window `json:"windows"`
			}
			if err = c.ReadJSON(&catalog); err != nil {
				t.Fatal(err)
			}
			if catalog.Type != "catalog" || catalog.MaxFPS != 0 || catalog.VideoError == "" || len(catalog.TransferToken) != 64 || catalog.Windows == nil {
				t.Fatalf("file-only catalog must be explicit and still grant files: %+v", catalog)
			}
			if err = c.WriteJSON(start{Type: "start", FPS: 60, Width: 1280, Quality: 70, Codec: "h264", Bitrate: 8}); err != nil {
				t.Fatal(err)
			}
			var status struct{ Type, Message string }
			if err = c.ReadJSON(&status); err != nil || status.Type != "status" || status.Message == "" {
				t.Fatalf("unavailable video: %+v %v", status, err)
			}
			if err = c.WriteJSON(input{Type: "lease", Active: false, View: false}); err != nil {
				t.Fatal(err)
			}
			client := ts.Client()
			client.Timeout = 2 * time.Second
			upload := func() *http.Response {
				r, err := http.NewRequest(http.MethodPost, ts.URL+"/transfer", bytes.NewReader([]byte("synthetic clip")))
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Authorization", "Bearer "+catalog.TransferToken)
				r.Header.Set("X-MacDow-Name", base64.RawURLEncoding.EncodeToString([]byte("clip.mov")))
				r.Header.Set("X-MacDow-Kind", "file")
				response, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				return response
			}
			response := upload()
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("paired file-only transfer failed: %d %s", response.StatusCode, body)
			}
			data, err := os.ReadFile(filepath.Join(receiver.root, "clip.mov"))
			if err != nil || string(data) != "synthetic clip" || nativeCalls.Load() != 0 {
				t.Fatalf("file-only started native work or changed bytes: %v", err)
			}
			c.Close()
			deadline := time.Now().Add(2 * time.Second)
			for {
				receiver.mu.Lock()
				revoked := receiver.credential == nil
				receiver.mu.Unlock()
				if revoked {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("disconnect retained transfer access")
				}
				time.Sleep(time.Millisecond)
			}
			response = upload()
			response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("disconnected capability still works: %d", response.StatusCode)
			}
		})
	}
}
