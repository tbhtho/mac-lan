package updater

import (
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

func standaloneFixture(t *testing.T, version, commit string) []byte {
	t.Helper()
	m := map[string]any{"product": "MAC-DOW", "repositoryId": BuildConfig().RepositoryID, "version": version, "os": "windows", "arch": "amd64", "commit": commit, "layout": "single-exe"}
	b, _ := json.Marshal(m)
	data := append([]byte("MZsynthetic fixture, never executed\n"), b...)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(b)))
	return append(data, []byte("MCDOWPK1")...)
}
func TestRenamedStandaloneApplyPreservesInstalledPathAndNeighbors(t *testing.T) {
	for _, installedName := range []string{"mac-lan.exe", "MAC-DOW.exe"} {
		t.Run(installedName, func(t *testing.T) {
			e, legacy, src := newFixture(t, true)
			install := filepath.Join(filepath.Dir(legacy), installedName)
			old := standaloneFixture(t, "0.1.0", src.SHA)
			if err := os.WriteFile(install, old, 0600); err != nil {
				t.Fatal(err)
			}
			sibling := filepath.Join(filepath.Dir(install), "settings.json")
			if err := os.WriteFile(sibling, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			src.Data = standaloneFixture(t, "0.2.0", src.SHA)
			digest := sha256.Sum256(src.Data)
			src.R.Assets[0] = Asset{ID: 12, Name: "mac-lan.exe", Size: int64(len(src.Data)), Digest: "sha256:" + hex.EncodeToString(digest[:])}
			stage, err := e.Stage(context.Background(), "stable")
			if err != nil || !stage.CanApply {
				t.Fatal("renamed standalone staging", stage, err)
			}
			if filepath.Base(payloadRoot(filepath.Join(stage.Directory, "preview"), stage.Plan)) != "mac-lan.exe" {
				t.Fatal("staged filename lost")
			}
			journal, err := e.Apply(context.Background(), stage.Directory, install, stage.Plan.confirmation(), false)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(install); string(got) != string(src.Data) {
				t.Fatal("actual installed path not replaced")
			}
			if got, _ := os.ReadFile(sibling); string(got) != "preserve" {
				t.Fatal("neighbor changed")
			}
			var tx Transaction
			if err := readJSON(journal, &tx); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(tx.Backup); string(got) != string(old) {
				t.Fatal("backup changed")
			}
		})
	}
}
func TestStandaloneApplyReplacesOnlyExecutable(t *testing.T) {
	e, legacy, src := newFixture(t, true)
	install := filepath.Join(filepath.Dir(legacy), "MAC-DOW.exe")
	old := standaloneFixture(t, "0.1.0", src.SHA)
	os.WriteFile(install, old, 0600)
	sibling := filepath.Join(filepath.Dir(install), "precious-user-file.txt")
	os.WriteFile(sibling, []byte("keep"), 0600)
	src.Data = standaloneFixture(t, "0.2.0", src.SHA)
	digest := sha256.Sum256(src.Data)
	src.R.Assets[0] = Asset{ID: 12, Name: "MAC-DOW.exe", Size: int64(len(src.Data)), Digest: "sha256:" + hex.EncodeToString(digest[:])}
	s, err := e.Stage(context.Background(), "stable")
	if err != nil || !s.CanApply {
		t.Fatal("standalone staging", s, err)
	}
	journal, err := e.Apply(context.Background(), s.Directory, install, s.Plan.confirmation(), false)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(install); string(b) != string(src.Data) {
		t.Fatal("executable not replaced")
	}
	if b, _ := os.ReadFile(sibling); string(b) != "keep" {
		t.Fatal("sibling was changed")
	}
	var tx Transaction
	if err = readJSON(journal, &tx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(tx.Backup); string(b) != string(old) {
		t.Fatal("previous executable not retained")
	}
	e.Current = "0.2.0"
	src.Data = old
	digest = sha256.Sum256(old)
	src.R = fixtureRelease("v0.1.0", false)
	src.R.Assets[0] = Asset{ID: 13, Name: "MAC-DOW.exe", Size: int64(len(old)), Digest: "sha256:" + hex.EncodeToString(digest[:])}
	if err = e.Rollback(context.Background(), journal, install, "ROLLBACK", false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(install); string(b) != string(old) {
		t.Fatal("single EXE rollback failed")
	}
}
func TestStandaloneIdentityAndCommitAreRequired(t *testing.T) {
	e, _, src := newFixture(t, true)
	p := Plan{Version: "0.2.0", OS: "windows", Arch: "amd64", Commit: src.SHA, Asset: Asset{Name: "MAC-DOW.exe"}}
	for _, data := range [][]byte{[]byte("MZnot identified"), standaloneFixture(t, "0.2.0", strings.Repeat("b", 40)), standaloneFixture(t, "0.1.0", src.SHA)} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "MAC-DOW.exe"), data, 0600)
		if e.validatePayload(root, p) == nil {
			t.Fatal("unrecognized/wrong version/wrong commit accepted")
		}
	}
}
func TestStandaloneNeverReplacesLegacyFolder(t *testing.T) {
	e, install, src := newFixture(t, true)
	src.Data = standaloneFixture(t, "0.2.0", src.SHA)
	digest := sha256.Sum256(src.Data)
	src.R.Assets[0] = Asset{ID: 12, Name: "MAC-DOW.exe", Size: int64(len(src.Data)), Digest: "sha256:" + hex.EncodeToString(digest[:])}
	s, err := e.Stage(context.Background(), "stable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apply(context.Background(), s.Directory, install, s.Plan.confirmation(), false); err == nil {
		t.Fatal("legacy directory replaced with standalone file")
	}
	if info, err := os.Stat(install); err != nil || !info.IsDir() {
		t.Fatal("legacy directory changed")
	}
}
func TestAppRelaunchUsesFinderAndStaysIdle(t *testing.T) {
	command, args := launchCommand("/Applications/MAC-DOW Host.app", "darwin")
	if command != "/usr/bin/open" || len(args) != 1 || args[0] != "/Applications/MAC-DOW Host.app" {
		t.Fatal(command, args)
	}
}
func TestAppBundlePayload(t *testing.T) {
	e, _, src := newFixture(t, true)
	e.OS = "darwin"
	e.Arch = "arm64"
	root := t.TempDir()
	bundle := filepath.Join(root, "MAC-DOW Host.app")
	resources := filepath.Join(bundle, "Contents", "Resources")
	os.MkdirAll(resources, 0700)
	// Native bundles already used app-bundle before the menu-bar executable was
	// renamed. Their marker must not route them through the new bundle validator.
	marker := Marker{Product: "MAC-DOW", RepositoryID: BuildConfig().RepositoryID, Version: "0.2.0", OS: "darwin", Arch: "arm64", Commit: src.SHA, Layout: "app-bundle"}
	writeJSON(filepath.Join(resources, "MAC-DOW-INSTALL.json"), marker)
	os.WriteFile(filepath.Join(resources, "COMMIT.txt"), []byte(src.SHA), 0600)
	for _, name := range []string{"MacOS/MAC-DOW Host", "Helpers/MacDowHost", "Helpers/MacDowCapture"} {
		path := filepath.Join(bundle, "Contents", filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte("synthetic"), 0700)
	}
	os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte("fixture"), 0600)
	p := Plan{Version: "0.2.0", Commit: src.SHA, OS: "darwin", Arch: "arm64", Asset: Asset{Name: "MAC-DOW-macos-arm64-unsigned.tar.gz"}}
	if payloadRoot(root, p) != bundle {
		t.Fatal("not updating bundle as a unit")
	}
	if runtime.GOOS == "windows" { // Windows cannot set POSIX executable bits; native acceptance stays separate.
		if err := e.validatePayload(root, p); err == nil {
			t.Fatal("Windows fixture unexpectedly has executable modes")
		}
		e.Current = "0.2.0"
		if err := e.installIdentity(bundle); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := e.validatePayload(root, p); err != nil {
		t.Fatal(err)
	}
	if payloadRoot(root, p) != bundle {
		t.Fatal("not updating bundle as a unit")
	}
	e.Current = "0.2.0"
	if err := e.installIdentity(bundle); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(bundle, "Contents", "Helpers", "MacDowCapture"))
	if e.validatePayload(root, p) == nil {
		t.Fatal("bundle missing capture helper accepted")
	}
}
