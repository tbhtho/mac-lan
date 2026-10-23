package updater

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Package identity is a bounded build marker, NOT proof of authenticity.
// Download signatures and digests are always verified separately.
func ReadPackageIdentity(path string) (Marker, error) {
	var m Marker
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return m, err
	}
	if !info.Mode().IsRegular() || info.Size() < 14 {
		return m, errors.New("not a standalone MAC-DOW executable")
	}
	tail := make([]byte, 12)
	if _, err = f.ReadAt(tail, info.Size()-12); err != nil {
		return m, err
	}
	size := int64(binary.LittleEndian.Uint32(tail[:4]))
	if string(tail[4:]) != "MCDOWPK1" || size < 2 || size > 4096 || size > info.Size()-14 {
		return m, errors.New("missing standalone package identity")
	}
	head := make([]byte, 2)
	if _, err = f.ReadAt(head, 0); err != nil || string(head) != "MZ" {
		return m, errors.New("not a Windows executable")
	}
	b := make([]byte, size)
	if _, err = f.ReadAt(b, info.Size()-12-size); err != nil {
		return m, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if m.Layout != "single-exe" {
		return m, errors.New("unknown standalone layout")
	}
	return m, nil
}
func standalonePlan(p Plan) bool   { return p.OS == "windows" && p.Asset.Name == "MAC-DOW.exe" }
func appBundle(base string) string { return filepath.Join(base, "MAC-DOW Host.app") }
func isApp(path string) bool       { return strings.HasSuffix(path, ".app") }
func metadataRoot(root string) string {
	if isApp(root) {
		return filepath.Join(root, "Contents", "Resources")
	}
	return root
}
func copyStandalone(src *os.File, dest string) error {
	out, err := os.OpenFile(filepath.Join(dest, "MAC-DOW.exe"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return err
	}
	if _, err = src.Seek(0, io.SeekStart); err == nil {
		_, err = io.Copy(out, src)
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func compatibleInstallShape(install, next, goos string) error {
	before, err := os.Stat(install)
	if err != nil {
		return err
	}
	after, err := os.Stat(next)
	if err != nil {
		return err
	}
	if before.IsDir() != after.IsDir() || (goos == "darwin" && isApp(install) != isApp(next)) {
		return errors.New("this older installation uses a different package layout; move to the standalone app once before in-app replacement")
	}
	return nil
}
