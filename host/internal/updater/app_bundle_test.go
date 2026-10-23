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
	"strings"
	"testing"
)

const menuInfoFixture = `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.tbhtho.macdow.host</string><key>CFBundleExecutable</key><string>mac-lan</string><key>CFBundleName</key><string>mac-lan</string><key>LSUIElement</key><true/></dict></plist>`

func menuAppFiles(t *testing.T, version, commit string) map[string][]byte {
	t.Helper()
	host := make([]byte, 64)
	binary.LittleEndian.PutUint32(host, 0xfeedfacf)
	binary.LittleEndian.PutUint32(host[4:], 0x0100000c)
	binary.LittleEndian.PutUint32(host[12:], 2)
	marker, err := json.Marshal(Marker{Product: "MAC-DOW", RepositoryID: 1430459188, Version: version, OS: "darwin", Arch: "arm64", Commit: commit, Layout: "app-bundle"})
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{
		"Contents/MacOS/mac-lan": host, "Contents/Helpers/MacDowHost": host,
		"Contents/Info.plist":                      []byte(menuInfoFixture),
		"Contents/Resources/MAC-DOW-INSTALL.json":  marker,
		"Contents/Resources/COMMIT.txt":            []byte(commit),
		"Contents/Resources/BUILD-PROVENANCE.json": []byte(`{"publicUpdate":true}`),
	}
}

