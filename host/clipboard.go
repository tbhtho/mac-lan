package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
	"unicode/utf8"
)

// The paired Windows PC shares plain text with this Mac in both directions. It uses
// the same pinned listener and per-pairing bearer token as file transfer.
const maxClipboard = 256 << 10

type clipboard struct {
	read  func(context.Context) (string, error)
	write func(context.Context, string) error
}

func macClipboard() *clipboard {
	// pbcopy and pbpaste convert using the locale; force UTF-8 for non-ASCII text.
	env := append(os.Environ(), "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
	return &clipboard{
		read: func(ctx context.Context) (string, error) {
			cmd := exec.CommandContext(ctx, "/usr/bin/pbpaste", "-Prefer", "txt")
			cmd.Env = env
			out, err := cmd.Output()
			return string(out), err
		},
		write: func(ctx context.Context, text string) error {
			cmd := exec.CommandContext(ctx, "/usr/bin/pbcopy")
			cmd.Env = env
			cmd.Stdin = bytes.NewBufferString(text)
			return cmd.Run()
		},
	}
}

func (s *server) serveClipboard(w http.ResponseWriter, r *http.Request) {
	if !s.transfers.authorized(r, s.peerAllowed) {
		transferError(w, http.StatusForbidden, "Pair with this Mac before sharing the clipboard.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		text, err := s.clipboard.read(ctx)
		// Images, files and oversized text are not shared; report no text rather than an error.
		if err != nil || len(text) > maxClipboard || !utf8.ValidString(text) {
			text = ""
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "text": text})
	case http.MethodPut:
		var body struct{ Text string }
		data, err := io.ReadAll(io.LimitReader(r.Body, maxClipboard*2))
		if err != nil || json.Unmarshal(data, &body) != nil || len(body.Text) > maxClipboard || !utf8.ValidString(body.Text) {
			transferError(w, http.StatusBadRequest, "Clipboard text is too large or invalid.")
			return
		}
		if err = s.clipboard.write(ctx, body.Text); err != nil {
			transferError(w, http.StatusInternalServerError, "The Mac clipboard is unavailable.")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	default:
		transferError(w, http.StatusMethodNotAllowed, "Use GET or PUT.")
	}
}
