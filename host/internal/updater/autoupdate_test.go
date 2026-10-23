package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func macHostFixture() []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b[:4], 0xfeedfacf)
	binary.LittleEndian.PutUint32(b[4:8], 0x0100000c)
	binary.LittleEndian.PutUint32(b[12:16], 2)
	return append(b, []byte("synthetic Mach-O shape; never executed")...)
}

func TestPreviewMachOShapeAndDirectoryIsolation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "MacDowHost")
	for _, data := range [][]byte{[]byte("MZwrong platform executable"), make([]byte, 32)} {
		os.WriteFile(path, data, 0700)
		if validateMacHost(path, false) == nil {
			t.Fatal("non-Mach-O host accepted")
		}
	}
	good := macHostFixture()
	os.WriteFile(path, good, 0700)
	if err := validateMacHost(path, false); err != nil {
		t.Fatal(err)
	}
	wrongArch := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(wrongArch[4:8], 0x01000007)
	os.WriteFile(path, wrongArch, 0700)
	if validateMacHost(path, false) == nil {
		t.Fatal("Intel executable accepted for arm64")
	}
	os.WriteFile(path, good, 0700)
	if err := validatePreviewDirectory(root, true); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(root, "my-clips.mov")
	os.WriteFile(precious, []byte("untouched"), 0600)
	if validatePreviewDirectory(root, true) == nil {
		t.Fatal("directory with user file accepted for replacement")
	}
	if b, _ := os.ReadFile(precious); string(b) != "untouched" {
		t.Fatal("user file changed")
	}
}

func TestInstalledPreviewAllowsOnlyBoundedRegularFinderMetadata(t *testing.T) {
	root := t.TempDir()
	finder := filepath.Join(root, ".DS_Store")
	os.WriteFile(finder, []byte("Finder metadata"), 0600)
	if err := validatePreviewDirectory(root, true); err != nil {
		t.Fatal(err)
	}
	if validatePreviewDirectory(root, false) == nil {
		t.Fatal("Finder metadata accepted as signed payload content")
	}
	large, err := os.OpenFile(finder, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = large.Truncate((1 << 20) + 1)
	large.Close()
	if err != nil {
		t.Fatal(err)
	}
	if validatePreviewDirectory(root, true) == nil {
		t.Fatal("oversized Finder metadata accepted")
	}
	os.Remove(finder)
	os.Mkdir(finder, 0700)
	if validatePreviewDirectory(root, true) == nil {
		t.Fatal("directory named Finder metadata accepted")
	}
	if home, err := os.UserHomeDir(); err == nil && rejectBroadPreviewDirectory(filepath.Join(home, "Downloads")) == nil {
		t.Fatal("broad Downloads folder accepted")
	}
}

func TestPreparedRunnerCopiesExactCurrentBytesAndRejectsLinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Root: filepath.Join(base, "updates")}
	exe := filepath.Join(base, "host")
	data := []byte("current executable fixture; never executed")
	os.WriteFile(exe, data, 0700)
	runner, err := e.prepareRunner(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !e.owned(filepath.Dir(runner)) {
		t.Fatal("worker escaped cache")
	}
	if b, _ := os.ReadFile(runner); !bytes.Equal(data, b) {
		t.Fatal("worker bytes changed")
	}
	link := filepath.Join(base, "linked-host")
	if os.Symlink(exe, link) == nil {
		if _, err := e.prepareRunner(link); err == nil {
			t.Fatal("symlink executable accepted")
		}
	}
}

