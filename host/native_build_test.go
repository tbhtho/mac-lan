package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeFixture(source []byte) (string, string) {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(source)
	w.Close()
	digest := sha256.Sum256(source)
	return base64.StdEncoding.EncodeToString(b.Bytes()), hex.EncodeToString(digest[:])
}

func TestEmbeddedNativeSourceValidation(t *testing.T) {
	encoded, digest := nativeFixture([]byte("print(\"capture source\")"))
	if got, err := decodeNativeSource(encoded, digest); err != nil || string(got) != "print(\"capture source\")" {
		t.Fatalf("valid source: %q %v", got, err)
	}
	oversized, oversizedDigest := nativeFixture(bytes.Repeat([]byte("x"), nativeSourceLimit+1))
	for _, tc := range []struct{ encoded, digest string }{
		{"bad", digest}, {encoded, strings.Repeat("0", 64)}, {encoded, "bad"},
		{oversized, oversizedDigest}, {strings.Repeat("A", nativeEncodedLimit+1), digest},
	} {
		if _, err := decodeNativeSource(tc.encoded, tc.digest); err == nil {
			t.Fatal("accepted malformed, oversized, or incorrectly hashed source")
		}
	}
}

type nativeFakeRunner struct {
	calls     []string
	failStage string
}

func (f *nativeFakeRunner) run(_ context.Context, name string, args, _ []string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if strings.Contains(name, f.failStage) && f.failStage != "" {
		return []byte("local compiler detail"), errors.New("command failed")
	}
	switch name {
	case "/usr/bin/xcode-select":
		return []byte("/ExistingToolchain\n"), nil
	case "/usr/bin/xcrun":
		if strings.Contains(strings.Join(args, " "), "--show-sdk-path") {
			return []byte("/Existing SDKs/MacOSX.sdk\n"), nil
		}
		return []byte("/ExistingToolchain/usr/bin/swiftc\n"), nil
	case "/ExistingToolchain/usr/bin/swiftc":
		for i, arg := range args {
			if arg == "-o" && i+1 < len(args) {
				return nil, os.WriteFile(args[i+1], []byte("new sixty fps capture helper"), 0700)
			}
		}
		return nil, errors.New("missing output")
	default:
		if len(args) == 1 && args[0] == "--self-test" {
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	}
}

func TestNativeBuildUsesSelectedMacOSSDK(t *testing.T) {
	r := &nativeFakeRunner{}
	b := nativeTestBuild(t, r)
	b.run = func(ctx context.Context, name string, args, env []string) ([]byte, error) {
		if strings.HasSuffix(name, "/swiftc") {
			// The actual toolchain binary cannot rely on the /usr/bin/swiftc shim
			// to find Darwin/Foundation headers. Reject an implicit or stale SDK.
			hasSDK := false
			for i, arg := range args {
				if arg == "-sdk" && i+1 < len(args) && args[i+1] == "/Existing SDKs/MacOSX.sdk" {
					hasSDK = true
				}
			}
			if !hasSDK || !strings.Contains(strings.Join(env, "\n"), "SDKROOT=/Existing SDKs/MacOSX.sdk") {
				return []byte("error: unable to load standard library for target 'arm64-apple-macos14.0'"), errors.New("missing selected SDK")
			}
		}
		return r.run(ctx, name, args, env)
	}
	if _, err := b.resolve(context.Background(), "/old/MacDowCapture"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, call := range r.calls {
		if call == "/usr/bin/xcrun --sdk macosx --show-sdk-path" {
			found = true
		}
	}
	if !found {
		t.Fatal("did not ask the selected developer toolchain for its macOS SDK")
	}
}

func TestNativeCompilerDiagnosticRetainsOnlyFixedCategoryAndLocation(t *testing.T) {
	for _, tc := range []struct {
		output, code string
		line, column int
	}{
		{"/Users/private-person/Library/Caches/MAC-DOW/native/hash/MacDowCapture.swift:167:18: error: cannot convert value of type 'SecretThing' to expected argument type 'OtherThing'", "compile-type", 167, 18},
		{"MacDowCapture.swift:139:22: error: cannot find 'PrivateSymbol' in scope", "compile-symbol", 139, 22},
		{"<unknown>:0: error: unable to load standard library for target 'arm64-apple-macos14.0'", "compile-sdk", 0, 0},
		{"error: this SDK is not supported by the compiler (the SDK is built with private version)", "compile-sdk", 0, 0},
		{"error: unknown argument: '-private-argument'", "compile-argument", 0, 0},
		{"MacDowCapture.swift:123:7: error: 'someAPI' is only available in macOS 15.0 or newer", "compile-availability", 123, 7},
		{"MacDowCapture.swift:15:2: error: expected expression", "compile-syntax", 15, 2},
		{"ld: library not found for -lprivate\nerror: link command failed with exit code 1", "compile-link", 0, 0},
		{"secret username token private-filename.mp4", "compile-failed", 0, 0},
	} {
		code, counts := nativeCompileDiagnostic([]byte(tc.output), errors.New("private process failure"))
		if code != tc.code || counts["compilerLine"] != tc.line || counts["compilerColumn"] != tc.column {
			t.Fatalf("unexpected safe classification: %s %+v", code, counts)
		}
		for key := range counts {
			if key != "compilerLine" && key != "compilerColumn" && key != "compilerExit" {
				t.Fatalf("unexpected diagnostic field %q", key)
			}
		}
	}
	if code, _ := nativeCompileDiagnostic(nil, context.DeadlineExceeded); code != "compile-timeout" {
		t.Fatal("compiler deadline was not classified")
	}
}

func nativeTestBuild(t *testing.T, runner *nativeFakeRunner) nativeBuilder {
	t.Helper()
	encoded, digest := nativeFixture([]byte("print(\"sixty fps\")"))
	return nativeBuilder{encoded: encoded, sourceSHA: digest, cacheBase: t.TempDir(), goos: "darwin", goarch: "arm64", run: runner.run, emit: func(string, string, map[string]int) {}}
}

func TestNativeCompileFailurePersistsBoundedLocalOutputAndReportsNoText(t *testing.T) {
	r := &nativeFakeRunner{}
	b := nativeTestBuild(t, r)
	var failedCode string
	var failedCounts map[string]int
	b.emit = func(event, code string, counts map[string]int) {
		if event == "native-build-failed" {
			failedCode, failedCounts = code, counts
		}
	}
	private := "/Users/private-user/Library/Caches/MacDowCapture.swift:167:18: error: cannot convert private-secret-token\n" + strings.Repeat("source and local paths\n", 2000)
	b.run = func(ctx context.Context, name string, args, env []string) ([]byte, error) {
		if strings.HasSuffix(name, "/swiftc") {
			return []byte(private), errors.New("exit 1")
		}
		return r.run(ctx, name, args, env)
	}
	if _, err := b.resolve(context.Background(), "old"); err == nil {
		t.Fatal("failed compiler accepted")
	}
	if failedCode != "compile-type" || failedCounts["compilerLine"] != 167 || failedCounts["compilerColumn"] != 18 {
		t.Fatalf("lost safe compiler information: %s %+v", failedCode, failedCounts)
	}
	logPath := filepath.Join(b.cacheBase, "MAC-DOW", "native", b.sourceSHA, "last-build-error.txt")
	log, err := os.ReadFile(logPath)
	if err != nil || len(log) != 16384 || !strings.Contains(string(log), "private-secret-token") {
		t.Fatalf("bounded full compiler detail missing from local log: size=%d %v", len(log), err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(logPath), "MacDowCapture")); !os.IsNotExist(err) {
		t.Fatal("published a failed compiler output")
	}
}

func TestNativeBuildRejectsUnavailableOrInvalidSDK(t *testing.T) {
	for _, sdk := range []string{"", "relative/MacOSX.sdk", "/SDK/MacOSX.sdk\n/other"} {
		r := &nativeFakeRunner{}
		b := nativeTestBuild(t, r)
		var failedCode string
		b.emit = func(event, code string, _ map[string]int) {
			if event == "native-build-failed" {
				failedCode = code
			}
		}
		b.run = func(ctx context.Context, name string, args, env []string) ([]byte, error) {
			if name == "/usr/bin/xcrun" && strings.Contains(strings.Join(args, " "), "--show-sdk-path") {
				return []byte(sdk), nil
			}
			return r.run(ctx, name, args, env)
		}
		if _, err := b.resolve(context.Background(), "old"); err == nil || failedCode != "sdk-unavailable" {
			t.Fatalf("accepted invalid SDK %q: %v %s", sdk, err, failedCode)
		}
		for _, call := range r.calls {
			if strings.HasPrefix(call, "/ExistingToolchain/usr/bin/swiftc ") {
				t.Fatal("compiler ran without valid SDK")
			}
		}
	}
}

func TestNativeBuildRequiresExistingToolchainAndNeverUsesOldHelper(t *testing.T) {
	for _, stage := range []string{"xcode-select", "xcrun", "swiftc"} {
		r := &nativeFakeRunner{failStage: stage}
		b := nativeTestBuild(t, r)
		path, err := b.resolve(context.Background(), "/old/MacDowCapture")
		if err == nil || path != "" {
			t.Fatalf("%s unexpectedly fell back: %q %v", stage, path, err)
		}
		for _, cmd := range r.calls {
			if strings.Contains(cmd, "--install") || strings.Contains(cmd, "--check") || strings.Contains(cmd, "--stream") || strings.Contains(cmd, "/old/") {
				t.Fatalf("build queried capture/permissions or installed tools: %s", cmd)
			}
		}
		if stage == "swiftc" && !strings.Contains(err.Error(), "local compiler detail") {
			t.Fatal("local compiler details lost")
		}
	}
}

func TestNativeBuildValidatesCacheAndRebuildsTamperedHelper(t *testing.T) {
	r := &nativeFakeRunner{}
	b := nativeTestBuild(t, r)
	path, err := b.resolve(context.Background(), "/old/MacDowCapture")
	if err != nil {
		t.Fatal(err)
	}
	if path == "/old/MacDowCapture" {
		t.Fatal("used old helper")
	}
	firstCalls := len(r.calls)
	again, err := b.resolve(context.Background(), "/old/MacDowCapture")
	if err != nil || again != path {
		t.Fatalf("cache reuse failed: %v", err)
	}
	if len(r.calls) != firstCalls+1 || !strings.HasSuffix(r.calls[len(r.calls)-1], " --self-test") {
		t.Fatal("cache was recompiled or not self-tested")
	}
	if err := os.WriteFile(path, []byte("tampered helper"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := b.resolve(context.Background(), "/old/MacDowCapture"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) <= firstCalls+2 {
		t.Fatal("tampered helper was reused")
	}
	for _, cmd := range r.calls {
		if strings.Contains(cmd, " --stream") || strings.Contains(cmd, " --check") {
			t.Fatal("bootstrap captured or asked permissions")
		}
	}
}

func TestNativeBuildRejectsSymlinkCache(t *testing.T) {
	r := &nativeFakeRunner{}
	b := nativeTestBuild(t, r)
	if err := os.Symlink(t.TempDir(), filepath.Join(b.cacheBase, "MAC-DOW")); err != nil {
		t.Skip("symlink creation unavailable")
	}
	if _, err := b.resolve(context.Background(), "old"); err == nil {
		t.Fatal("followed cache symlink")
	}
	if len(r.calls) != 0 {
		t.Fatal("ran toolchain before validating cache")
	}
}

func TestNativeBuildAbsentSourcePreservesNativeSibling(t *testing.T) {
	b := nativeBuilder{}
	if got, err := b.resolve(context.Background(), "native-sibling"); err != nil || got != "native-sibling" {
		t.Fatalf("%q %v", got, err)
	}
	b.sourceSHA = strings.Repeat("0", 64)
	if _, err := b.resolve(context.Background(), "native-sibling"); err == nil {
		t.Fatal("accepted partial embedded configuration")
	}
}

func TestNativeBuildSelfTestFailureDoesNotPublish(t *testing.T) {
	r := &nativeFakeRunner{failStage: "MacDowCapture"}
	b := nativeTestBuild(t, r)
	if _, err := b.resolve(context.Background(), "old"); err == nil {
		t.Fatal("failed self-test accepted")
	}
	final := filepath.Join(b.cacheBase, "MAC-DOW", "native", b.sourceSHA, "MacDowCapture")
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatal("failed helper was published")
	}
}

func TestNativeBuildCleansOnlyStaleOwnTemporaryDirectories(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, ".macdow-build-stale")
	fresh := filepath.Join(root, ".macdow-build-fresh")
	foreign := filepath.Join(root, "keep-me")
	for _, p := range []string{stale, fresh, foreign} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(stale, old, old)
	cleanupNativeTemps(root)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale own build directory retained")
	}
	for _, p := range []string{fresh, foreign} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("removed current/foreign directory: %v", err)
		}
	}
}
