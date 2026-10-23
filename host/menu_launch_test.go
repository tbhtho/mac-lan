package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tbhtho/MAC-DOW/host/internal/updater"
)

func TestMenuBuilderHasSeparateExecutableAndVerifiedCache(t *testing.T) {
	runner := &nativeFakeRunner{}
	b := nativeTestBuild(t, runner)
	b.menu = true
	binaryPath, err := b.resolve(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(binaryPath) != "MacLanMenu" {
		t.Fatal("menu reused the capture executable name", binaryPath)
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), "main.swift") {
		t.Fatal("menu source must compile as a Swift entry point")
	}
	runner.calls = nil
	again, err := b.resolve(context.Background(), "")
	if err != nil || again != binaryPath || len(runner.calls) != 1 || !strings.Contains(runner.calls[0], "--self-test") {
		t.Fatal("verified menu cache was not reused", err, runner.calls)
	}
	if err = os.WriteFile(binaryPath, []byte("tampered menu"), 0700); err != nil {
		t.Fatal(err)
	}
	runner.calls = nil
	if _, err = b.resolve(context.Background(), ""); err != nil || len(runner.calls) < 4 {
		t.Fatal("tampered menu was accepted", err, runner.calls)
	}
}

func TestMenuAppRootSelectsOnlyBundleEntryPoint(t *testing.T) {
	root := filepath.Join(menuTempDir(t), "mac-lan.app")
	if appRootForExecutable(filepath.Join(root, "Contents", "MacOS", "mac-lan")) != root {
		t.Fatal("bundle entry point not recognized")
	}
	for _, path := range []string{filepath.Join(root, "Contents", "Helpers", "MacDowHost"), filepath.Join(root, "mac-lan"), filepath.Join(menuTempDir(t), "MacDowHost")} {
		if appRootForExecutable(path) != "" {
			t.Fatal("host/helper was mistaken for the app entry point")
		}
	}
}

func menuPreviewFixture(t *testing.T) string {
	t.Helper()
	root := menuTempDir(t)
	header := make([]byte, 64)
	binary.LittleEndian.PutUint32(header, 0xfeedfacf)
	binary.LittleEndian.PutUint32(header[4:], 0x0100000c)
	binary.LittleEndian.PutUint32(header[12:], 2)
	if err := os.WriteFile(filepath.Join(root, "MacDowHost"), header, 0700); err != nil {
		t.Fatal(err)
	}
	marker := updater.Marker{Product: "MAC-DOW", RepositoryID: 1430459188, Version: updater.BuildConfig().Version, Commit: strings.Repeat("a", 40), OS: "darwin", Arch: "arm64", Layout: "commandline-preview"}
	data, _ := json.Marshal(marker)
	for name, bytes := range map[string][]byte{"MAC-DOW-INSTALL.json": data, "COMMIT.txt": []byte(marker.Commit), "BUILD-PROVENANCE.json": []byte(`{"publicUpdate":true}`), "LICENSE": []byte("license"), "THIRD_PARTY_NOTICES.md": []byte("notices"), "README.md": []byte("readme")} {
		if err := os.WriteFile(filepath.Join(root, name), bytes, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "licenses"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "licenses", "go-LICENSE.txt"), []byte("go license"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPreviewInstallsMenuAppWithoutChangingSourceOrNeighbors(t *testing.T) {
	preview := menuPreviewFixture(t)
	destination := filepath.Join(menuTempDir(t), "Applications", "mac-lan.app")
	if err := os.WriteFile(filepath.Join(preview, "user-clip.mp4"), []byte("leave this file"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := installMenuApp(preview, destination)
	if err != nil || got != destination {
		t.Fatal("app migration failed", got, err)
	}
	for _, name := range []string{"Contents/MacOS/mac-lan", "Contents/Helpers/MacDowHost", "Contents/Info.plist", "Contents/Resources/LICENSE", "Contents/Resources/licenses/go-LICENSE.txt"} {
		if _, err := os.Stat(filepath.Join(destination, filepath.FromSlash(name))); err != nil {
			t.Fatal("missing app content", name, err)
		}
	}
	var marker updater.Marker
	data, err := os.ReadFile(filepath.Join(destination, "Contents", "Resources", "MAC-DOW-INSTALL.json"))
	if err != nil || json.Unmarshal(data, &marker) != nil || marker.Layout != "app-bundle" || marker.Commit != strings.Repeat("a", 40) {
		t.Fatal("app lost its release identity", marker, err)
	}
	if data, err := os.ReadFile(filepath.Join(preview, "user-clip.mp4")); err != nil || string(data) != "leave this file" {
		t.Fatal("migration touched a neighboring file")
	}
	if _, err := os.Stat(filepath.Join(preview, "MacDowHost")); err != nil {
		t.Fatal("migration removed original host")
	}
	if _, err := os.Stat(filepath.Join(destination, "Contents", "Resources", "user-clip.mp4")); !os.IsNotExist(err) {
		t.Fatal("migration copied unrelated content")
	}
	if again, err := installMenuApp(preview, destination); err != nil || again != destination {
		t.Fatal("repeated launch did not reuse installed app", err)
	}
}

func TestPreviewMigrationNeverReplacesUnrelatedOrMismatchedApp(t *testing.T) {
	preview := menuPreviewFixture(t)
	parent := menuTempDir(t)
	destination := filepath.Join(parent, "mac-lan.app")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(destination, "user-data")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := installMenuApp(preview, destination); err == nil {
		t.Fatal("replaced unrelated application")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("existing app changed")
	}
	markerPath := filepath.Join(preview, "MAC-DOW-INSTALL.json")
	data, _ := os.ReadFile(markerPath)
	data = []byte(strings.Replace(string(data), `"repositoryId":1430459188`, `"repositoryId":1`, 1))
	if err := os.WriteFile(markerPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := installMenuApp(preview, filepath.Join(parent, "other.app")); err == nil {
		t.Fatal("installed a different repository's package")
	}
}

// Opt-in packaging check installs actual ARM64 release bytes into a temporary
// app folder. It never opens the app or executes any downloaded/native code.
func TestReleaseMenuBridge(t *testing.T) {
	root := os.Getenv("MACDOW_TEST_MENU_PACKAGE")
	if root == "" {
		t.Skip("set MACDOW_TEST_MENU_PACKAGE to the extracted release compatibility package")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("release package path must be absolute")
	}
	info, err := os.ReadFile("../native/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	previous := NativeMenuInfoBase64
	NativeMenuInfoBase64 = base64.StdEncoding.EncodeToString(info)
	defer func() { NativeMenuInfoBase64 = previous }()
	destination := filepath.Join(menuTempDir(t), "Applications", "mac-lan.app")
	got, err := installMenuApp(root, destination)
	if err != nil || got != destination {
		t.Fatal("real release migration failed", err)
	}
	sourceHash, err := nativeFileSHA(filepath.Join(root, "MacDowHost"), nativeBinaryLimit)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Contents/MacOS/mac-lan", "Contents/Helpers/MacDowHost"} {
		hash, err := nativeFileSHA(filepath.Join(destination, filepath.FromSlash(name)), nativeBinaryLimit)
		if err != nil || hash != sourceHash {
			t.Fatal("migrated executable differs from release", name, err)
		}
	}
	t.Log("Actual compatibility host installed a complete validated temporary .app; no native execution or OS app registration")
}

func menuTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
