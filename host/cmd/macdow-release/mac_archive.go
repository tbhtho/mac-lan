package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tbhtho/MAC-DOW/host/internal/updater"
)

const maximumMacArchiveExpansion = 256 * 1024 * 1024
const maximumMacHostBytes = 128 * 1024 * 1024

// Match credentials inside binary string tables, not just a whole line. The
// generic validation pattern compiled into the host contains no concrete ID or
// token and therefore does not match. Tokens here are never printed.
var embeddedWebhook = regexp.MustCompile(`(?i:https://(?:canary\.|ptb\.)?discord(?:app)?\.com/api(?:/v[0-9]+)?/webhooks/)[0-9]{17,22}/[A-Za-z0-9_-]{16,200}`)

func validatePublicMacArchive(source io.Reader) error {
	return validatePublicMacArchiveBound(source, "", "", "")
}

func validatePublicMacArchiveBound(source io.Reader, expectedRoot, version, commit string) error {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return errors.New("Mac update must be a gzip tar archive")
	}
	defer gz.Close()
	limited := &io.LimitedReader{R: gz, N: maximumMacArchiveExpansion + 1}
	archive := tar.NewReader(limited)
	foundHost, publicUpdate := false, false
	root, temporary := "", ""
	defer func() {
		if temporary != "" {
			_ = os.RemoveAll(temporary)
		}
	}()
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
		key := strings.ToLower(name)
		if !safeMacArchiveName(name) || seen[key] {
			return errors.New("Mac update archive contains an unsafe or duplicate path")
		}
		entryRoot := strings.Split(name, "/")[0]
		if entryRoot != "macos-arm64" && entryRoot != "mac-lan.app" || root != "" && root != entryRoot || expectedRoot != "" && entryRoot != expectedRoot {
			return errors.New("Mac update archive contains an unexpected package root")
		}
		root = entryRoot
		seen[key] = true
		if root == "mac-lan.app" && temporary == "" {
			temporary, err = os.MkdirTemp("", "mac-lan-public-package-")
			if err != nil {
				return errors.New("cannot validate Mac app package")
			}
		}
		if h.Typeflag == tar.TypeDir {
			if h.Size != 0 {
				return errors.New("Mac update directory contains unexpected data")
			}
			if temporary != "" {
				if err := os.MkdirAll(filepath.Join(temporary, filepath.FromSlash(name)), 0700); err != nil {
					return err
				}
			}
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA || h.Size < 0 || h.Size > maximumMacHostBytes {
			return errors.New("Mac update archive must contain bounded regular files, without links")
		}
		content := io.Reader(archive)
		var output *os.File
		if temporary != "" {
			dest := filepath.Join(temporary, filepath.FromSlash(name))
			if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return err
			}
			output, err = os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
			if err != nil {
				return err
			}
			content = io.TeeReader(archive, output)
		}
		if root == "mac-lan.app" && (name == root+"/Contents/MacOS/mac-lan" || name == root+"/Contents/Helpers/MacDowHost") && (h.Size == 0 || h.Mode&0111 == 0) {
			if output != nil {
				output.Close()
			}
			return errors.New("Mac app executable content or mode is missing")
		}
		provenanceName := "macos-arm64/BUILD-PROVENANCE.json"
		if root == "mac-lan.app" {
			provenanceName = root + "/Contents/Resources/BUILD-PROVENANCE.json"
		}
		if name == provenanceName {
			if h.Size > 64*1024 {
				if output != nil {
					output.Close()
				}
				return errors.New("Mac public build provenance exceeds limits")
			}
			raw, err := io.ReadAll(content)
			if output != nil {
				closeErr := output.Close()
				if err == nil {
					err = closeErr
				}
			}
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
			scanErr := scanPrivateCredentials(content)
			if output != nil {
				closeErr := output.Close()
				if scanErr == nil {
					scanErr = closeErr
				}
			}
			if scanErr != nil {
				return scanErr
			}
		}
	}
	// Drain the bounded reader to validate the gzip checksum and reject expansion
	// hidden after tar's end marker or in concatenated gzip members.
	if err := scanPrivateCredentials(limited); err != nil || limited.N <= 0 {
		return errors.New("Mac update archive is corrupt or exceeds expansion limits")
	}
	if root == "mac-lan.app" {
		app := filepath.Join(temporary, root)
		if err := updater.ValidateMacAppBundle(app, false); err != nil {
			return err
		}
		if version != "" || commit != "" {
			raw, err := os.ReadFile(filepath.Join(app, "Contents", "Resources", "MAC-DOW-INSTALL.json"))
			var marker updater.Marker
			if err != nil || json.Unmarshal(raw, &marker) != nil || marker.Version != version || marker.Commit != commit {
				return errors.New("Mac app identity differs from manifest version or commit")
			}
		}
		foundHost = true
	}
	if !foundHost || !publicUpdate {
		return errors.New("Mac update requires a regular host and explicit publicUpdate:true provenance")
	}
	return nil
}

func safeMacArchiveName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\:\x00") || path.IsAbs(name) || path.Clean(name) != name {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." || strings.TrimRight(component, ". ") != component {
			return false
		}
		for _, r := range component {
			if r < 32 {
				return false
			}
		}
		stem := strings.ToUpper(strings.Split(component, ".")[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9' {
			return false
		}
	}
	return true
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
