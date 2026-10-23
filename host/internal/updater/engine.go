package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type Engine struct {
	Root, OS, Arch, Current string
	Source                  Source
	Verify                  func(context.Context, string, string) error
}
type Stage struct {
	Directory string `json:"directory"`
	Plan      Plan   `json:"plan"`
	CanApply  bool   `json:"canApply"`
	Message   string `json:"message"`
}
type Marker struct {
	Product      string `json:"product"`
	RepositoryID int64  `json:"repositoryId"`
	Version      string `json:"version"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Commit       string `json:"commit,omitempty"`
	Layout       string `json:"layout,omitempty"`
}
type Transaction struct {
	Install         string            `json:"install"`
	Backup          string            `json:"backup"`
	Candidate       string            `json:"candidate"`
	State           string            `json:"state"`
	Hashes          map[string]string `json:"hashes"`
	Version         string            `json:"version"`
	PreviousVersion string            `json:"previousVersion"`
}

func (e *Engine) Check(ctx context.Context, channel string) (Plan, error) {
	rs, err := e.Source.Releases(ctx)
	if err != nil {
		return Plan{}, err
	}
	p, err := Select(rs, e.Current, channel, e.OS, e.Arch)
	if err != nil {
		return p, err
	}
	p.Commit, err = e.Source.Commit(ctx, p.Tag)
	return p, err
}
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	tmp := path + ".new"
	f, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(append(b, '\n'))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	// Atomic replacement of updater-owned JSON only.
	return os.Rename(tmp, path)
}
func readJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 2<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func (e *Engine) owned(path string) bool {
	root, err := filepath.Abs(e.Root)
	if err != nil {
		return false
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	for q := p; ; q = filepath.Dir(q) {
		i, er := os.Lstat(q)
		if er != nil || i.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if q == filepath.Dir(q) {
			break
		}
	}
	return true
}
func (e *Engine) Stage(ctx context.Context, channel string) (Stage, error) {
	p, err := e.Check(ctx, channel)
	if err != nil {
		return Stage{}, err
	}
	if err = rejectLinks(e.Root, true); err != nil {
		return Stage{}, err
	}
	if err = os.MkdirAll(e.Root, 0700); err != nil {
		return Stage{}, err
	}
	dir, err := os.MkdirTemp(e.Root, "stage-")
	if err != nil {
		return Stage{}, err
	}
	s := Stage{Directory: dir, Plan: p}
	ok := false
	defer func() {
		if !ok && e.owned(dir) {
			_ = os.RemoveAll(dir)
		}
	}()
	partial := filepath.Join(dir, "download.partial")
	if err = e.Source.Download(ctx, p, partial); err != nil {
		return s, err
	}
	archive := filepath.Join(dir, p.Asset.Name)
	f, err := os.Open(partial)
	if err != nil {
		return s, err
	}
	err = CheckDigest(f, p.Asset.Size, p.Asset.Digest)
	f.Close()
	if err != nil {
		return s, err
	}
	if err = os.Rename(partial, archive); err != nil {
		return s, err
	}
	payload := filepath.Join(dir, "preview")
	if err = os.Mkdir(payload, 0700); err != nil {
		return s, err
	}
	if err = e.extractChecked(archive, p, payload); err != nil {
		return s, err
	}
	if err = e.validatePayload(payload, p); err != nil {
		s.Message = "Downloaded and SHA-256 checked; package layout is incompatible with automatic apply: " + err.Error()
	} else if e.Verify == nil || e.Verify(ctx, p.Tag, archive) != nil {
		s.Message = ErrProvenance.Error()
	} else {
		s.CanApply = true
		s.Message = "Release signature verified. Apply requires explicit confirmation and exits the app; backup retained."
	}
	if err = writeJSON(filepath.Join(dir, "stage.json"), s); err != nil {
		return s, err
	}
	ok = true
	return s, nil
}
func (e *Engine) extractChecked(archive string, p Plan, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = CheckDigest(f, p.Asset.Size, p.Asset.Digest); err != nil {
		return err
	}
	if standalonePlan(p) {
		return copyStandalone(f, dest, p.Asset.Name)
	}
	ext := ".zip"
	if p.OS == "darwin" {
		ext = ".tar.gz"
	}
	return Extract(f, p.Asset.Size, ext, dest) // same open handle used for hash and extraction
}
func payloadRoot(base string, p Plan) string {
	if standalonePlan(p) {
		return filepath.Join(base, p.Asset.Name)
	}
	if p.OS == "darwin" {
		if info, err := os.Lstat(appBundle(base)); err == nil && info.IsDir() {
			return appBundle(base)
		}
		return filepath.Join(base, "macos-arm64")
	}
	return base
}
func (e *Engine) validatePayload(base string, p Plan) error {
	root := payloadRoot(base, p)
	var m Marker
	if standalonePlan(p) {
		var err error
		m, err = ReadPackageIdentity(root)
		if err != nil {
			return err
		}
		if m.Commit != p.Commit {
			return errors.New("payload commit mismatch")
		}
	} else if err := readJSON(filepath.Join(metadataRoot(root), "MAC-DOW-INSTALL.json"), &m); err != nil {
		return errors.New("missing install identity")
	}
	if m.Product != "MAC-DOW" || m.RepositoryID != BuildConfig().RepositoryID || m.Version != p.Version || m.OS != e.OS || m.Arch != e.Arch {
		return errors.New("payload identity/version/platform mismatch")
	}
	if standalonePlan(p) {
		return nil
	}
	if commandlinePreviewPlan(p) {
		if m.Layout != "commandline-preview" || m.Commit != p.Commit {
			return errors.New("Mac preview identity/commit mismatch")
		}
		if err := validatePreviewDirectory(root, false); err != nil {
			return err
		}
		if err := validateMacHost(filepath.Join(root, "MacDowHost"), true); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(root, "COMMIT.txt"))
		if err != nil || strings.TrimSpace(string(b)) != p.Commit {
			return errors.New("payload commit mismatch")
		}
		return nil
	}
	names := []string{"MAC-DOW.exe", "MAC-DOW-Update.exe"}
	if e.OS == "darwin" {
		names = []string{"MacDowHost", "MacDowCapture"}
		if isApp(root) {
			names = []string{"Contents/MacOS/MAC-DOW Host", "Contents/Helpers/MacDowHost", "Contents/Helpers/MacDowCapture"}
			if i, err := os.Lstat(filepath.Join(root, "Contents", "Info.plist")); err != nil || !i.Mode().IsRegular() {
				return errors.New("missing app Info.plist")
			}
		}
	}
	for _, n := range names {
		i, err := os.Lstat(filepath.Join(root, n))
		if err != nil || !i.Mode().IsRegular() || i.Size() == 0 || (e.OS == "darwin" && i.Mode().Perm()&0111 == 0) {
			return errors.New("missing regular platform executable")
		}
	}
	b, err := os.ReadFile(filepath.Join(metadataRoot(root), "COMMIT.txt"))
	if err != nil || strings.TrimSpace(string(b)) != p.Commit {
		return errors.New("payload commit mismatch")
	}
	return nil
}
func (e *Engine) loadStage(dir string) (Stage, error) {
	var s Stage
	if !e.owned(dir) {
		return s, errors.New("stage is outside updater cache or contains a link")
	}
	err := readJSON(filepath.Join(dir, "stage.json"), &s)
	if err != nil {
		return s, err
	}
	if filepath.Clean(s.Directory) != filepath.Clean(dir) {
		return s, errors.New("stage directory mismatch")
	}
	return s, nil
}
func (e *Engine) Preflight(ctx context.Context, dir, install string) (Stage, error) {
	s, err := e.loadStage(dir)
	if err != nil {
		return s, err
	}
	rs, err := e.Source.Releases(ctx)
	if err != nil {
		return s, err
	}
	var exact []Release
	for _, r := range rs {
		if r.Tag == s.Plan.Tag {
			exact = append(exact, r)
		}
	}
	fresh, err := Select(exact, e.Current, s.Plan.Channel, e.OS, e.Arch)
	if err != nil {
		return s, err
	}
	fresh.Commit, err = e.Source.Commit(ctx, s.Plan.Tag)
	if err != nil {
		return s, err
	}
	if !reflect.DeepEqual(fresh, s.Plan) {
		return s, errors.New("release/asset/commit changed since staging")
	}
	archive := filepath.Join(dir, s.Plan.Asset.Name)
	f, err := os.Open(archive)
	if err != nil {
		return s, err
	}
	err = CheckDigest(f, s.Plan.Asset.Size, s.Plan.Asset.Digest)
	f.Close()
	if err != nil {
		return s, err
	}
	if e.Verify == nil || e.Verify(ctx, s.Plan.Tag, archive) != nil {
		return s, ErrProvenance
	}
	if err = e.installIdentity(install); err != nil {
		return s, err
	}
	return s, nil
}
func rejectLinks(path string, missing bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for q := abs; ; q = filepath.Dir(q) {
		i, er := os.Lstat(q)
		if er != nil && !(missing && os.IsNotExist(er)) {
			return er
		}
		if er == nil && (i.Mode()&os.ModeSymlink != 0 || !i.IsDir()) {
			return errors.New("path contains a link or non-directory")
		}
		if q == filepath.Dir(q) {
			break
		}
	}
	return nil
}
func (e *Engine) installIdentity(install string) error {
	abs, err := filepath.Abs(install)
	if err != nil {
		return err
	}
	if abs == filepath.Dir(abs) {
		return errors.New("cannot update filesystem root")
	}
	if info, er := os.Lstat(abs); er == nil && info.Mode().IsRegular() && e.OS == "windows" {
		if err = rejectLinks(filepath.Dir(abs), false); err != nil {
			return err
		}
		m, er := ReadPackageIdentity(abs)
		if er != nil {
			return er
		}
		if m.Product != "MAC-DOW" || m.RepositoryID != BuildConfig().RepositoryID || m.OS != e.OS || m.Arch != e.Arch || m.Version != e.Current {
			return errors.New("standalone product/version/platform mismatch")
		}
		return nil
	}
	if _, err = os.Lstat(filepath.Join(abs, ".git")); err == nil {
		return errors.New("development checkout cannot be replaced")
	}
	for q := abs; q != filepath.Dir(q); q = filepath.Dir(q) {
		i, er := os.Lstat(q)
		if er != nil || i.Mode()&os.ModeSymlink != 0 {
			return errors.New("install path contains link or missing directory")
		}
	}
	var m Marker
	if err = readJSON(filepath.Join(metadataRoot(abs), "MAC-DOW-INSTALL.json"), &m); err != nil {
		return errors.New("unrecognized portable install; use manual update")
	}
	if m.Product != "MAC-DOW" || m.RepositoryID != BuildConfig().RepositoryID || m.OS != e.OS || m.Arch != e.Arch || m.Version != e.Current {
		return errors.New("installed product/version/platform mismatch")
	}
	if e.OS == "darwin" && m.Layout == "commandline-preview" {
		if err = rejectBroadPreviewDirectory(abs); err != nil {
			return err
		}
		if len(m.Commit) != 40 {
			return errors.New("installed Mac preview commit missing")
		}
		if err = validatePreviewDirectory(abs, true); err != nil {
			return err
		}
		if err = validateMacHost(filepath.Join(abs, "MacDowHost"), true); err != nil {
			return err
		}
		b, er := os.ReadFile(filepath.Join(abs, "COMMIT.txt"))
		if er != nil || strings.TrimSpace(string(b)) != m.Commit {
			return errors.New("installed Mac preview commit mismatch")
		}
	}
	return nil
}
func treeHashes(root string) (map[string]string, error) {
	result := map[string]string{}
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("links in portable install are unsupported")
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return errors.New("refusing repository update")
			}
			return nil
		}
		i, err := d.Info()
		if err != nil || !i.Mode().IsRegular() {
			return errors.New("non-regular installed file")
		}
		total += i.Size()
		if total > maxExpanded {
			return errors.New("installed tree exceeds backup bound")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		result[rel] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return result, err
}
func Swap(install, candidate, backup string, rename func(string, string) error) error {
	if _, e := os.Lstat(backup); !os.IsNotExist(e) {
		return errors.New("backup already exists")
	}
	if e := rename(install, backup); e != nil {
		return e
	}
	if e := rename(candidate, install); e != nil {
		if rollback := rename(backup, install); rollback != nil {
			return fmt.Errorf("swap failed %v; restore failed %v; backup retained at %s", e, rollback, backup)
		}
		return e
	}
	return nil
}
func Restore(install, backup, rejected string, rename func(string, string) error) error {
	if _, e := os.Lstat(backup); e != nil {
		return e
	}
	if _, e := os.Lstat(rejected); !os.IsNotExist(e) {
		return errors.New("rollback destination exists")
	}
	if _, e := os.Lstat(install); os.IsNotExist(e) {
		return rename(backup, install)
	}
	if e := rename(install, rejected); e != nil {
		return e
	}
	if e := rename(backup, install); e != nil {
		_ = rename(rejected, install)
		return e
	}
	return nil
}
func (e *Engine) Apply(ctx context.Context, dir, install, confirmation string, relaunch bool) (string, error) {
	s, err := e.loadStage(dir)
	if err != nil {
		return "", err
	}
	if confirmation != s.Plan.confirmation() {
		return "", errors.New("explicit version confirmation required")
	}
	s, err = e.Preflight(ctx, dir, install)
	if err != nil {
		return "", err
	}
	install, err = filepath.Abs(install)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(install)
	candidate, err := os.MkdirTemp(parent, ".MAC-DOW-candidate-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(candidate) // only the fresh updater-owned temporary directory
	if err = e.extractChecked(filepath.Join(dir, s.Plan.Asset.Name), s.Plan, candidate); err != nil {
		return "", err
	}
	if err = e.validatePayload(candidate, s.Plan); err != nil {
		return "", err
	}
	next := payloadRoot(candidate, s.Plan)
	if err = compatibleInstallShape(install, next, e.OS); err != nil {
		return "", err
	}
	hashes, err := treeHashes(install)
	if err != nil {
		return "", err
	}
	backup := filepath.Join(parent, ".MAC-DOW-backup-"+filepath.Base(candidate))
	tx := Transaction{Install: install, Backup: backup, Candidate: next, State: "prepared", Hashes: hashes, Version: s.Plan.Version, PreviousVersion: e.Current}
	journal := filepath.Join(dir, "transaction.json")
	if err = writeJSON(journal, tx); err != nil {
		return "", err
	}
	if err = Swap(install, next, backup, os.Rename); err != nil {
		tx.State = "failed-recovery-needed"
		if e.installIdentity(install) == nil {
			tx.State = "failed-restored"
		}
		_ = writeJSON(journal, tx)
		return journal, err
	}
	tx.State = "applied"
	if err = writeJSON(journal, tx); err != nil {
		return journal, err
	}
	if relaunch {
		if err = launch(install, e.OS); err != nil {
			return journal, fmt.Errorf("installed; relaunch failed, backup available: %w", err)
		}
	}
	return journal, nil
}

// A mutable journal or backup directory is never an executable trust anchor.
// Rollback downloads the named older release and verifies its signed provenance again.
type exactSource struct {
	Source
	tag string
}

func (s exactSource) Releases(ctx context.Context) ([]Release, error) {
	rs, err := s.Source.Releases(ctx)
	if err != nil {
		return nil, err
	}
	var out []Release
	for _, r := range rs {
		if r.Tag == s.tag {
			out = append(out, r)
		}
	}
	return out, nil
}
func (e *Engine) PrepareRollback(ctx context.Context, journal, install string) (Stage, error) {
	if !e.owned(filepath.Dir(journal)) {
		return Stage{}, errors.New("journal outside updater cache")
	}
	var t Transaction
	if err := readJSON(journal, &t); err != nil {
		return Stage{}, err
	}
	abs, err := filepath.Abs(install)
	if err != nil {
		return Stage{}, err
	}
	recorded, err := filepath.Abs(t.Install)
	if err != nil || abs != recorded || abs == filepath.Dir(abs) || t.Version != e.Current {
		return Stage{}, errors.New("rollback journal does not match this installation/version")
	}
	if t.State != "applied" && t.State != "prepared" && t.State != "failed-recovery-needed" {
		return Stage{}, errors.New("transaction is not rollbackable")
	}
	checkPath := abs
	if info, er := os.Lstat(abs); er == nil && info.Mode().IsRegular() {
		checkPath = filepath.Dir(abs)
	}
	if err = rejectLinks(checkPath, true); err != nil {
		return Stage{}, err
	}
	if _, err = os.Lstat(abs); err == nil {
		if err = e.installIdentity(abs); err != nil {
			return Stage{}, err
		}
	} else if !os.IsNotExist(err) {
		return Stage{}, err
	}
	previous, err := ParseVersion(t.PreviousVersion)
	if err != nil {
		return Stage{}, err
	}
	current, err := ParseVersion(e.Current)
	if err != nil || previous.Compare(current) >= 0 {
		return Stage{}, errors.New("rollback must select an older published version")
	}
	if e.Source == nil || e.Verify == nil {
		return Stage{}, ErrAccess
	}
	restore := *e
	restore.Current = "0.0.0-0"
	restore.Source = exactSource{Source: e.Source, tag: "v" + t.PreviousVersion}
	staged, err := restore.Stage(ctx, "preview")
	if err != nil {
		return staged, err
	}
	if !staged.CanApply {
		return staged, ErrProvenance
	}
	return staged, nil
}
func (e *Engine) Rollback(ctx context.Context, journal, install, confirmation string, relaunch bool) error {
	if confirmation != "ROLLBACK" {
		return errors.New("explicit rollback confirmation required")
	}
	s, err := e.PrepareRollback(ctx, journal, install)
	if err != nil {
		return err
	}
	install, err = filepath.Abs(install)
	if err != nil {
		return err
	}
	candidate, err := os.MkdirTemp(filepath.Dir(install), ".MAC-DOW-rollback-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(candidate)
	if err = e.extractChecked(filepath.Join(s.Directory, s.Plan.Asset.Name), s.Plan, candidate); err != nil {
		return err
	}
	if err = e.validatePayload(candidate, s.Plan); err != nil {
		return err
	}
	next := payloadRoot(candidate, s.Plan)
	if _, er := os.Lstat(install); er == nil {
		if err = compatibleInstallShape(install, next, e.OS); err != nil {
			return err
		}
	}
	if _, err = os.Lstat(install); os.IsNotExist(err) {
		err = os.Rename(next, install)
	} else if err == nil {
		rejected := filepath.Join(filepath.Dir(install), ".MAC-DOW-reverted-"+filepath.Base(candidate))
		err = Swap(install, next, rejected, os.Rename)
	}
	if err != nil {
		return err
	}
	var tx Transaction
	if err = readJSON(journal, &tx); err != nil {
		return err
	}
	tx.State = "rolled-back"
	if err = writeJSON(journal, tx); err != nil {
		return err
	}
	if relaunch {
		return launch(install, e.OS)
	}
	return nil
}
