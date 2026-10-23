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

func nativeTestBuild(t *testing.T, runner *nativeFakeRunner) nativeBuilder {
	t.Helper()
	encoded, digest := nativeFixture([]byte("print(\"sixty fps\")"))
	return nativeBuilder{encoded: encoded, sourceSHA: digest, cacheBase: t.TempDir(), goos: "darwin", goarch: "arm64", run: runner.run, emit: func(string, string) {}}
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
