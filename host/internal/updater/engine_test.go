package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureSource struct {
	R    Release
	Data []byte
	SHA  string
	Fail bool
}

func (s *fixtureSource) Releases(context.Context) ([]Release, error)    { return []Release{s.R}, nil }
func (s *fixtureSource) Commit(context.Context, string) (string, error) { return s.SHA, nil }
func (s *fixtureSource) Download(_ context.Context, _ Plan, p string) error {
	if s.Fail {
		os.WriteFile(p, []byte("partial"), 0600)
		return errors.New("connection reset")
	}
	return os.WriteFile(p, s.Data, 0600)
}
func newFixture(t *testing.T, trust bool) (*Engine, string, *fixtureSource) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(base, "MAC-DOW")
	os.Mkdir(install, 0700)
	marker := func(v string) string {
		b, _ := json.Marshal(Marker{Product: "MAC-DOW", RepositoryID: BuildConfig().RepositoryID, Version: v, OS: "windows", Arch: "amd64"})
		return string(b)
	}
	os.WriteFile(filepath.Join(install, "MAC-DOW-INSTALL.json"), []byte(marker("0.1.0")), 0600)
	os.WriteFile(filepath.Join(install, "MAC-DOW.exe"), []byte("old"), 0600)
	data := zipFixture(t, map[string]string{"MAC-DOW-INSTALL.json": marker("0.2.0"), "MAC-DOW.exe": "new", "MAC-DOW-Update.exe": "new helper", "COMMIT.txt": strings.Repeat("a", 40)})
	sum := sha256.Sum256(data)
	r := fixtureRelease("v0.2.0", false)
	r.Assets[0].Digest = "sha256:" + hex.EncodeToString(sum[:])
	r.Assets[0].Size = int64(len(data))
	src := &fixtureSource{R: r, Data: data, SHA: strings.Repeat("a", 40)}
	verify := func(context.Context, string, string) error {
		if trust {
			return nil
		}
		return ErrProvenance
	}
	return &Engine{Root: filepath.Join(base, "updates"), OS: "windows", Arch: "amd64", Current: "0.1.0", Source: src, Verify: verify}, install, src
}
func TestStageBlocksUnsignedAndCannotForgeTrust(t *testing.T) {
	e, install, _ := newFixture(t, false)
	s, err := e.Stage(context.Background(), "stable")
	if err != nil || s.CanApply {
		t.Fatal(s, err)
	}
	s.CanApply = true
	if err = writeJSON(filepath.Join(s.Directory, "stage.json"), s); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apply(context.Background(), s.Directory, install, s.Plan.confirmation(), false); !errors.Is(err, ErrProvenance) {
		t.Fatal("forged trust accepted", err)
	}
	if b, _ := os.ReadFile(filepath.Join(install, "MAC-DOW.exe")); string(b) != "old" {
		t.Fatal("unsigned update replaced app")
	}
}
func TestSignedFixtureApplyRollbackAndPreservedSettings(t *testing.T) {
	e, install, src := newFixture(t, true)
	prefs := filepath.Join(filepath.Dir(install), "preferences.json")
	os.WriteFile(prefs, []byte("keep"), 0600)
	s, err := e.Stage(context.Background(), "stable")
	if err != nil || !s.CanApply {
		t.Fatal(err)
	}
	// Cache preview is not trusted as executable input; apply re-extracts the verified archive.
	os.WriteFile(filepath.Join(s.Directory, "preview", "MAC-DOW.exe"), []byte("tampered preview"), 0600)
	j, err := e.Apply(context.Background(), s.Directory, install, s.Plan.confirmation(), false)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(install, "MAC-DOW.exe")); string(b) != "new" {
		t.Fatal("untrusted preview used")
	}
	e.Current = "0.2.0"
	// Rollback obtains an independently trusted older release, not journal hashes.
	var old Marker
	old = Marker{Product: "MAC-DOW", RepositoryID: BuildConfig().RepositoryID, Version: "0.1.0", OS: "windows", Arch: "amd64"}
	marker, _ := json.Marshal(old)
	src.Data = zipFixture(t, map[string]string{"MAC-DOW-INSTALL.json": string(marker), "MAC-DOW.exe": "old", "MAC-DOW-Update.exe": "old helper", "COMMIT.txt": src.SHA})
	sum := sha256.Sum256(src.Data)
	src.R = fixtureRelease("v0.1.0", false)
	src.R.Assets[0].Digest = "sha256:" + hex.EncodeToString(sum[:])
	src.R.Assets[0].Size = int64(len(src.Data))
	if err = e.Rollback(context.Background(), j, install, "ROLLBACK", false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(install, "MAC-DOW.exe")); string(b) != "old" {
		t.Fatal("rollback failed")
	}
	if b, _ := os.ReadFile(prefs); string(b) != "keep" {
		t.Fatal("settings lost")
	}
}
func TestMetadataAndArchiveChangesBlockApply(t *testing.T) {
	for _, change := range []string{"commit", "digest", "archive", "repo"} {
		t.Run(change, func(t *testing.T) {
			e, install, src := newFixture(t, true)
			s, err := e.Stage(context.Background(), "stable")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "commit":
				src.SHA = strings.Repeat("b", 40)
			case "digest":
				src.R.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
			case "archive":
				os.WriteFile(filepath.Join(s.Directory, s.Plan.Asset.Name), []byte("tampered"), 0600)
			case "repo":
				os.Mkdir(filepath.Join(install, ".git"), 0700)
			}
			if _, err = e.Apply(context.Background(), s.Directory, install, s.Plan.confirmation(), false); err == nil {
				t.Fatal("changed update applied")
			}
			if b, _ := os.ReadFile(filepath.Join(install, "MAC-DOW.exe")); string(b) != "old" {
				t.Fatal("old app modified")
			}
		})
	}
}
func TestInterruptedDownloadLeavesNoPartialStage(t *testing.T) {
	e, _, src := newFixture(t, true)
	src.Fail = true
	if _, err := e.Stage(context.Background(), "stable"); err == nil {
		t.Fatal("partial accepted")
	}
	entries, _ := os.ReadDir(e.Root)
	if len(entries) != 0 {
		t.Fatal("partial stage retained")
	}
}
func TestArchiveLinksAndCaseCollisions(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "link"}
	h.SetMode(os.ModeSymlink | 0700)
	f, _ := z.CreateHeader(h)
	f.Write([]byte("../outside"))
	z.Close()
	if err := Extract(bytes.NewReader(b.Bytes()), int64(b.Len()), ".zip", t.TempDir()); err == nil {
		t.Fatal("symlink accepted")
	}
	b2 := zipFixture(t, map[string]string{"FILE": "a", "file": "b"})
	if err := Extract(bytes.NewReader(b2), int64(len(b2)), ".zip", t.TempDir()); err == nil {
		t.Fatal("case collision accepted")
	}
}
func TestRollbackRefusesUnrelatedDirectory(t *testing.T) {
	e, install, _ := newFixture(t, true)
	s, err := e.Stage(context.Background(), "stable")
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.Apply(context.Background(), s.Directory, install, s.Plan.confirmation(), false)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(filepath.Dir(install), "unrelated")
	os.Mkdir(unrelated, 0700)
	os.WriteFile(filepath.Join(unrelated, "user-data"), []byte("untouched"), 0600)
	var tx Transaction
	readJSON(j, &tx)
	tx.Install = unrelated
	writeJSON(j, tx)
	if err = e.Rollback(context.Background(), j, install, "ROLLBACK", false); err == nil {
		t.Fatal("unrelated directory replaced during rollback")
	}
	if b, _ := os.ReadFile(filepath.Join(unrelated, "user-data")); string(b) != "untouched" {
		t.Fatal("unrelated data moved")
	}
}
func TestEmptyTagCannotPanic(t *testing.T) {
	g := &GH{}
	if _, err := g.Commit(context.Background(), ""); err == nil {
		t.Fatal("empty tag accepted")
	}
}

