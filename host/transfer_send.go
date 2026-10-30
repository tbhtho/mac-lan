package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Only the local file picker can create an offer. HTTP accepts an opaque ID,
// never a path. An offer belongs to exactly one approved pairing.
type outgoingFile struct {
	ID                             string `json:"id"`
	Name                           string `json:"name"`
	Bytes                          int64  `json:"bytes"`
	SHA256                         string `json:"sha256"`
	file                           *os.File
	ctx                            context.Context
	cancel                         context.CancelFunc
	credential                     *transferCredential
	status                         func(string)
	ready, downloading, downloaded bool
}

func (t *transferReceiver) cancelOutgoing() {
	t.mu.Lock()
	o := t.outgoing
	t.outgoing = nil
	t.mu.Unlock()
	if o != nil {
		o.cancel()
	}
}
func (t *transferReceiver) offerFile(path string, status func(string)) {
	t.mu.Lock()
	if t.credential == nil || t.credential.ctx.Err() != nil || t.stopped {
		t.mu.Unlock()
		status("Connect Windows before choosing a file.")
		return
	}
	if t.outgoing != nil {
		t.mu.Unlock()
		status("Finish or cancel the current outgoing file first.")
		return
	}
	ctx, cancel := context.WithCancel(t.credential.ctx)
	o := &outgoingFile{ctx: ctx, cancel: cancel, credential: t.credential, status: status}
	t.outgoing = o
	t.mu.Unlock()
	go func() {
		defer func() {
			if !o.ready {
				cancel()
			}
		}()
		context.AfterFunc(ctx, func() {
			t.mu.Lock()
			if t.outgoing == o {
				t.outgoing = nil
			}
			f := o.file
			t.mu.Unlock()
			if f != nil {
				f.Close()
			}
		})
		fail := func() {
			status("Cannot prepare this file. Choose a regular readable file up to 100 GiB, or reconnect and retry.")
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || !safeTransferName(filepath.Base(path)) || info.Size() > maxTransferBytes {
			fail()
			return
		}
		f, err := os.Open(path)
		if err != nil {
			fail()
			return
		}
		actual, err := f.Stat()
		if err != nil || !os.SameFile(info, actual) {
			f.Close()
			fail()
			return
		}
		t.mu.Lock()
		if ctx.Err() != nil {
			t.mu.Unlock()
			f.Close()
			return
		}
		o.file = f
		t.mu.Unlock()
		status("Preparing file for Windows…")
		hash := sha256.New()
		n, err := copyTransfer(ctx, hash, f, info.Size())
		if err != nil || n != info.Size() {
			fail()
			return
		}
		secret := make([]byte, 16)
		if _, err = rand.Read(secret); err != nil {
			fail()
			return
		}
		t.mu.Lock()
		if ctx.Err() != nil {
			t.mu.Unlock()
			return
		}
		o.ID = hex.EncodeToString(secret)
		o.Name = filepath.Base(path)
		o.Bytes = n
		o.SHA256 = hex.EncodeToString(hash.Sum(nil))
		o.ready = true
		t.mu.Unlock()
		status("File ready. On Windows open Files → Receive from Mac, then choose where to save it.")
	}()
}
func (t *transferReceiver) serveOutgoing(w http.ResponseWriter, r *http.Request, allowed func(string) bool) {
	if !t.authorized(r, allowed) {
		transferError(w, 403, "Pair with this Mac before receiving files.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	t.mu.Lock()
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !t.authorizedLocked(r, peer) {
		t.mu.Unlock()
		transferError(w, 403, "Pairing expired.")
		return
	}
	o := t.outgoing
	if o == nil || !o.ready || o.ctx.Err() != nil || o.credential != t.credential {
		t.mu.Unlock()
		transferError(w, 404, "Choose Send File to Windows in the Mac menu first.")
		return
	}
	if r.URL.Path == "/transfer/outgoing" && r.Method == http.MethodGet {
		// Encode while locked so publication and cancellation cannot race metadata.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(o)
		t.mu.Unlock()
		return
	}
	if r.URL.Path != "/transfer/outgoing/"+o.ID {
		t.mu.Unlock()
		transferError(w, 404, "File offer expired.")
		return
	}
	if r.Method == http.MethodPost && o.downloaded && !o.downloading && r.Header.Get("X-MacDow-SHA256") == o.SHA256 {
		t.outgoing = nil
		t.mu.Unlock()
		o.cancel()
		o.status("File saved and verified on Windows. Your original stays on this Mac.")
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodGet || o.downloading {
		t.mu.Unlock()
		transferError(w, 409, "File is already being received or the request is invalid.")
		return
	}
	o.downloading = true
	t.mu.Unlock()
	defer func() { t.mu.Lock(); o.downloading = false; t.mu.Unlock() }()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(o.ctx, func() { cancel(); _ = http.NewResponseController(w).SetWriteDeadline(time.Now()) })
	defer stop()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(o.Bytes, 10))
	progress := &outgoingProgress{writer: w, total: o.Bytes, status: o.status, last: -1}
	n, err := copyTransfer(ctx, progress, io.NewSectionReader(o.file, 0, o.Bytes), o.Bytes)
	t.mu.Lock()
	o.downloaded = err == nil && n == o.Bytes && ctx.Err() == nil
	t.mu.Unlock()
}

// Paths and filenames never appear in the shared diagnostic log.
func (t *transferReceiver) outgoingCommand(kind, path string, status func(string)) {
	switch kind {
	case "send-file":
		t.offerFile(path, status)
	case "cancel-file":
		t.cancelOutgoing()
		status("Outgoing file cancelled. Your original stays on this Mac.")
	}
}

type outgoingProgress struct {
	writer      io.Writer
	total, sent int64
	last        int
	status      func(string)
}

func (p *outgoingProgress) Write(data []byte) (int, error) {
	n, err := p.writer.Write(data)
	p.sent += int64(n)
	if p.total > 0 {
		percent := int(p.sent * 100 / p.total)
		if percent/5 != p.last {
			p.last = percent / 5
			p.status(fmt.Sprintf("Sending to Windows: %d%%. Waiting for verification before completion.", percent))
		}
	}
	return n, err
}
