package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// resumableSource can continue a partial download. The signed feed implements
// it; sources without it download each attempt from the start.
type resumableSource interface {
	DownloadResume(context.Context, Plan, string) error
}

const (
	// Incomplete stage folders, worker copies and temporary install folders
	// younger than this may belong to a running updater and are kept.
	staleUpdaterAge = time.Hour
	// Partial downloads of a release nobody resumed for this long are dropped.
	stalePartialAge = 7 * 24 * time.Hour
	downloadsFolder = "downloads"
)

// downloadKey names a partial download after the exact signed asset, so a
// resumed file can only ever continue the same release, asset, size and digest.
func downloadKey(p Plan) string {
	if p.ReleaseID <= 0 || p.Asset.ID <= 0 || p.Asset.Size <= 0 || !strings.HasPrefix(p.Asset.Digest, "sha256:") || !feedSHA.MatchString(p.Asset.Digest[7:]) {
		return ""
	}
	return fmt.Sprintf("%d-%d-%d-%s.partial", p.ReleaseID, p.Asset.ID, p.Asset.Size, p.Asset.Digest[7:])
}

// downloadStage downloads p and returns a new stage folder holding the archive.
// Resumable sources keep the partial file outside any stage folder until it is
// complete and matches the signed size and SHA-256, so a failed or killed
// attempt leaves no half-built stage and the next attempt continues it.
func (e *Engine) downloadStage(ctx context.Context, p Plan) (string, error) {
	newStage := func() (string, error) { return os.MkdirTemp(e.Root, "stage-") }
	rs, resumable := e.Source.(resumableSource)
	key := downloadKey(p)
	if !resumable || key == "" {
		dir, err := newStage()
		if err != nil {
			return "", err
		}
		partial := filepath.Join(dir, "download.partial")
		if err = e.Source.Download(ctx, p, partial); err == nil {
			err = os.Rename(partial, filepath.Join(dir, p.Asset.Name))
		}
		if err != nil {
			if e.owned(dir) {
				_ = os.RemoveAll(dir)
			}
			return "", err
		}
		return dir, nil
	}
	downloads := filepath.Join(e.Root, downloadsFolder)
	if err := os.Mkdir(downloads, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	if info, err := os.Lstat(downloads); err != nil || !info.IsDir() || !e.owned(downloads) {
		return "", errors.New("update download folder is not an updater-owned directory")
	}
	e.prunePartials(downloads, key, time.Time{})
	partial := filepath.Join(downloads, key)
	if err := rs.DownloadResume(ctx, p, partial); err != nil {
		return "", err
	}
	f, err := os.Open(partial)
	if err != nil {
		return "", err
	}
	err = CheckDigest(f, p.Asset.Size, p.Asset.Digest)
	f.Close()
	if err != nil {
		// Never keep or reuse bytes that do not match the signed digest.
		_ = os.Remove(partial)
		return "", err
	}
	dir, err := newStage()
	if err != nil {
		return "", err
	}
	if err = os.Rename(partial, filepath.Join(dir, p.Asset.Name)); err != nil {
		if e.owned(dir) {
			_ = os.RemoveAll(dir)
		}
		return "", err
	}
	return dir, nil
}

// prunePartials removes other partial downloads. With a zero cutoff every
// partial except keep is removed; otherwise only those not written since cutoff.
func (e *Engine) prunePartials(downloads, keep string, cutoff time.Time) []string {
	var removed []string
	entries, err := os.ReadDir(downloads)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == keep || !strings.HasSuffix(name, ".partial") {
			continue
		}
		path := filepath.Join(downloads, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || !e.owned(path) || (!cutoff.IsZero() && info.ModTime().After(cutoff)) {
			continue
		}
		if os.Remove(path) == nil {
			removed = append(removed, path)
		}
	}
	return removed
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// reusableStage finds the newest completed, never-applied stage of exactly
// this release asset whose archive is still present. Stage re-verifies it.
func (e *Engine) reusableStage(p Plan) string {
	entries, err := os.ReadDir(e.Root)
	if err != nil {
		return ""
	}
	best, bestTime := "", time.Time{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "stage-") || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		dir := filepath.Join(e.Root, entry.Name())
		if !e.owned(dir) || fileExists(filepath.Join(dir, "transaction.json")) || fileExists(filepath.Join(dir, "worker-result.json")) {
			continue
		}
		var s Stage
		if readJSON(filepath.Join(dir, "stage.json"), &s) != nil || filepath.Clean(s.Directory) != filepath.Clean(dir) {
			continue
		}
		q := s.Plan
		q.Channel = p.Channel
		if !reflect.DeepEqual(q, p) {
			continue
		}
		archive, err := os.Lstat(filepath.Join(dir, p.Asset.Name))
		if err != nil || !archive.Mode().IsRegular() || archive.Size() != p.Asset.Size {
			continue
		}
		if best == "" || archive.ModTime().After(bestTime) {
			best, bestTime = dir, archive.ModTime()
		}
	}
	return best
}

type journalStage struct {
	dir string
	tx  Transaction
	mod time.Time
}

// Cleanup removes update leftovers that nothing needs any more and returns
// what it removed. It keeps:
//   - the newest applied journal (and the backup it names) of each install at
//     the current version, which is what rollback and recovery read;
//   - journals of an interrupted or unrecovered swap, with their backups;
//   - the newest downloaded update that is newer than this version, plus any
//     stage a pending automatic update refers to;
//   - anything younger than an hour that a running updater might still use.
//
// Only folders with the updater's own name patterns inside the updater cache,
// and only .MAC-DOW-* siblings of a verified current install, are removed.
// Links are never followed and the install itself is never touched.
func (e *Engine) Cleanup(install string) []string {
	return e.cleanup(install, time.Now())
}

func (e *Engine) cleanup(install string, now time.Time) []string {
	var removed []string
	current, err := ParseVersion(e.Current)
	if err != nil {
		return nil
	}
	keepBackups := map[string]bool{}
	if info, err := os.Lstat(e.Root); err == nil && info.IsDir() && rejectLinks(e.Root, false) == nil {
		removed = append(removed, e.cleanupCache(current, keepBackups, now)...)
	}
	if install == "" {
		return removed
	}
	abs, err := filepath.Abs(install)
	if err != nil || abs == filepath.Dir(abs) || e.installIdentity(abs) != nil {
		return removed
	}
	parent := filepath.Dir(abs)
	if rejectLinks(parent, false) != nil {
		return removed
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return removed
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(parent, name)
		if filepath.Clean(path) == filepath.Clean(abs) || !strings.HasPrefix(name, ".MAC-DOW-") {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !(info.IsDir() || info.Mode().IsRegular()) || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			continue
		}
		stale := now.Sub(info.ModTime()) > staleUpdaterAge
		switch {
		case strings.HasPrefix(name, ".MAC-DOW-backup-"):
			if keepBackups[filepath.Clean(path)] {
				continue
			}
		case strings.HasPrefix(name, ".MAC-DOW-reverted-"):
		case strings.HasPrefix(name, ".MAC-DOW-candidate-"), strings.HasPrefix(name, ".MAC-DOW-rollback-"):
			// Temporary folders of an apply or rollback that may still be running.
			if !stale {
				continue
			}
		default:
			continue
		}
		if os.RemoveAll(path) == nil {
			removed = append(removed, path)
		}
	}
	return removed
}

func (e *Engine) cleanupCache(current Version, keepBackups map[string]bool, now time.Time) []string {
	var removed, discard []string
	entries, err := os.ReadDir(e.Root)
	if err != nil {
		return nil
	}
	referenced := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "pending-") || !strings.HasSuffix(entry.Name(), ".json") || !entry.Type().IsRegular() {
			continue
		}
		var pending pendingAutomaticUpdate
		if readJSON(filepath.Join(e.Root, entry.Name()), &pending) == nil && pending.Current == e.Current {
			referenced[filepath.Clean(pending.Directory)] = true
		}
	}
	newestApplied := map[string]journalStage{}
	var newer []journalStage
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(e.Root, name)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !e.owned(path) {
			continue
		}
		switch {
		case name == downloadsFolder:
			removed = append(removed, e.prunePartials(path, "", now.Add(-stalePartialAge))...)
		case strings.HasPrefix(name, "runner-"):
			if now.Sub(info.ModTime()) > staleUpdaterAge {
				discard = append(discard, path)
			}
		case strings.HasPrefix(name, "stage-"):
			journal := filepath.Join(path, "transaction.json")
			var tx Transaction
			if readJSON(journal, &tx) == nil {
				switch {
				case tx.State == "prepared" || tx.State == "failed-recovery-needed":
					// An interrupted swap may still need this backup for recovery.
					keepBackups[filepath.Clean(tx.Backup)] = true
				case tx.State == "applied" && tx.Version == e.Current:
					mod := info.ModTime()
					if j, err := os.Lstat(journal); err == nil {
						mod = j.ModTime()
					}
					key := filepath.Clean(tx.Install)
					if previous, ok := newestApplied[key]; !ok || mod.After(previous.mod) {
						if ok {
							discard = append(discard, previous.dir)
						}
						newestApplied[key] = journalStage{dir: path, tx: tx, mod: mod}
					} else {
						discard = append(discard, path)
					}
				default:
					discard = append(discard, path)
				}
				continue
			}
			var s Stage
			if readJSON(filepath.Join(path, "stage.json"), &s) == nil {
				if v, err := ParseVersion(s.Plan.Version); err == nil && v.Compare(current) > 0 {
					mod := info.ModTime()
					if j, err := os.Lstat(filepath.Join(path, "stage.json")); err == nil {
						mod = j.ModTime()
					}
					newer = append(newer, journalStage{dir: path, mod: mod})
				} else {
					discard = append(discard, path) // this or an older version: never applied again
				}
				continue
			}
			if now.Sub(info.ModTime()) > staleUpdaterAge {
				discard = append(discard, path) // interrupted download or extraction
			}
		}
	}
	sort.Slice(newer, func(i, j int) bool { return newer[i].mod.After(newer[j].mod) })
	for i, n := range newer {
		if i > 0 && !referenced[filepath.Clean(n.dir)] {
			discard = append(discard, n.dir)
		}
	}
	for _, j := range newestApplied {
		keepBackups[filepath.Clean(j.tx.Backup)] = true
		// Rollback re-downloads the signed older release; the applied package
		// copies in the journal's folder are no longer read.
		removed = append(removed, e.stripAppliedStage(j.dir)...)
	}
	for _, path := range discard {
		if e.owned(path) && os.RemoveAll(path) == nil {
			removed = append(removed, path)
		}
	}
	return removed
}

func (e *Engine) stripAppliedStage(dir string) []string {
	var removed []string
	targets := []string{filepath.Join(dir, "preview"), filepath.Join(dir, "download.partial")}
	var s Stage
	if readJSON(filepath.Join(dir, "stage.json"), &s) == nil {
		// Only a package name the updater itself would have downloaded.
		if names, err := assetNames(e.OS, e.Arch); err == nil {
			for _, name := range names {
				if s.Plan.Asset.Name == name {
					targets = append(targets, filepath.Join(dir, name))
				}
			}
		}
	}
	for _, path := range targets {
		if !fileExists(path) || !e.owned(path) {
			continue
		}
		if os.RemoveAll(path) == nil {
			removed = append(removed, path)
		}
	}
	return removed
}
