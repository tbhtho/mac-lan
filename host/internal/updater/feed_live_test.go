package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestLiveSignedUpdates deliberately opts into the production HTTPS feed. It
// never executes downloaded programs: only a temporary copy of an alpha.14
// executable is replaced, and Mac validation stops at archive/identity/Mach-O.
// The ordinary suite skips before reading a local binary or making a request.
func TestLiveSignedUpdates(t *testing.T) {
	if os.Getenv("MACDOW_LIVE_UPDATE_TEST") != "1" {
		t.Skip("set MACDOW_LIVE_UPDATE_TEST=1 and MACDOW_LIVE_OLD_EXE to opt into isolated production-feed verification")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Fatal("this live updater verification expects a Windows x64 test runner")
	}
	const previous = "0.1.0-alpha.14"
	oldPath := os.Getenv("MACDOW_LIVE_OLD_EXE")
	if !filepath.IsAbs(oldPath) {
		t.Fatal("MACDOW_LIVE_OLD_EXE must be an absolute path to the real alpha.14 standalone EXE")
	}
	oldInfo, err := os.Lstat(oldPath)
	if err != nil || !oldInfo.Mode().IsRegular() || oldInfo.Size() <= 0 || oldInfo.Size() > MaxArchive {
		t.Fatal("old executable must be a bounded regular file", err)
	}
	oldMarker, err := ReadPackageIdentity(oldPath)
	if err != nil || oldMarker.Product != "MAC-DOW" || oldMarker.RepositoryID != BuildConfig().RepositoryID || oldMarker.Version != previous || oldMarker.OS != "windows" || oldMarker.Arch != "amd64" {
		t.Fatal("old executable is not the expected alpha.14 Windows standalone release", oldMarker, err)
	}
	originalHash := liveFileHash(t, oldPath)
	root := t.TempDir()
	install := filepath.Join(root, "isolated-install", "mac-lan.exe")
	if err = os.Mkdir(filepath.Dir(install), 0700); err != nil {
		t.Fatal(err)
	}
	copyLiveOldExecutable(t, oldPath, install)
	sentinel := filepath.Join(filepath.Dir(install), "user-neighbor-sentinel.txt")
	keep := []byte("user-owned neighboring content must survive the isolated replacement\n")
	if err = os.WriteFile(sentinel, keep, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	e, err := NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Source.(*SignedFeed); !ok {
		t.Fatal("production NewEngine did not choose the pinned signed feed")
	}
	e.Root, e.Current = filepath.Join(root, "windows-cache"), previous
	plan, err := e.Check(ctx, BuildConfig().DefaultChannel)
	if err != nil || plan.Version != BuildConfig().Version || plan.Asset.Name != "mac-lan.exe" || plan.Commit == "" {
		t.Fatal("published Windows update did not match this build", plan, err)
	}
	stage, err := e.Stage(ctx, BuildConfig().DefaultChannel)
	if err != nil || !stage.CanApply || stage.Plan.Version != BuildConfig().Version {
		t.Fatal("production Windows staging/signature verification failed", stage, err)
	}
	if _, err = e.Preflight(ctx, stage.Directory, install); err != nil {
		t.Fatal("production Windows preflight failed", err)
	}
	journal, err := e.Apply(ctx, stage.Directory, install, stage.Plan.confirmation(), false)
	if err != nil {
		t.Fatal("isolated Windows update failed", err)
	}
	installed, err := ReadPackageIdentity(install)
	if err != nil || installed.Version != BuildConfig().Version || installed.Commit != stage.Plan.Commit || installed.Product != "MAC-DOW" || installed.RepositoryID != BuildConfig().RepositoryID || installed.OS != "windows" || installed.Arch != "amd64" {
		t.Fatal("isolated installed identity differs from signed release", installed, err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || !bytes.Equal(got, keep) {
		t.Fatal("neighboring sentinel changed", err)
	}
	var transaction Transaction
	if err = readJSON(journal, &transaction); err != nil {
		t.Fatal(err)
	}
	if liveFileHash(t, transaction.Backup) != originalHash || liveFileHash(t, oldPath) != originalHash {
		t.Fatal("old release backup or untouched source executable changed")
	}
	t.Logf("Verified signed Windows %s -> %s in temporary install; source EXE and neighboring sentinel preserved; no relaunch", previous, installed.Version)

	// Use the same production constructor/key/HTTPS transport, with only the
	// requested package platform changed. No Darwin process can be launched here.
	mac, err := NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	mac.Root, mac.Current, mac.OS, mac.Arch = filepath.Join(root, "mac-cache"), previous, "darwin", "arm64"
	macPlan, err := mac.Check(ctx, BuildConfig().DefaultChannel)
	if err != nil || macPlan.Version != BuildConfig().Version || macPlan.Asset.Name != "MAC-DOW-macos-arm64-unsigned.tar.gz" {
		t.Fatal("published Mac update did not match this build", macPlan, err)
	}
	macStage, err := mac.Stage(ctx, BuildConfig().DefaultChannel)
	if err != nil {
		t.Fatal("production Mac archive staging failed", err)
	}
	archive := filepath.Join(macStage.Directory, macStage.Plan.Asset.Name)
	if err = mac.Verify(ctx, macStage.Plan.Tag, archive); err != nil {
		t.Fatal("production Mac package signature/digest verification failed", err)
	}
	macRoot := payloadRoot(filepath.Join(macStage.Directory, "preview"), macStage.Plan)
	if filepath.Base(macRoot) != "mac-lan.app" {
		t.Fatal("Mac payload is not the menu-bar app bundle", macRoot)
	}
	if err = ValidateMacAppBundle(macRoot, false); err != nil {
		t.Fatal("Mac package contains an unexpected path or layout", err)
	}
	var marker Marker
	if err = readJSON(filepath.Join(metadataRoot(macRoot), "MAC-DOW-INSTALL.json"), &marker); err != nil {
		t.Fatal(err)
	}
	if marker.Product != "MAC-DOW" || marker.RepositoryID != BuildConfig().RepositoryID || marker.Version != BuildConfig().Version || marker.Commit != macStage.Plan.Commit || marker.OS != "darwin" || marker.Arch != "arm64" || marker.Layout != "app-bundle" {
		t.Fatal("Mac package identity differs from signed release", marker)
	}
	commitBytes, err := os.ReadFile(filepath.Join(metadataRoot(macRoot), "COMMIT.txt"))
	if err != nil || strings.TrimSpace(string(commitBytes)) != marker.Commit {
		t.Fatal("Mac commit marker differs from signed release", err)
	}
	t.Logf("Verified public signed Mac %s archive, menu-bar bundle layout, commit and arm64 Mach-O headers; no Darwin execution", marker.Version)
}

func liveFileHash(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(f, MaxArchive+1)); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func copyLiveOldExecutable(t *testing.T, source, destination string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, io.LimitReader(in, MaxArchive+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal("copy old release into temporary install", copyErr, closeErr)
	}
}
