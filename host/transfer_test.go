package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func transferFixture(t *testing.T) (*transferReceiver, string, func()) {
	t.Helper()
	receiver := newTransferReceiver(filepath.Join(t.TempDir(), "received"))
	token, revoke, err := receiver.grant("192.168.1.2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(revoke)
	return receiver, token, revoke
}

func transferRequest(token, name, kind string, data []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://mac.example/transfer", bytes.NewReader(data))
	r.RemoteAddr = "192.168.1.2:12345"
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-MacDow-Name", base64.RawURLEncoding.EncodeToString([]byte(name)))
	r.Header.Set("X-MacDow-Kind", kind)
	return r
}

func receiveFixture(receiver *transferReceiver, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	receiver.serveHTTP(w, r, privateIP)
	return w
}

func zipFixture(t *testing.T, items map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, value := range items {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(w, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func assertOnlyReceived(t *testing.T, root string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != len(names) {
		t.Fatalf("unexpected received or staging entries: %v", entries)
	}
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTransferPreservesFileAndZipAndNeverOverwrites(t *testing.T) {
	receiver, token, _ := transferFixture(t)
	data := zipFixture(t, map[string]string{"clip.mov": "synthetic clip"})
	for i := 0; i < 2; i++ {
		w := receiveFixture(receiver, transferRequest(token, "clips.zip", "file", data))
		if w.Code != http.StatusOK {
			t.Fatalf("transfer status %d: %s", w.Code, w.Body)
		}
		var got transferReceipt
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		wantName := "clips.zip"
		if i == 1 {
			wantName = "clips (2).zip"
		}
		hash := sha256.Sum256(data)
		if !got.OK || got.Name != wantName || got.Bytes != int64(len(data)) || got.SHA256 != hex.EncodeToString(hash[:]) {
			t.Fatalf("wrong receipt: %+v", got)
		}
		saved, err := os.ReadFile(filepath.Join(receiver.root, got.Name))
		if err != nil || !bytes.Equal(saved, data) {
			t.Fatalf("changed file: %v", err)
		}
	}
	assertOnlyReceived(t, receiver.root, "clips.zip", "clips (2).zip")
}

func TestTransferFolderPreservesNestedFilesAndEmptyDirectories(t *testing.T) {
	receiver, token, _ := transferFixture(t)
	data := zipFixture(t, map[string]string{"nested/clip.mov": "video fixture", "empty/": ""})
	for i := 0; i < 2; i++ {
		w := receiveFixture(receiver, transferRequest(token, "Clips", "folder", data))
		if w.Code != http.StatusOK {
			t.Fatalf("folder status %d: %s", w.Code, w.Body)
		}
	}
	for _, name := range []string{"Clips", "Clips (2)"} {
		saved, err := os.ReadFile(filepath.Join(receiver.root, name, "nested", "clip.mov"))
		if err != nil || string(saved) != "video fixture" {
			t.Fatalf("nested file: %v", err)
		}
		st, err := os.Stat(filepath.Join(receiver.root, name, "empty"))
		if err != nil || !st.IsDir() {
			t.Fatalf("empty directory: %v", err)
		}
	}
	assertOnlyReceived(t, receiver.root, "Clips", "Clips (2)")
}

func TestTransferRequiresCurrentPairingPeerAndNonBrowserLANRequest(t *testing.T) {
	for _, scenario := range []string{"unpaired", "bad-token", "missing-bearer", "wrong-peer", "public-peer", "origin", "get", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			receiver, token, revoke := transferFixture(t)
			r := transferRequest(token, "clip.mov", "file", []byte("fixture"))
			switch scenario {
			case "unpaired":
				receiver = newTransferReceiver(receiver.root)
			case "bad-token":
				r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
			case "missing-bearer":
				r.Header.Set("Authorization", token)
			case "wrong-peer":
				r.RemoteAddr = "192.168.1.3:12345"
			case "public-peer":
				r.RemoteAddr = "8.8.8.8:12345"
			case "origin":
				r.Header.Set("Origin", "https://example.invalid")
			case "get":
				r.Method = http.MethodGet
			case "revoked":
				revoke()
			}
			w := receiveFixture(receiver, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("unauthorized transfer status %d", w.Code)
			}
			assertOnlyReceived(t, receiver.root)
		})
	}
}

func TestTransferRejectsUnsafeNamesLengthsAndKindsBeforeWriting(t *testing.T) {
	for _, scenario := range []string{"../clip", "a/b", `a\b`, "a:b", ".", "..", "\x00", "\x1f", strings.Repeat("a", 241), "bad-base64", "bad-kind", "unknown-length", "too-large", "short-body", "extra-body"} {
		name := scenario
		if len(name) > 100 {
			// TempDir includes the test name; keep its directory component within
			// filesystem limits while still testing the full overlong input.
			name = "overlong-name"
		}
		t.Run(name, func(t *testing.T) {
			receiver, token, _ := transferFixture(t)
			r := transferRequest(token, scenario, "file", []byte("fixture"))
			switch scenario {
			case "bad-base64":
				r.Header.Set("X-MacDow-Name", "a===")
			case "bad-kind":
				r.Header.Set("X-MacDow-Kind", "execute")
			case "unknown-length":
				r.ContentLength = -1
			case "too-large":
				r.ContentLength = maxTransferBytes + 1
			case "short-body":
				r.ContentLength++
			case "extra-body":
				r.ContentLength--
			}
			w := receiveFixture(receiver, r)
			if w.Code < 400 {
				t.Fatalf("unsafe request accepted: %s", w.Body)
			}
			assertOnlyReceived(t, receiver.root)
		})
	}
}

func TestTransferRejectsUnsafeZIPAndCleansStaging(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", `a\b`, "a:b", "a//b", "a/./b", strings.Repeat("a/", 32) + "deep"} {
		t.Run(name, func(t *testing.T) {
			receiver, token, _ := transferFixture(t)
			w := receiveFixture(receiver, transferRequest(token, "Clips", "folder", zipFixture(t, map[string]string{name: "fixture"})))
			if w.Code < 400 {
				t.Fatalf("unsafe ZIP path accepted: %q", name)
			}
			assertOnlyReceived(t, receiver.root)
		})
	}
	for _, scenario := range []string{"duplicate", "case-duplicate", "symlink", "special", "corrupt", "file-directory-conflict"} {
		t.Run(scenario, func(t *testing.T) {
			receiver, token, _ := transferFixture(t)
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			h := &zip.FileHeader{Name: "clip.mov", Method: zip.Store}
			if scenario == "symlink" {
				h.SetMode(os.ModeSymlink | 0600)
			}
			if scenario == "special" {
				h.SetMode(os.ModeNamedPipe | 0600)
			}
			w, _ := z.CreateHeader(h)
			_, _ = io.WriteString(w, "fixture")
			if scenario == "duplicate" || scenario == "case-duplicate" {
				name := "clip.mov"
				if scenario == "case-duplicate" {
					name = "CLIP.MOV"
				}
				w, _ = z.Create(name)
				_, _ = io.WriteString(w, "second")
			}
			if scenario == "file-directory-conflict" {
				w, _ = z.Create("clip.mov/child")
				_, _ = io.WriteString(w, "second")
			}
			_ = z.Close()
			data := b.Bytes()
			if scenario == "corrupt" {
				data[38] ^= 0x40
			}
			result := receiveFixture(receiver, transferRequest(token, "Clips", "folder", data))
			if result.Code < 400 {
				t.Fatalf("unsafe ZIP accepted: %s", scenario)
			}
			assertOnlyReceived(t, receiver.root)
		})
	}
}

type blockingTransferBody struct {
	started chan struct{}
	closed  chan struct{}
}

func (b *blockingTransferBody) Read(p []byte) (int, error) {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	<-b.closed
	return 0, context.Canceled
}
func (b *blockingTransferBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

func TestTransferDisconnectCancelsUploadAndBlocksConcurrency(t *testing.T) {
	receiver, token, revoke := transferFixture(t)
	body := &blockingTransferBody{started: make(chan struct{}), closed: make(chan struct{})}
	r := transferRequest(token, "clip.mov", "file", nil)
	r.Body, r.ContentLength = body, 4
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- receiveFixture(receiver, r) }()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("upload did not start")
	}
	w := receiveFixture(receiver, transferRequest(token, "other.mov", "file", []byte("fixture")))
	if w.Code != http.StatusConflict {
		t.Fatalf("concurrent upload status %d", w.Code)
	}
	revoke()
	select {
	case w := <-finished:
		if w.Code < 400 {
			t.Fatal("revoked transfer succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel active request body")
	}
	assertOnlyReceived(t, receiver.root)
}

func TestTransferRejectsSymlinkDestination(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	root := filepath.Join(base, "received")
	if err := os.Symlink(outside, root); err != nil {
		t.Skip("symlinks unavailable on this host")
	}
	receiver := newTransferReceiver(root)
	token, revoke, err := receiver.grant("192.168.1.2")
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	w := receiveFixture(receiver, transferRequest(token, "clip.mov", "file", []byte("fixture")))
	if w.Code < 400 {
		t.Fatal("symlink destination accepted")
	}
	assertOnlyReceived(t, outside)
}

func TestTransferRejectsForgedZIPDirectoryBoundsAndCount(t *testing.T) {
	for _, scenario := range []string{"count", "size", "offset", "oversized-directory", "multidisk"} {
		t.Run(scenario, func(t *testing.T) {
			receiver, token, _ := transferFixture(t)
			data := zipFixture(t, map[string]string{"one": "first", "two": "second"})
			end := data[len(data)-22:]
			switch scenario {
			case "count":
				binary.LittleEndian.PutUint16(end[8:], 1)
				binary.LittleEndian.PutUint16(end[10:], 1)
			case "size":
				binary.LittleEndian.PutUint32(end[12:], binary.LittleEndian.Uint32(end[12:])-1)
			case "offset":
				binary.LittleEndian.PutUint32(end[16:], binary.LittleEndian.Uint32(end[16:])+1)
			case "oversized-directory":
				binary.LittleEndian.PutUint32(end[12:], 17<<20)
			case "multidisk":
				binary.LittleEndian.PutUint16(end[4:], 1)
			}
			w := receiveFixture(receiver, transferRequest(token, "Clips", "folder", data))
			if w.Code < 400 {
				t.Fatalf("forged ZIP directory accepted: %s", scenario)
			}
			assertOnlyReceived(t, receiver.root)
		})
	}
}

func TestTransferMaximumLengthNamesRemainValidOnCollision(t *testing.T) {
	receiver, token, _ := transferFixture(t)
	name := strings.Repeat("é", 118) + ".mov"
	for i := 0; i < 2; i++ {
		w := receiveFixture(receiver, transferRequest(token, name, "file", []byte("fixture")))
		if w.Code != http.StatusOK {
			t.Fatalf("maximum-length file status %d", w.Code)
		}
		var got transferReceipt
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !safeTransferName(got.Name) {
			t.Fatalf("invalid collision name: %+v %v", got, err)
		}
	}
}

func TestTransferAcceptsZIP64DirectoryMetadata(t *testing.T) {
	receiver, token, _ := transferFixture(t)
	original := zipFixture(t, map[string]string{"clip.mov": "fixture"})
	oldEnd := original[len(original)-22:]
	data := append([]byte{}, original[:len(original)-22]...)
	zip64 := make([]byte, 56)
	binary.LittleEndian.PutUint32(zip64, 0x06064b50)
	binary.LittleEndian.PutUint64(zip64[4:], 44)
	binary.LittleEndian.PutUint16(zip64[12:], 45)
	binary.LittleEndian.PutUint16(zip64[14:], 45)
	binary.LittleEndian.PutUint64(zip64[24:], 1)
	binary.LittleEndian.PutUint64(zip64[32:], 1)
	binary.LittleEndian.PutUint64(zip64[40:], uint64(binary.LittleEndian.Uint32(oldEnd[12:])))
	binary.LittleEndian.PutUint64(zip64[48:], uint64(binary.LittleEndian.Uint32(oldEnd[16:])))
	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator, 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:], uint64(len(data)))
	binary.LittleEndian.PutUint32(locator[16:], 1)
	end := append([]byte{}, oldEnd...)
	binary.LittleEndian.PutUint16(end[8:], 0xffff)
	binary.LittleEndian.PutUint16(end[10:], 0xffff)
	binary.LittleEndian.PutUint32(end[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(end[16:], 0xffffffff)
	data = append(data, zip64...)
	data = append(data, locator...)
	data = append(data, end...)
	w := receiveFixture(receiver, transferRequest(token, "Clips", "folder", data))
	if w.Code != http.StatusOK {
		t.Fatalf("ZIP64 folder status %d: %s", w.Code, w.Body)
	}
	saved, err := os.ReadFile(filepath.Join(receiver.root, "Clips", "clip.mov"))
	if err != nil || string(saved) != "fixture" {
		t.Fatalf("ZIP64 content: %v", err)
	}
}

func TestTransferTLSStalledBodyIsCancelledByPairingDisconnect(t *testing.T) {
	receiver := newTransferReceiver(filepath.Join(t.TempDir(), "received"))
	s := &server{token: strings.Repeat("d", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1), transfers: receiver,
		catalog: func() ([]window, error) { return nil, nil },
	}
	server := httptest.NewTLSServer(s)
	defer server.Close()
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // Ephemeral loopback test certificate only.
	c, _, err := dialer.Dial("wss"+strings.TrimPrefix(server.URL, "https")+"/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.WriteJSON(map[string]string{"type": "auth", "token": s.token}); err != nil {
		t.Fatal(err)
	}
	var catalog struct{ Type, TransferToken string }
	if err := c.ReadJSON(&catalog); err != nil || catalog.Type != "catalog" || len(catalog.TransferToken) != 64 {
		t.Fatalf("paired catalog: %+v %v", catalog, err)
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	r, err := http.NewRequest(http.MethodPost, server.URL+"/transfer", reader)
	if err != nil {
		t.Fatal(err)
	}
	r.ContentLength = 1024
	r.Header.Set("Authorization", "Bearer "+catalog.TransferToken)
	r.Header.Set("X-MacDow-Name", base64.RawURLEncoding.EncodeToString([]byte("clip.mov")))
	r.Header.Set("X-MacDow-Kind", "file")
	result := make(chan error, 1)
	go func() {
		response, err := server.Client().Do(r)
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode < 400 {
				err = errTransfer
			}
		}
		result <- err
	}()
	if _, err := writer.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, _ := os.ReadDir(receiver.root)
		if len(entries) != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TLS upload did not reach staging")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = c.Close()
	select {
	case <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("pairing disconnect left TLS request stuck reading an idle upload body")
	}
	deadline = time.Now().Add(time.Second)
	for {
		entries, _ := os.ReadDir(receiver.root)
		if len(entries) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled TLS upload left staging data")
		}
		time.Sleep(5 * time.Millisecond)
	}
	response, err := server.Client().Post(server.URL+"/transfer", "application/octet-stream", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("unpaired follow-up transfer accepted")
	}
}
