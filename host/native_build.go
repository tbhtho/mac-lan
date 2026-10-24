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
	"regexp"
	"runtime"
	"strconv"
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
	menu                                        bool
	run                                         nativeCommand
	emit                                        func(string, string, map[string]int)
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
	b := nativeBuilder{encoded: NativeCaptureSourceGzip, sourceSHA: NativeCaptureSourceSHA256, cacheBase: cache,
		goos: runtime.GOOS, goarch: runtime.GOARCH, run: runNativeCommand,
		emit: func(event, code string, counts map[string]int) {
			if event == "native-build-started" {
				fmt.Fprintln(os.Stderr, "Preparing the 60 fps native capture helper. The first launch may take a minute; no video or input has started.")
			}
			if event == "native-build-failed" {
				diagnostics.Record("mac", event, code, counts) // Retain the fixed stage before main exits.
			} else {
				diagnostics.Emit("mac", event, code, counts)
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
	var failureCounts map[string]int
	if b.emit == nil {
		b.emit = func(string, string, map[string]int) {}
	}
	b.emit("native-build-started", "source", nil)
	defer func() {
		if err != nil {
			b.emit("native-build-failed", stage, failureCounts)
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
	binaryName, sourceName := "MacDowCapture", "MacDowCapture.swift"
	if b.menu {
		binaryName, sourceName = "MacLanMenu", "main.swift"
	}
	helper := filepath.Join(root, binaryName)
	manifestPath := filepath.Join(root, "manifest.json")
	if validNativeCache(helper, manifestPath, b.sourceSHA) {
		stage = "self-test-failed"
		if _, err = b.command(ctx, 15*time.Second, helper, []string{"--self-test"}, nil); err != nil {
			return "", fmt.Errorf("cached native capture self-test failed: %w", err)
		}
		b.emit("native-build-ready", "cached", nil)
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
	compilerOutput, err := b.command(ctx, 10*time.Second, "/usr/bin/xcrun", []string{"--sdk", "macosx", "--find", "swiftc"}, nil)
	if err != nil {
		return "", errors.New("existing Swift compiler is unavailable; nothing was installed")
	}
	compiler := strings.TrimSpace(string(compilerOutput))
	if !posixpath.IsAbs(compiler) || posixpath.Base(compiler) != "swiftc" {
		return "", errors.New("existing Swift compiler path is invalid")
	}
	stage = "sdk-unavailable"
	sdkOutput, err := b.command(ctx, 10*time.Second, "/usr/bin/xcrun", []string{"--sdk", "macosx", "--show-sdk-path"}, nil)
	if err != nil {
		return "", errors.New("the selected developer toolchain has no usable macOS SDK; nothing was installed or changed")
	}
	sdk := strings.TrimSpace(string(sdkOutput))
	if !posixpath.IsAbs(sdk) || !strings.HasSuffix(sdk, ".sdk") || strings.ContainsAny(sdk, "\r\n\x00") {
		return "", errors.New("the selected developer toolchain returned an invalid macOS SDK path")
	}
	stage = "cache-unavailable"
	temp, err := os.MkdirTemp(root, ".macdow-build-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp) // Only the directory created by this invocation.
	sourcePath, temporaryHelper := filepath.Join(temp, sourceName), filepath.Join(temp, binaryName)
	moduleCache := filepath.Join(temp, "modules")
	if err = os.Mkdir(moduleCache, 0700); err != nil {
		return "", err
	}
	if err = os.WriteFile(sourcePath, source, 0600); err != nil {
		return "", err
	}
	// xcrun --find returns the toolchain binary, bypassing the /usr/bin shim.
	// Supply the matching SDK explicitly instead of relying on an inherited SDKROOT.
	args := []string{"-swift-version", "5", "-O", "-num-threads", "2", "-target", "arm64-apple-macos14.0", "-sdk", sdk, "-module-cache-path", moduleCache, sourcePath, "-o", temporaryHelper}
	for _, framework := range []string{"ApplicationServices", "Foundation", "AppKit", "ScreenCaptureKit", "CoreGraphics", "CoreMedia", "CoreImage", "VideoToolbox", "ImageIO", "UniformTypeIdentifiers"} {
		args = append(args, "-framework", framework)
	}
	env := []string{"CLANG_MODULE_CACHE_PATH=" + moduleCache, "SWIFT_MODULECACHE_PATH=" + moduleCache, "SDKROOT=" + sdk}
	stage = "compile-failed"
	compileOutput, err := b.command(ctx, 3*time.Minute, compiler, args, env)
	if err != nil {
		stage, failureCounts = nativeCompileDiagnostic(compileOutput, err)
		if logPath := saveNativeBuildError(root, temp, compileOutput, err); logPath != "" {
			return "", fmt.Errorf("native capture compilation failed (local details saved to %s): %w", logPath, err)
		}
		return "", fmt.Errorf("native capture compilation failed: %w", err)
	}
	if _, err = nativeFileSHA(temporaryHelper, nativeBinaryLimit); err != nil {
		return "", fmt.Errorf("native compiler did not produce a regular capture helper: %w", err)
	}
	if err = os.Chmod(temporaryHelper, 0700); err != nil {
		return "", err
	}
	stage = "self-test-failed"
	selfTestOutput, selfTestErr := b.command(ctx, 15*time.Second, temporaryHelper, []string{"--self-test"}, nil)
	if selfTestErr != nil {
		// A crashing self-test prints the failed precondition; keep that local evidence.
		if logPath := saveNativeBuildError(root, temp, selfTestOutput, selfTestErr); logPath != "" {
			return "", fmt.Errorf("new native capture self-test failed (local details saved to %s): %w", logPath, selfTestErr)
		}
		return "", fmt.Errorf("new native capture self-test failed: %w", selfTestErr)
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
	b.emit("native-build-ready", "compiled", nil)
	return helper, nil
}

var nativeCompilerLocation = regexp.MustCompile(`(?:^|[/\\])MacDowCapture\.swift:([0-9]{1,6}):([0-9]{1,6}):\s*(?:fatal )?error:`)

// Never send compiler output to remote diagnostics. Only fixed categories and
// numeric locations in our own source can leave the Mac; full output stays local.
func nativeCompileDiagnostic(output []byte, err error) (string, map[string]int) {
	counts := map[string]int{}
	if location := nativeCompilerLocation.FindSubmatch(output); len(location) == 3 {
		line, _ := strconv.Atoi(string(location[1]))
		column, _ := strconv.Atoi(string(location[2]))
		if line > 0 && line <= 100000 && column > 0 && column <= 100000 {
			counts["compilerLine"], counts["compilerColumn"] = line, column
		}
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 && exit.ExitCode() <= 100000 {
		counts["compilerExit"] = exit.ExitCode()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "compile-timeout", counts
	}
	text := strings.ToLower(string(output))
	for _, category := range []struct {
		code    string
		markers []string
	}{
		{"compile-sdk", []string{"unable to load standard library", "no such module", "cannot load underlying module", "sdk does not contain", "unable to find sdk", "invalid sdk", "could not build module", "failed to build module", "this sdk is not supported by the compiler", "compiled with a different version"}},
		{"compile-argument", []string{"unknown argument", "unsupported option", "unknown option", "missing argument"}},
		{"compile-availability", []string{"is only available in", "has been explicitly marked unavailable", "is unavailable in"}},
		{"compile-link", []string{"link command failed", "ld: ", "undefined symbols for architecture"}},
		{"compile-symbol", []string{"cannot find", "has no member", "use of unresolved identifier"}},
		{"compile-type", []string{"cannot convert", "cannot force unwrap", "ambiguous use", "could not infer", "cannot assign", "cannot pass", "type of expression is ambiguous", "requires that", "does not conform", "must be unwrapped"}},
		{"compile-syntax", []string{"expected expression", "expected declaration", "expected '}", "expected ')", "consecutive statements", "expressions are not allowed"}},
	} {
		for _, marker := range category.markers {
			if strings.Contains(text, marker) {
				return category.code, counts
			}
		}
	}
	return "compile-failed", counts
}

func saveNativeBuildError(root, temp string, output []byte, err error) string {
	if len(output) == 0 {
		output = []byte(err.Error())
	}
	if len(output) > 16384 {
		output = output[:16384]
	}
	path := filepath.Join(root, "last-build-error.txt")
	if info, statErr := os.Lstat(path); statErr == nil && !info.Mode().IsRegular() {
		return ""
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return ""
	}
	// Rename an invocation-owned file instead of opening a shared destination,
	// so an existing link is never followed. The directory is validated earlier.
	staged := filepath.Join(temp, "compiler-error.txt")
	if os.WriteFile(staged, output, 0600) != nil || os.Rename(staged, path) != nil {
		return ""
	}
	return path
}

func (b nativeBuilder) command(ctx context.Context, timeout time.Duration, name string, args, env []string) ([]byte, error) {
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := b.run(child, name, args, env)
	if len(output) > 16384 {
		output = output[:16384]
	}
	if err != nil {
		if child.Err() != nil {
			err = child.Err()
		}
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
