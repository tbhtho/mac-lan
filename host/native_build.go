package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	posixpath "path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tbhtho/MAC-DOW/host/internal/diagnostics"
)

// Set only for cross-compiled preview packages. Native .app builds use their
// sibling helper. The preview never substitutes an older helper for this source.
var NativeCaptureSourceGzip string
var NativeCaptureSourceSHA256 string

const nativeSourceLimit = 128 * 1024
const nativeEncodedLimit = 256 * 1024
const nativeBinaryLimit = 64 * 1024 * 1024

type nativeCommand func(context.Context, string, []string, []string) ([]byte, error)
type nativeBuilder struct {
	encoded, sourceSHA, cacheBase, goos, goarch string
	run                                         nativeCommand
	emit                                        func(string, string)
}
type nativeManifest struct {
	SourceSHA string `json:"sourceSHA256"`
	BinarySHA string `json:"binarySHA256"`
}

func resolveCaptureHelper(ctx context.Context, sibling string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil && (NativeCaptureSourceGzip != "" || NativeCaptureSourceSHA256 != "") {
		return "", errors.New("native capture build cannot locate the Mac user cache")
	}
	b := nativeBuilder{NativeCaptureSourceGzip, NativeCaptureSourceSHA256, cache, runtime.GOOS, runtime.GOARCH, runNativeCommand,
		func(event, code string) {
			if event == "native-build-started" {
				fmt.Fprintln(os.Stderr, "Preparing the 60 fps native capture helper. The first launch may take a minute; no video or input has started.")
			}
			if event == "native-build-failed" {
				diagnostics.Record("mac", event, code, nil) // Retain the fixed stage before main exits.
			} else {
				diagnostics.Emit("mac", event, code, nil)
			}
		}}
	return b.resolve(ctx, sibling)
}

func decodeNativeSource(encoded, expected string) ([]byte, error) {
	if len(encoded) == 0 || len(encoded) > nativeEncodedLimit || len(expected) != 64 || strings.ToLower(expected) != expected {
		return nil, errors.New("embedded capture source configuration is invalid")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return nil, errors.New("embedded capture source hash is invalid")
	}
	compressed, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, errors.New("embedded capture source encoding is invalid")
	}
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, errors.New("embedded capture source compression is invalid")
	}
	defer r.Close()
	source, err := io.ReadAll(io.LimitReader(r, nativeSourceLimit+1))
	if err != nil || len(source) == 0 || len(source) > nativeSourceLimit {
		return nil, errors.New("embedded capture source size or checksum is invalid")
	}
	digest := sha256.Sum256(source)
	if hex.EncodeToString(digest[:]) != expected {
		return nil, errors.New("embedded capture source hash did not match")
	}
	return source, nil
}

