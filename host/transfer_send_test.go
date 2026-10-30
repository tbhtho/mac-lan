package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func outgoingRequest(t *transferReceiver, token, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = "192.168.1.20:1234"
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	t.serveOutgoing(w, r, func(string) bool { return true })
	return w
}
func readyOffer(t *testing.T, tr *transferReceiver, path string) outgoingFile {
	t.Helper()
	ready := make(chan string, 100)
	tr.offerFile(path, func(s string) {
		select {
		case ready <- s:
		default:
		}
	})
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case s := <-ready:
			if strings.HasPrefix(s, "File ready.") {
				tr.mu.Lock()
				o := *tr.outgoing
				tr.mu.Unlock()
				return o
			}
			if strings.HasPrefix(s, "Cannot") {
				t.Fatal(s)
			}
		case <-timer.C:
			t.Fatal("offer not ready")
		}
	}
}
func TestOutgoingFileRoundTrip(t *testing.T) {
	for _, size := range []int{0, 700019, 16 << 20} {
		t.Run(string(rune('a'+size%20)), func(t *testing.T) {
			tr := newTransferReceiver(t.TempDir())
			token, revoke, err := tr.grant("192.168.1.20")
			if err != nil {
				t.Fatal(err)
			}
			defer revoke()
			data := bytes.Repeat([]byte{42}, size)
			file := filepath.Join(t.TempDir(), "café 文.txt")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			o := readyOffer(t, tr, file)
			meta := outgoingRequest(tr, token, "GET", "/transfer/outgoing")
			var got outgoingFile
			if meta.Code != 200 || json.Unmarshal(meta.Body.Bytes(), &got) != nil || got.Name != o.Name || got.Bytes != int64(size) {
				t.Fatal("metadata", meta.Code, meta.Body.String())
			}
			if strings.Contains(meta.Body.String(), filepath.Dir(file)) {
				t.Fatal("source path leaked")
			}
			body := outgoingRequest(tr, token, "GET", "/transfer/outgoing/"+o.ID)
			sum := sha256.Sum256(body.Body.Bytes())
			if body.Code != 200 || !bytes.Equal(data, body.Body.Bytes()) || hex.EncodeToString(sum[:]) != o.SHA256 {
				t.Fatal("incorrect bytes/hash")
			}
			// A download can be retried until Windows verifies and acknowledges publication.
			if retry := outgoingRequest(tr, token, "GET", "/transfer/outgoing/"+o.ID); retry.Code != 200 {
				t.Fatal("retry failed")
			}
			ack := httptest.NewRequest("POST", "/transfer/outgoing/"+o.ID, nil)
			ack.RemoteAddr = "192.168.1.20:1234"
			ack.Header.Set("Authorization", "Bearer "+token)
			ack.Header.Set("X-MacDow-SHA256", o.SHA256)
			w := httptest.NewRecorder()
			tr.serveOutgoing(w, ack, func(string) bool { return true })
			if w.Code != 204 {
				t.Fatal("ack", w.Code)
			}
			if outgoingRequest(tr, token, "GET", "/transfer/outgoing").Code != 404 {
				t.Fatal("ack retained offer")
			}
			if original, _ := os.ReadFile(file); !bytes.Equal(original, data) {
				t.Fatal("original changed")
			}
		})
	}
}
func TestOutgoingAuthorizationAndCancellation(t *testing.T) {
	tr := newTransferReceiver(t.TempDir())
	token, revoke, _ := tr.grant("192.168.1.20")
	defer revoke()
	file := filepath.Join(t.TempDir(), "fixture.txt")
	os.WriteFile(file, []byte("fixture"), 0600)
	o := readyOffer(t, tr, file)
	for _, tc := range []struct {
		token, path string
		code        int
	}{{"wrong", "/transfer/outgoing", 403}, {token, "/transfer/outgoing/../../secret", 404}} {
		if w := outgoingRequest(tr, tc.token, "GET", tc.path); w.Code != tc.code {
			t.Fatal(w.Code, tc.code)
		}
	}
	r := httptest.NewRequest("GET", "/transfer/outgoing", nil)
	r.RemoteAddr = "192.168.1.21:1"
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	tr.serveOutgoing(w, r, func(string) bool { return true })
	if w.Code != 403 {
		t.Fatal("wrong peer")
	}
	r.RemoteAddr = "192.168.1.20:1"
	r.Header.Set("Origin", "https://example.com")
	w = httptest.NewRecorder()
	tr.serveOutgoing(w, r, func(string) bool { return true })
	if w.Code != 403 {
		t.Fatal("origin")
	}
	tr.cancelOutgoing()
	if outgoingRequest(tr, token, "GET", "/transfer/outgoing/"+o.ID).Code != 404 {
		t.Fatal("cancel failed")
	}
	readyOffer(t, tr, file)
	revoke()
	if outgoingRequest(tr, token, "GET", "/transfer/outgoing").Code != 403 {
		t.Fatal("revoked")
	}
	token2, revoke2, _ := tr.grant("192.168.1.20")
	defer revoke2()
	if outgoingRequest(tr, token2, "GET", "/transfer/outgoing").Code != 404 {
		t.Fatal("offer crossed pairing")
	}
}
func TestOutgoingRejectsUnselectedAndLinks(t *testing.T) {
	tr := newTransferReceiver(t.TempDir())
	ch := make(chan string, 10)
	status := func(s string) { ch <- s }
	tr.offerFile("not-selected", status)
	if !strings.Contains(<-ch, "Connect Windows") {
		t.Fatal("unpaired")
	}
	_, revoke, _ := tr.grant("192.168.1.20")
	defer revoke()
	file := filepath.Join(t.TempDir(), "link")
	os.Symlink("missing", file)
	tr.offerFile(file, status)
	select {
	case s := <-ch:
		if !strings.HasPrefix(s, "Cannot") {
			t.Fatal(s)
		}
	case <-time.After(time.Second):
		t.Fatal("link not rejected")
	}
}

type cancellingWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	count  int
}

func (w *cancellingWriter) Write(p []byte) (int, error) {
	w.count += len(p)
	w.cancel()
	return len(p), nil
}
func TestOutgoingInterruptedDownload(t *testing.T) {
	tr := newTransferReceiver(t.TempDir())
	token, revoke, _ := tr.grant("192.168.1.20")
	defer revoke()
	file := filepath.Join(t.TempDir(), "data.bin")
	os.WriteFile(file, bytes.Repeat([]byte{1}, 2<<20), 0600)
	o := readyOffer(t, tr, file)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "/transfer/outgoing/"+o.ID, nil).WithContext(ctx)
	r.RemoteAddr = "192.168.1.20:1"
	r.Header.Set("Authorization", "Bearer "+token)
	w := &cancellingWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	tr.serveOutgoing(w, r, func(string) bool { return true })
	if w.count > transferChunk {
		t.Fatal("cancel continued streaming", w.count)
	}
	tr.mu.Lock()
	downloaded := tr.outgoing.downloaded
	tr.mu.Unlock()
	if downloaded {
		t.Fatal("partial download marked complete")
	}
	if outgoingRequest(tr, token, "GET", "/transfer/outgoing/"+o.ID).Code != 200 {
		t.Fatal("cancelled request cannot retry")
	}
}
