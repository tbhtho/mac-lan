package updater

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type cleanupFixture struct {
	t                     *testing.T
	e                     *Engine
	install, parent, root string
	now                   time.Time
}

func newCleanupFixture(t *testing.T) *cleanupFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(base, "Downloads")
	if err = os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(parent, "mac-lan.exe")
	if err = os.WriteFile(install, standaloneFixture(t, "0.2.0", strings.Repeat("a", 40)), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "updates")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	return &cleanupFixture{t: t, e: &Engine{Root: root, OS: "windows", Arch: "amd64", Current: "0.2.0"}, install: install, parent: parent, root: root, now: time.Now()}
}

// dir creates a directory with the given files and sets its age last.
func (f *cleanupFixture) dir(path string, age time.Duration, files map[string]any) string {
	f.t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		f.t.Fatal(err)
	}
	for name, content := range files {
		file := filepath.Join(path, name)
		var err error
		if b, ok := content.([]byte); ok {
			err = os.WriteFile(file, b, 0600)
		} else {
			err = writeJSON(file, content)
		}
		if err != nil {
			f.t.Fatal(err)
		}
		f.age(file, age)
	}
	f.age(path, age)
	return path
}

func (f *cleanupFixture) age(path string, age time.Duration) {
	f.t.Helper()
	when := f.now.Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		f.t.Fatal(err)
	}
}

func (f *cleanupFixture) journal(name, state, version, backup string, age time.Duration) string {
	stage := Stage{Directory: filepath.Join(f.root, name), Plan: Plan{Version: version, Asset: Asset{Name: "mac-lan.exe"}}}
	return f.dir(stage.Directory, age, map[string]any{
		"stage.json":         stage,
		"transaction.json":   Transaction{Install: f.install, Backup: backup, State: state, Version: version, PreviousVersion: "0.1.0"},
		"mac-lan.exe":        []byte("package copy"),
		"worker-result.json": Result{OK: true},
	})
}

func (f *cleanupFixture) stage(name, version string, age time.Duration) string {
	path := filepath.Join(f.root, name)
	return f.dir(path, age, map[string]any{"stage.json": Stage{Directory: path, Plan: Plan{Version: version, Asset: Asset{Name: "mac-lan.exe"}}}, "mac-lan.exe": []byte("package")})
}