func previewArchiveFixture(t *testing.T, version, commit string) []byte {
	t.Helper()
	m, _ := json.Marshal(Marker{Product: "MAC-DOW", RepositoryID: BuildConfig().RepositoryID, Version: version, Commit: commit, Layout: "commandline-preview", OS: "darwin", Arch: "arm64"})
	files := map[string][]byte{"MacDowHost": macHostFixture(), "MAC-DOW-INSTALL.json": m, "COMMIT.txt": []byte(commit), "README.md": []byte("fixture")}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		mode := int64(0600)
		if name == "MacDowHost" {
			mode = 0700
		}
		if err := tw.WriteHeader(&tar.Header{Name: "macos-arm64/" + name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

func TestPreviewStageApplyAndBackup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable-mode and directory replacement acceptance requires Unix")
	}
	e, install, src := newFixture(t, true)
	e.OS, e.Arch = "darwin", "arm64"
	os.Remove(filepath.Join(install, "MAC-DOW.exe"))
	commit := strings.Repeat("a", 40)
	old := Marker{Product: "MAC-DOW", RepositoryID: BuildConfig().RepositoryID, Version: "0.1.0", Commit: commit, Layout: "commandline-preview", OS: "darwin", Arch: "arm64"}
	writeJSON(filepath.Join(install, "MAC-DOW-INSTALL.json"), old)
	os.WriteFile(filepath.Join(install, "MacDowHost"), macHostFixture(), 0700)
	os.WriteFile(filepath.Join(install, "COMMIT.txt"), []byte(commit), 0600)
	neighbor := filepath.Join(filepath.Dir(install), "personal-movie.mov")
	os.WriteFile(neighbor, []byte("keep"), 0600)
	src.Data = previewArchiveFixture(t, "0.2.0", commit)
	digest := sha256.Sum256(src.Data)
	src.R.Assets = []Asset{{ID: 12, Name: "MAC-DOW-macos-arm64-commandline-preview.tar.gz", Size: int64(len(src.Data)), Digest: "sha256:" + hex.EncodeToString(digest[:])}}
	s, err := e.PrepareAutomaticUpdate(context.Background(), install)
	if err != nil || !s.CanApply {
		t.Fatal(s, err)
	}
	journal, err := e.Apply(context.Background(), s.Directory, install, s.Plan.confirmation(), false)
	if err != nil {
		t.Fatal(err)
	}
	var next Marker
	if err := readJSON(filepath.Join(install, "MAC-DOW-INSTALL.json"), &next); err != nil || next.Version != "0.2.0" {
		t.Fatal(next, err)
	}
	var tx Transaction
	if err := readJSON(journal, &tx); err != nil || tx.State != "applied" {
		t.Fatal(tx, err)
	}
	var before Marker
	if err := readJSON(filepath.Join(tx.Backup, "MAC-DOW-INSTALL.json"), &before); err != nil || before.Version != "0.1.0" {
		t.Fatal("backup lost", before, err)
	}
	if b, _ := os.ReadFile(neighbor); string(b) != "keep" {
		t.Fatal("neighbor changed")
	}
}

func TestMacRelaunchStartsFreshHostNotUpdaterMode(t *testing.T) {
	command, args := launchCommand("/Users/example/Downloads/macos-arm64", "darwin")
	if command != filepath.Join("/Users/example/Downloads/macos-arm64", "MacDowHost") || len(args) != 0 {
		t.Fatal(command, args)
	}
}

func TestAutomaticPendingIsReusedOnlyAfterFreshVerification(t *testing.T) {
	e, install, source := newFixture(t, true)
	first, err := e.PrepareAutomaticUpdate(context.Background(), install)
	if err != nil || !first.CanApply {
		t.Fatal(first, err)
	}
	source.Fail = true // A second download would fail: reuse the verified archive.
	second, err := e.PrepareAutomaticUpdate(context.Background(), install)
	if err != nil || second.Directory != first.Directory || !second.CanApply {
		t.Fatal("pending download not reused", second, err)
	}
	e.Verify = func(context.Context, string, string) error { return ErrProvenance }
	if _, err = e.PrepareAutomaticUpdate(context.Background(), install); err == nil {
		t.Fatal("cached CanApply bypassed fresh signature verification")
	}
	e.Verify = func(context.Context, string, string) error { return nil }
	source.Fail = false
	os.WriteFile(filepath.Join(first.Directory, first.Plan.Asset.Name), []byte("tampered cached archive"), 0600)
	third, err := e.PrepareAutomaticUpdate(context.Background(), install)
	if err != nil || !third.CanApply || third.Directory == first.Directory {
		t.Fatal("tampered pending archive was reused", third, err)
	}
	source.R = fixtureRelease("v0.1.0", false)
	entries, _ := os.ReadDir(e.Root)
	if _, err = e.PrepareAutomaticUpdate(context.Background(), install); err != ErrNoUpdate {
		t.Fatal(err)
	}
	after, _ := os.ReadDir(e.Root)
	if len(after) != len(entries) {
		t.Fatal("current version created a needless stage")
	}
}