func (b nativeBuilder) resolve(ctx context.Context, sibling string) (path string, err error) {
	if b.encoded == "" && b.sourceSHA == "" {
		return sibling, nil
	}
	stage := "source-invalid"
	if b.emit == nil {
		b.emit = func(string, string) {}
	}
	b.emit("native-build-started", "source")
	defer func() {
		if err != nil {
			b.emit("native-build-failed", stage)
		}
	}()
	source, err := decodeNativeSource(b.encoded, b.sourceSHA)
	if err != nil {
		return "", err
	}
	stage = "unsupported-platform"
	if b.goos != "darwin" || b.goarch != "arm64" {
		return "", errors.New("embedded native capture requires Apple Silicon macOS 14+")
	}
	stage = "cache-unavailable"
	if b.cacheBase == "" {
		return "", errors.New("native capture build user cache is unavailable")
	}
	root, err := nativeCacheDirectory(b.cacheBase, b.sourceSHA)
	if err != nil {
		return "", err
	}
	cleanupNativeTemps(root)
	helper := filepath.Join(root, "MacDowCapture")
	manifestPath := filepath.Join(root, "manifest.json")
	if validNativeCache(helper, manifestPath, b.sourceSHA) {
		stage = "self-test-failed"
		if _, err = b.command(ctx, 15*time.Second, helper, []string{"--self-test"}, nil); err != nil {
			return "", fmt.Errorf("cached native capture self-test failed: %w", err)
		}
		b.emit("native-build-ready", "cached")
		return helper, nil
	}
	// Invalid files can be replaced; links and non-regular targets must not be followed.
	for _, p := range []string{helper, manifestPath} {
		if info, statErr := os.Lstat(p); statErr == nil && !info.Mode().IsRegular() {
			return "", errors.New("native capture cache contains an unsafe file")
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return "", statErr
		}
	}
	stage = "toolchain-missing"
	if _, err = b.command(ctx, 10*time.Second, "/usr/bin/xcode-select", []string{"-p"}, nil); err != nil {
		return "", errors.New("existing Xcode Command Line Tools are required to build the capture helper; nothing was installed")
	}
	compilerOutput, err := b.command(ctx, 10*time.Second, "/usr/bin/xcrun", []string{"--find", "swiftc"}, nil)
	if err != nil {
		return "", errors.New("existing Swift compiler is unavailable; nothing was installed")
	}
	compiler := strings.TrimSpace(string(compilerOutput))
	if !posixpath.IsAbs(compiler) || posixpath.Base(compiler) != "swiftc" {
		return "", errors.New("existing Swift compiler path is invalid")
	}
	stage = "cache-unavailable"
	temp, err := os.MkdirTemp(root, ".macdow-build-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp) // Only the directory created by this invocation.
	sourcePath, temporaryHelper := filepath.Join(temp, "MacDowCapture.swift"), filepath.Join(temp, "MacDowCapture")
	moduleCache := filepath.Join(temp, "modules")
	if err = os.Mkdir(moduleCache, 0700); err != nil {
		return "", err
	}
	if err = os.WriteFile(sourcePath, source, 0600); err != nil {
		return "", err
	}
	args := []string{"-swift-version", "5", "-O", "-num-threads", "2", "-target", "arm64-apple-macos14.0", "-module-cache-path", moduleCache, sourcePath, "-o", temporaryHelper}
	for _, framework := range []string{"ApplicationServices", "Foundation", "AppKit", "ScreenCaptureKit", "CoreGraphics", "CoreMedia", "CoreImage", "VideoToolbox", "ImageIO", "UniformTypeIdentifiers"} {
		args = append(args, "-framework", framework)
	}
	env := []string{"CLANG_MODULE_CACHE_PATH=" + moduleCache, "SWIFT_MODULECACHE_PATH=" + moduleCache}
	stage = "compile-failed"
	if _, err = b.command(ctx, 3*time.Minute, compiler, args, env); err != nil {
		return "", fmt.Errorf("native capture compilation failed: %w", err)
	}
	if _, err = nativeFileSHA(temporaryHelper, nativeBinaryLimit); err != nil {
		return "", fmt.Errorf("native compiler did not produce a regular capture helper: %w", err)
	}
	if err = os.Chmod(temporaryHelper, 0700); err != nil {
		return "", err
	}
	stage = "self-test-failed"
	if _, err = b.command(ctx, 15*time.Second, temporaryHelper, []string{"--self-test"}, nil); err != nil {
		return "", fmt.Errorf("new native capture self-test failed: %w", err)
	}
	binarySHA, err := nativeFileSHA(temporaryHelper, nativeBinaryLimit)
	if err != nil {
		return "", err
	}
	manifest, _ := json.Marshal(nativeManifest{b.sourceSHA, binarySHA})
	temporaryManifest := filepath.Join(temp, "manifest.json")
	if err = os.WriteFile(temporaryManifest, manifest, 0600); err != nil {
		return "", err
	}
	stage = "cache-write-failed"
	// Publishing the manifest last makes interrupted writes fail validation on restart.
	if err = os.Rename(temporaryHelper, helper); err != nil {
		return "", err
	}
	if err = os.Rename(temporaryManifest, manifestPath); err != nil {
		return "", err
	}
	b.emit("native-build-ready", "compiled")
	return helper, nil
}

func (b nativeBuilder) command(ctx context.Context, timeout time.Duration, name string, args, env []string) ([]byte, error) {
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := b.run(child, name, args, env)
	if len(output) > 16384 {
		output = output[:16384]
	}
	if err != nil {
		return output, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func nativeCacheDirectory(base, sourceSHA string) (string, error) {
	current := base
	for _, component := range []string{"", "MAC-DOW", "native", sourceSHA} {
		if component != "" {
			current = filepath.Join(current, component)
		}
		if err := os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("native capture cache directory is not a regular directory")
		}
	}
	return current, nil
}

func openNativeFile(path string, limit int64) (*os.File, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, nil, errors.New("native capture cache file is missing, unsafe, or too large")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		f.Close()
		return nil, nil, errors.New("native capture cache file changed while opening")
	}
	return f, info, nil
}

func nativeFileSHA(path string, limit int64) (string, error) {
	f, info, err := openNativeFile(path, limit)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	count, err := io.Copy(h, io.LimitReader(f, limit+1))
	if err != nil || count != info.Size() || count > limit {
		return "", errors.New("native capture cache file changed while reading")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func validNativeCache(helper, manifestPath, sourceSHA string) bool {
	f, info, err := openNativeFile(manifestPath, 1024)
	if err != nil {
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1025))
	var manifest nativeManifest
	if err != nil || int64(len(data)) != info.Size() || len(data) > 1024 || json.Unmarshal(data, &manifest) != nil || manifest.SourceSHA != sourceSHA {
		return false
	}
	binarySHA, err := nativeFileSHA(helper, nativeBinaryLimit)
	return err == nil && binarySHA == manifest.BinarySHA
}

func cleanupNativeTemps(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".macdow-build-") || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if filepath.Dir(path) == root {
			_ = os.RemoveAll(path)
		}
	}
}

type nativeOutput struct{ bytes.Buffer }

func (w *nativeOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 16384 - w.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = w.Buffer.Write(p)
	}
	return n, nil
}

func runNativeCommand(ctx context.Context, name string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	var output nativeOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	return output.Bytes(), err
}
