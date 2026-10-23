package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"
)

const maximumMacArchiveExpansion = 256 * 1024 * 1024
const maximumMacHostBytes = 128 * 1024 * 1024

// Match credentials inside binary string tables, not just a whole line. The
// generic validation pattern compiled into the host contains no concrete ID or
// token and therefore does not match. Tokens here are never printed.
var embeddedWebhook = regexp.MustCompile(`(?i:https://(?:canary\.|ptb\.)?discord(?:app)?\.com/api(?:/v[0-9]+)?/webhooks/)[0-9]{17,22}/[A-Za-z0-9_-]{16,200}`)

func validatePublicMacArchive(source io.Reader) error {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return errors.New("Mac update must be a gzip tar archive")
	}
	defer gz.Close()
	limited := &io.LimitedReader{R: gz, N: maximumMacArchiveExpansion + 1}
	archive := tar.NewReader(limited)
	foundHost, publicUpdate := false, false
	seen := map[string]bool{}
	for entries := 0; ; entries++ {
		h, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil || entries >= 512 || limited.N <= 0 {
			return errors.New("Mac update archive is invalid or exceeds limits")
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "" || strings.ContainsAny(name, `\:`) || path.IsAbs(name) || path.Clean(name) != name || name != "macos-arm64" && !strings.HasPrefix(name, "macos-arm64/") || seen[name] {
			return errors.New("Mac update archive contains an unsafe or duplicate path")
		}
		seen[name] = true
		if h.Typeflag == tar.TypeDir {
			if h.Size != 0 {
				return errors.New("Mac update directory contains unexpected data")
			}
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA || h.Size < 0 || h.Size > maximumMacHostBytes {
			return errors.New("Mac update archive must contain bounded regular files, without links")
		}
		if name == "macos-arm64/BUILD-PROVENANCE.json" {
			if h.Size > 64*1024 {
				return errors.New("Mac public build provenance exceeds limits")
			}
			raw, err := io.ReadAll(archive)
			if err != nil || embeddedWebhook.Match(raw) {
				return errors.New("Mac public build provenance is invalid or contains a private diagnostic destination")
			}
			var provenance struct {
				PublicUpdate *bool `json:"publicUpdate"`
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			if decoder.Decode(&provenance) != nil || decoder.Decode(new(any)) != io.EOF || provenance.PublicUpdate == nil || !*provenance.PublicUpdate {
				return errors.New("Mac archive is not marked publicUpdate:true; build with package-macos-preview.ps1 -PublicUpdate")
			}
			publicUpdate = true
		} else {
			if name == "macos-arm64/MacDowHost" {
				if h.Size == 0 || h.Mode&0111 == 0 {
					return errors.New("Mac update host is missing executable content")
				}
				foundHost = true
			}
			if err := scanPrivateCredentials(archive); err != nil {
				return err
			}
		}
	}
	// Drain the bounded reader to validate the gzip checksum and reject expansion
	// hidden after tar's end marker or in concatenated gzip members.
	if err := scanPrivateCredentials(limited); err != nil || limited.N <= 0 {
		return errors.New("Mac update archive is corrupt or exceeds expansion limits")
	}
	if !foundHost || !publicUpdate {
		return errors.New("Mac update requires a regular host and explicit publicUpdate:true provenance")
	}
	return nil
}

func scanPrivateCredentials(source io.Reader) error {
	buffer := make([]byte, 64*1024+512)
	retained := 0
	for {
		n, err := source.Read(buffer[retained:])
		end := retained + n
		if embeddedWebhook.Match(buffer[:end]) {
			return errors.New("Mac update contains an embedded private diagnostic destination; rebuild with -PublicUpdate")
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("Mac update content could not be scanned")
		}
		if n == 0 {
			return io.ErrNoProgress
		}
		retained = end
		if retained > 512 {
			retained = 512
		}
		copy(buffer, buffer[end-retained:end])
	}
}