func writeMenuApp(t *testing.T, parent, version, commit string) string {
	t.Helper()
	app := filepath.Join(parent, "mac-lan.app")
	for name, data := range menuAppFiles(t, version, commit) {
		dest := filepath.Join(app, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

func menuAppArchive(t *testing.T, version, commit string) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tarball := tar.NewWriter(gz)
	for name, data := range menuAppFiles(t, version, commit) {
		if err := tarball.WriteHeader(&tar.Header{Name: "mac-lan.app/" + name, Size: int64(len(data)), Mode: 0755, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarball.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestMenuAppPayloadAndInstalledIdentity(t *testing.T) {
	e, _, source := newFixture(t, true)
	e.OS, e.Arch, e.Current = "darwin", "arm64", "0.2.0"
	root := t.TempDir()
	app := writeMenuApp(t, root, "0.2.0", source.SHA)
	p := Plan{Version: "0.2.0", Commit: source.SHA, OS: "darwin", Arch: "arm64", Asset: Asset{Name: "MAC-DOW-macos-arm64-unsigned.tar.gz"}}
	if payloadRoot(root, p) != app {
		t.Fatal("menu app was not selected as the replacement unit")
	}
	if err := e.validatePayload(root, p); err != nil {
		t.Fatal(err)
	}
	if err := e.installIdentity(app); err != nil {
		t.Fatal(err)
	}
	// An unrelated sibling must not disappear when payloadRoot selects one app.
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.validatePayload(root, p); err == nil {
		t.Fatal("accepted unrelated archive root")
	}
}

func TestMenuAppRejectsWrongHostBrandingMetadataAndExtraFiles(t *testing.T) {
	for _, test := range []struct {
		name, file string
		contents   []byte
	}{
		{"wrong executable", "Contents/MacOS/mac-lan", []byte("not Mach-O")},
		{"wrong architecture", "Contents/Helpers/MacDowHost", bytes.Repeat([]byte{0}, 64)},
		{"wrong bundle identifier", "Contents/Info.plist", []byte(strings.Replace(menuInfoFixture, "com.tbhtho.macdow.host", "other.app", 1))},
		{"wrong executable name", "Contents/Info.plist", []byte(strings.Replace(menuInfoFixture, "<string>mac-lan</string>", "<string>Other</string>", 1))},
		{"visible dock app", "Contents/Info.plist", []byte(strings.Replace(menuInfoFixture, "<true/>", "<false/>", 1))},
		{"wrong commit", "Contents/Resources/COMMIT.txt", []byte(strings.Repeat("b", 40))},
		{"unrelated content", "Contents/Resources/user-notes.txt", []byte("user content")},
		{"obsolete capture helper", "Contents/Helpers/MacDowCapture", []byte("old helper")},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, _, source := newFixture(t, true)
			e.OS, e.Arch, e.Current = "darwin", "arm64", "0.2.0"
			root := t.TempDir()
			app := writeMenuApp(t, root, "0.2.0", source.SHA)
			if err := os.WriteFile(filepath.Join(app, filepath.FromSlash(test.file)), test.contents, 0700); err != nil {
				t.Fatal(err)
			}
			if err := e.installIdentity(app); err == nil {
				t.Fatal("accepted invalid installed menu app")
			}
		})
	}
}

func TestMenuAppPreferredOverCompatibilityPreview(t *testing.T) {
	r := Release{ID: 1, Tag: "v0.2.0", Assets: []Asset{
		{ID: 2, Name: "mac-lan-macos-arm64-commandline-preview.tar.gz", Size: 1, Digest: "sha256:" + strings.Repeat("a", 64)},
		{ID: 3, Name: "MAC-DOW-macos-arm64-unsigned.tar.gz", Size: 2, Digest: "sha256:" + strings.Repeat("b", 64)},
	}}
	p, err := Select([]Release{r}, "0.1.0", "stable", "darwin", "arm64")
	if err != nil || p.Asset.ID != 3 {
		t.Fatal("menu app not selected", p, err)
	}
}

func TestRenamedMenuAppInstalledIdentityStillValidatesNewLayout(t *testing.T) {
	e, _, source := newFixture(t, true)
	e.OS, e.Arch, e.Current = "darwin", "arm64", "0.2.0"
	parent := t.TempDir()
	app := writeMenuApp(t, parent, "0.2.0", source.SHA)
	renamed := filepath.Join(parent, "My Menu.app")
	if err := os.Rename(app, renamed); err != nil {
		t.Fatal(err)
	}
	if err := e.installIdentity(renamed); err != nil {
		t.Fatal("valid renamed menu app refused", err)
	}
	if err := os.WriteFile(filepath.Join(renamed, "Contents", "Helpers", "MacDowHost"), []byte("not a Mach-O host"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := e.installIdentity(renamed); err == nil {
		t.Fatal("renamed menu app bypassed Mach-O validation")
	}
}

func TestMenuAppApplyRollbackPreservesNeighbor(t *testing.T) {
	e, _, source := newFixture(t, true)
	e.OS, e.Arch = "darwin", "arm64"
	parent := t.TempDir()
	install := writeMenuApp(t, parent, "0.1.0", source.SHA)
	sentinel := filepath.Join(parent, "settings.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	setRelease := func(version string) {
		source.Data = menuAppArchive(t, version, source.SHA)
		digest := sha256.Sum256(source.Data)
		source.R = Release{ID: 1, Tag: "v" + version, Assets: []Asset{{ID: 2, Name: "MAC-DOW-macos-arm64-unsigned.tar.gz", Size: int64(len(source.Data)), Digest: "sha256:" + hex.EncodeToString(digest[:])}}}
	}
	setRelease("0.2.0")
	stage, err := e.Stage(context.Background(), "stable")
	if err != nil || !stage.CanApply {
		t.Fatal(stage, err)
	}
	journal, err := e.Apply(context.Background(), stage.Directory, install, stage.Plan.confirmation(), false)
	if err != nil {
		t.Fatal(err)
	}
	e.Current = "0.2.0"
	if err := e.installIdentity(install); err != nil {
		t.Fatal(err)
	}
	setRelease("0.1.0")
	if err := e.Rollback(context.Background(), journal, install, "ROLLBACK", false); err != nil {
		t.Fatal(err)
	}
	e.Current = "0.1.0"
	if err := e.installIdentity(install); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("neighbor changed", err)
	}
}
