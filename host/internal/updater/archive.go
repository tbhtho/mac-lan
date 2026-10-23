package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const MaxArchive int64 = 256 << 20
const maxExpanded int64 = 1 << 30
const maxFile int64 = 512 << 20

func CheckDigest(r io.Reader, size int64, digest string) error {
	if size <= 0 || size > MaxArchive || len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		return errors.New("invalid integrity metadata")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(r, size+1))
	if e != nil {
		return e
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != digest[7:] {
		return errors.New("download size/SHA-256 mismatch")
	}
	return nil
}
func safeName(s string) (string, error) {
	s = strings.TrimSuffix(s, "/")
	if s == "" || strings.ContainsAny(s, "\\:\x00") || strings.HasPrefix(s, "/") || path.Clean(s) != s {
		return "", errors.New("unsafe archive path")
	}
	for _, p := range strings.Split(s, "/") {
		if p == "" || p == "." || p == ".." || strings.TrimRight(p, ". ") != p {
			return "", errors.New("unsafe archive component")
		}
		for _, r := range p {
			if r < 32 {
				return "", errors.New("control character in path")
			}
		}
		stem := strings.ToUpper(strings.Split(p, ".")[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9' {
			return "", errors.New("reserved archive name")
		}
	}
	return s, nil
}
func Extract(r io.ReaderAt, size int64, ext, dest string) error {
	if entries, e := os.ReadDir(dest); e != nil || len(entries) != 0 {
		return errors.New("extraction requires an empty owned directory")
	}
	seen := map[string]bool{}
	var total int64
	count := 0
	put := func(name string, mode os.FileMode, n int64, src io.Reader) error {
		name, e := safeName(name)
		if e != nil {
			return e
		}
		key := strings.ToLower(name)
		if seen[key] {
			return errors.New("duplicate archive path")
		}
		seen[key] = true
		count++
		if count > 2000 || n < 0 || n > maxFile || total+n > maxExpanded {
			return errors.New("archive resource limit exceeded")
		}
		total += n
		target := filepath.Join(dest, filepath.FromSlash(name))
		rel, e := filepath.Rel(dest, target)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("archive escaped staging")
		}
		if mode.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !mode.IsRegular() {
			return errors.New("archive links/devices are forbidden")
		}
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		perm := os.FileMode(0600)
		if mode&0111 != 0 {
			perm = 0700
		}
		f, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if e != nil {
			return e
		}
		written, copyErr := io.Copy(f, io.LimitReader(src, n+1))
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if written != n {
			return errors.New("archive entry length mismatch")
		}
		return nil
	}
	if ext == ".zip" {
		z, e := zip.NewReader(r, size)
		if e != nil {
			return e
		}
		for _, f := range z.File {
			if f.UncompressedSize64 > uint64(maxFile) {
				return errors.New("ZIP expansion limit")
			}
			rd, e := f.Open()
			if e != nil {
				return e
			}
			e = put(f.Name, f.Mode(), int64(f.UncompressedSize64), rd)
			closeErr := rd.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	}
	if ext != ".tar.gz" {
		return fmt.Errorf("unsupported archive format %q", ext)
	}
	gz, e := gzip.NewReader(io.NewSectionReader(r, 0, size))
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		mode := os.FileMode(h.Mode) & 0777
		if h.Typeflag == tar.TypeDir {
			mode |= os.ModeDir
		} else if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return errors.New("tar links/devices are forbidden")
		}
		if e = put(h.Name, mode, h.Size, tr); e != nil {
			return e
		}
	}
}
