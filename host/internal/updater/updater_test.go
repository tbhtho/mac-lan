package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSemanticVersionsAndChannels(t *testing.T) {
	for _, pair := range [][2]string{{"1.0.0-alpha.2", "1.0.0-alpha.10"}, {"1.0.0-rc.1", "1.0.0"}, {"1.9.9", "2.0.0"}} {
		a, e := ParseVersion(pair[0])
		if e != nil {
			t.Fatal(e)
		}
		b, _ := ParseVersion(pair[1])
		if a.Compare(b) >= 0 {
			t.Fatal(pair)
		}
	}
	for _, s := range []string{"v1", "1.0", "01.0.0", "1.0.0-alpha.01", "1.0.0/../../x", "1.0.0+build", ""} {
		if _, e := ParseVersion(s); e == nil {
			t.Fatal("accepted", s)
		}
	}
	rs := []Release{fixtureRelease("v0.2.0", false), fixtureRelease("v0.3.0-alpha.1", true), fixtureRelease("v0.1.0", false)}
	p, e := Select(rs, "0.1.0", "stable", "windows", "amd64")
	if e != nil || p.Tag != "v0.2.0" {
		t.Fatal(p, e)
	}
	p, e = Select(rs, "0.1.0", "preview", "windows", "amd64")
	if e != nil || p.Tag != "v0.3.0-alpha.1" {
		t.Fatal(p, e)
	}
	if _, e = Select(rs, "0.4.0", "preview", "windows", "amd64"); !errors.Is(e, ErrNoUpdate) {
		t.Fatal(e)
	}
	if _, e = Select(rs, "0.1.0", "preview", "windows", "arm64"); e == nil {
		t.Fatal("wrong architecture")
	}
}
func fixtureRelease(tag string, pre bool) Release {
	return Release{ID: 10, Tag: tag, Prerelease: pre, Assets: []Asset{{ID: 11, Name: "MAC-DOW-windows-x64-unsigned.zip", Size: 10, Digest: "sha256:" + string(bytes.Repeat([]byte{'a'}, 64))}}}
}
func TestMetadataRejectsUnsafeAssets(t *testing.T) {
	for _, mutate := range []func(*Release){
		func(r *Release) { r.Draft = true }, func(r *Release) { r.Assets[0].Digest = "" }, func(r *Release) { r.Assets[0].Size = 1 << 40 }, func(r *Release) { r.Assets = append(r.Assets, r.Assets[0]) },
		func(r *Release) { r.Assets[0].Name = "MAC-DOW-windows-x64-BUILD-KIT.zip" }, func(r *Release) { r.Prerelease = false; r.Tag = "v0.2.0-alpha.1" },
	} {
		r := fixtureRelease("v0.2.0", false)
		mutate(&r)
		if _, e := Select([]Release{r}, "0.1.0", "preview", "windows", "amd64"); e == nil {
			t.Fatal("bad metadata accepted", r)
		}
	}
}
func zipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for n, s := range files {
		f, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte(s))
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestSafeArchiveAndIntegrity(t *testing.T) {
	for _, name := range []string{"../evil", "/root", "C:/evil", "a/../../evil", "a\\evil", "CON", "safe/../evil"} {
		b := zipFixture(t, map[string]string{name: "bad"})
		if e := Extract(bytes.NewReader(b), int64(len(b)), ".zip", t.TempDir()); e == nil {
			t.Fatal("unsafe archive", name)
		}
	}
	b := zipFixture(t, map[string]string{"ok.txt": "hello"})
	dir := t.TempDir()
	if e := Extract(bytes.NewReader(b), int64(len(b)), ".zip", dir); e != nil {
		t.Fatal(e)
	}
	if e := CheckDigest(bytes.NewReader(b), int64(len(b)), "sha256:"+string(bytes.Repeat([]byte{'0'}, 64))); e == nil {
		t.Fatal("tamper accepted")
	}
	sum := sha256.Sum256(b)
	if e := CheckDigest(bytes.NewReader(b), int64(len(b)), "sha256:"+hex.EncodeToString(sum[:])); e != nil {
		t.Fatal(e)
	}
	if e := CheckDigest(bytes.NewReader(b), int64(len(b)+1), "sha256:"+hex.EncodeToString(sum[:])); e == nil {
		t.Fatal("partial download accepted")
	}
}
func TestAtomicSwapRollbackAndSettings(t *testing.T) {
	base := t.TempDir()
	install := filepath.Join(base, "MAC-DOW")
	next := filepath.Join(base, "next")
	for _, d := range []string{install, next} {
		os.Mkdir(d, 0700)
	}
	os.WriteFile(filepath.Join(install, "app"), []byte("old"), 0600)
	os.WriteFile(filepath.Join(next, "app"), []byte("new"), 0600)
	settings := filepath.Join(base, "preferences.json")
	os.WriteFile(settings, []byte("keep"), 0600)
	backup := filepath.Join(base, "backup")
	if e := Swap(install, next, backup, os.Rename); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(filepath.Join(install, "app")); string(b) != "new" {
		t.Fatal("new not installed")
	}
	if e := Restore(install, backup, filepath.Join(base, "rejected"), os.Rename); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(filepath.Join(install, "app")); string(b) != "old" {
		t.Fatal("rollback failed")
	}
	if b, _ := os.ReadFile(settings); string(b) != "keep" {
		t.Fatal("settings changed")
	}
	next = filepath.Join(base, "broken")
	os.Mkdir(next, 0700)
	os.WriteFile(filepath.Join(next, "app"), []byte("new"), 0600)
	fail := func(a, b string) error {
		if a == next {
			return errors.New("simulated sharing violation")
		}
		return os.Rename(a, b)
	}
	if e := Swap(install, next, filepath.Join(base, "backup2"), fail); e == nil {
		t.Fatal("swap should fail")
	}
	if b, _ := os.ReadFile(filepath.Join(install, "app")); string(b) != "old" {
		t.Fatal("failed swap lost old app")
	}
}
func TestStagingDoesNotTrustStoredVerification(t *testing.T) {
	e := Engine{Root: t.TempDir(), OS: "windows", Arch: "amd64", Current: "0.1.0", Source: &fakeSource{}, Verify: func(context.Context, string, string) error { return errors.New("unattested") }}
	_, err := e.Apply(context.Background(), "not-a-stage", t.TempDir(), "", false)
	if err == nil {
		t.Fatal("apply without confirmation")
	}
}

type fakeSource struct{}

func (*fakeSource) Releases(context.Context) ([]Release, error) {
	return nil, errors.New("fixture offline")
}
func (*fakeSource) Download(context.Context, Plan, string) error {
	return errors.New("fixture offline")
}
func (*fakeSource) Commit(context.Context, string) (string, error) {
	return "", errors.New("fixture offline")
}