func TestCleanupKeepsRollbackStateAndRemovesUpdaterLeftovers(t *testing.T) {
	f := newCleanupFixture(t)
	hour := time.Hour
	backup := func(name string) string { return filepath.Join(f.parent, ".MAC-DOW-backup-"+name) }

	olderApplied := f.journal("stage-older-applied", "applied", "0.2.0", backup("older"), 3*hour)
	newestApplied := f.journal("stage-newest-applied", "applied", "0.2.0", backup("newest"), 2*hour)
	f.dir(filepath.Join(newestApplied, "preview"), 2*hour, map[string]any{"mac-lan.exe": []byte("preview")})
	rolledBack := f.journal("stage-rolled-back", "rolled-back", "0.3.0", backup("rolled"), 5*hour)
	interrupted := f.journal("stage-interrupted", "failed-recovery-needed", "0.3.0", backup("recovery"), 5*hour)
	oldVersion := f.stage("stage-old-version", "0.1.0", 5*hour)
	sameVersion := f.stage("stage-same-version", "0.2.0", 5*hour)
	olderCandidate := f.stage("stage-older-candidate", "0.3.0", 3*hour)
	newestCandidate := f.stage("stage-newest-candidate", "0.3.0", 2*hour)
	pendingCandidate := f.stage("stage-pending-candidate", "0.3.0", 4*hour)
	if err := writeJSON(filepath.Join(f.root, "pending-test.json"), pendingAutomaticUpdate{Install: f.install, Current: "0.2.0", Tag: "v0.3.0", Directory: pendingCandidate}); err != nil {
		t.Fatal(err)
	}
	staleIncomplete := f.dir(filepath.Join(f.root, "stage-stale-incomplete"), 2*hour, map[string]any{"download.partial": []byte("x")})
	freshIncomplete := f.dir(filepath.Join(f.root, "stage-fresh-incomplete"), time.Minute, map[string]any{"download.partial": []byte("x")})
	staleRunner := f.dir(filepath.Join(f.root, "runner-stale"), 2*hour, map[string]any{"MAC-DOW-Update.exe": []byte("worker")})
	freshRunner := f.dir(filepath.Join(f.root, "runner-fresh"), time.Minute, map[string]any{"MAC-DOW-Update.exe": []byte("worker")})
	downloads := f.dir(filepath.Join(f.root, downloadsFolder), 0, nil)
	stalePartial := filepath.Join(downloads, "1-2-3-old.partial")
	freshPartial := filepath.Join(downloads, "1-2-3-new.partial")
	for path, age := range map[string]time.Duration{stalePartial: 8 * 24 * hour, freshPartial: hour} {
		os.WriteFile(path, []byte("partial"), 0600)
		f.age(path, age)
	}
	unrelatedCache := f.dir(filepath.Join(f.root, "notes"), 9*hour, map[string]any{"keep.txt": []byte("keep")})

	keep := map[string]string{
		"newest backup":   f.dir(backup("newest"), 9*hour, map[string]any{"mac-lan.exe": []byte("v0.1.0")}),
		"recovery backup": f.dir(backup("recovery"), 9*hour, map[string]any{"mac-lan.exe": []byte("v0.2.0")}),
		"fresh candidate": f.dir(filepath.Join(f.parent, ".MAC-DOW-candidate-fresh"), time.Minute, nil),
		"user file":       f.dir(filepath.Join(f.parent, "photos"), 9*hour, map[string]any{"a.jpg": []byte("user")}),
		"other dotfile":   f.dir(filepath.Join(f.parent, ".MAC-DOW-notes"), 9*hour, nil),
	}
	gone := map[string]string{
		"older backup":     f.dir(backup("older"), time.Minute, map[string]any{"mac-lan.exe": []byte("v0.0.9")}),
		"rolled backup":    f.dir(backup("rolled"), time.Minute, map[string]any{"mac-lan.exe": []byte("v0.2.0")}),
		"reverted install": f.dir(filepath.Join(f.parent, ".MAC-DOW-reverted-x"), time.Minute, map[string]any{"mac-lan.exe": []byte("v0.3.0")}),
		"stale candidate":  f.dir(filepath.Join(f.parent, ".MAC-DOW-candidate-old"), 2*hour, nil),
		"stale rollback":   f.dir(filepath.Join(f.parent, ".MAC-DOW-rollback-old"), 2*hour, nil),
	}
	// A backup-named file stays when it is a link: cleanup never follows links.
	outside := f.dir(filepath.Join(filepath.Dir(f.root), "outside"), 9*hour, map[string]any{"precious.txt": []byte("keep")})
	link := filepath.Join(f.parent, ".MAC-DOW-backup-link")
	linked := os.Symlink(outside, link) == nil

	removed := f.e.cleanup(f.install, f.now)

	for name, path := range keep {
		if !fileExists(path) {
			t.Errorf("%s removed", name)
		}
	}
	for name, path := range gone {
		if fileExists(path) {
			t.Errorf("%s kept", name)
		}
	}
	for _, path := range []string{newestApplied, filepath.Join(newestApplied, "transaction.json"), filepath.Join(newestApplied, "worker-result.json"), interrupted, newestCandidate, pendingCandidate, freshIncomplete, freshRunner, freshPartial, unrelatedCache, f.install, filepath.Join(outside, "precious.txt")} {
		if !fileExists(path) {
			t.Errorf("needed %s removed", path)
		}
	}
	for _, path := range []string{olderApplied, rolledBack, oldVersion, sameVersion, olderCandidate, staleIncomplete, staleRunner, stalePartial, filepath.Join(newestApplied, "preview"), filepath.Join(newestApplied, "mac-lan.exe")} {
		if fileExists(path) {
			t.Errorf("leftover %s kept", path)
		}
	}
	if linked && !fileExists(link) {
		t.Error("link was removed or followed")
	}
	sort.Strings(removed)
	for _, path := range removed {
		if !strings.HasPrefix(path, f.root) && !strings.HasPrefix(filepath.Base(path), ".MAC-DOW-") {
			t.Errorf("removed a path outside updater patterns: %s", path)
		}
	}
	if b, err := os.ReadFile(f.install); err != nil || len(b) == 0 {
		t.Fatal("install damaged", err)
	}
	// The kept journal still supports rollback preparation checks.
	var tx Transaction
	if err := readJSON(filepath.Join(newestApplied, "transaction.json"), &tx); err != nil || tx.Backup != backup("newest") {
		t.Fatal("rollback journal changed", err)
	}
}

func TestCleanupLeavesSiblingsAloneForUnverifiedInstall(t *testing.T) {
	f := newCleanupFixture(t)
	f.e.Current = "0.9.0" // the file next to the backups is not this version's install
	backup := f.dir(filepath.Join(f.parent, ".MAC-DOW-backup-x"), 9*time.Hour, nil)
	reverted := f.dir(filepath.Join(f.parent, ".MAC-DOW-reverted-x"), 9*time.Hour, nil)
	f.e.cleanup(f.install, f.now)
	if !fileExists(backup) || !fileExists(reverted) {
		t.Fatal("cleanup removed siblings of an unverified install")
	}
	// An install path that is itself a leftover name is never removed.
	f.e.Current = "0.2.0"
	disguised := filepath.Join(f.parent, ".MAC-DOW-backup-install.exe")
	if err := os.Rename(f.install, disguised); err != nil {
		t.Fatal(err)
	}
	f.e.cleanup(disguised, f.now)
	if !fileExists(disguised) {
		t.Fatal("cleanup removed the install")
	}
}

func TestCleanupIgnoresMissingOrLinkedCache(t *testing.T) {
	f := newCleanupFixture(t)
	f.e.Root = filepath.Join(f.root, "missing")
	if removed := f.e.cleanup("", f.now); len(removed) != 0 {
		t.Fatal(removed)
	}
	target := f.dir(filepath.Join(filepath.Dir(f.root), "elsewhere"), 0, nil)
	stale := f.dir(filepath.Join(target, "stage-old"), 9*time.Hour, nil)
	linkedRoot := filepath.Join(filepath.Dir(f.root), "linked-updates")
	if os.Symlink(target, linkedRoot) != nil {
		t.Skip("symbolic links unavailable")
	}
	f.e.Root = linkedRoot
	f.e.cleanup("", f.now)
	if !fileExists(stale) {
		t.Fatal("cleanup followed a linked cache folder")
	}
}