func TestRollbackCannotTrustForgedBackupOrRedirect(t *testing.T) {
	e, install, _ := newFixture(t, true)
	s, err := e.Stage(context.Background(), "stable")
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.Apply(context.Background(), s.Directory, install, s.Plan.confirmation(), false)
	if err != nil {
		t.Fatal(err)
	}
	e.Current = "0.2.0"
	var tx Transaction
	readJSON(j, &tx)
	os.WriteFile(filepath.Join(tx.Backup, "MAC-DOW.exe"), []byte("malicious"), 0600)
	tx.Hashes, _ = treeHashes(tx.Backup)
	writeJSON(j, tx)
	e.Verify = func(context.Context, string, string) error { return ErrProvenance }
	if err = e.Rollback(context.Background(), j, install, "ROLLBACK", false); err == nil {
		t.Fatal("unsigned backup trusted")
	}
	tx.Install = filepath.Join(filepath.Dir(install), "absent-unrelated")
	writeJSON(j, tx)
	if err = e.Rollback(context.Background(), j, install, "ROLLBACK", false); err == nil {
		t.Fatal("journal redirected installation")
	}
	if _, err = os.Stat(tx.Install); !os.IsNotExist(err) {
		t.Fatal("unrelated target created")
	}
	if b, _ := os.ReadFile(filepath.Join(install, "MAC-DOW.exe")); string(b) != "new" {
		t.Fatal("installed app changed")
	}
}
func TestNumericPrereleaseOverflowRejected(t *testing.T) {
	for _, v := range []string{"1.0.0-18446744073709551616", "1.0.0-0000000000000000000000000000001"} {
		if _, err := ParseVersion(v); err == nil {
			t.Fatal("invalid numeric prerelease accepted", v)
		}
	}
}
func TestDarwinRequiresExecutablePayload(t *testing.T) {
	e := &Engine{OS: "darwin", Arch: "arm64"}
	p := Plan{OS: "darwin", Version: "0.2.0", Commit: strings.Repeat("a", 40)}
	root := t.TempDir()
	target := filepath.Join(root, "macos-arm64")
	os.Mkdir(target, 0700)
	writeJSON(filepath.Join(target, "MAC-DOW-INSTALL.json"), Marker{Product: "MAC-DOW", RepositoryID: BuildConfig().RepositoryID, Version: p.Version, OS: "darwin", Arch: "arm64"})
	for _, name := range []string{"MacDowHost", "MacDowCapture"} {
		os.WriteFile(filepath.Join(target, name), []byte("binary"), 0600)
	}
	os.WriteFile(filepath.Join(target, "COMMIT.txt"), []byte(p.Commit), 0600)
	if err := e.validatePayload(root, p); err == nil {
		t.Fatal("non-executable Darwin binary accepted")
	}
}

func TestVerificationRequiresPatchedGitHubCLI(t *testing.T) {
	for _, v := range []string{"gh version 2.88.0 (2026-03-10)", "gh version 2.92.0", "unknown", "gh version 2.93.0-beta"} {
		if safeGHVersion(v) {
			t.Fatal("unsafe CLI accepted", v)
		}
	}
	for _, v := range []string{"gh version 2.93.0 (2026-05-27)", "gh version 2.94.1\nhttps://github.com/cli/cli", "gh version 3.0.0"} {
		if !safeGHVersion(v) {
			t.Fatal("patched CLI refused", v)
		}
	}
}
