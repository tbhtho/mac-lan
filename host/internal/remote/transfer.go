package remote

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// TransferRequest copies a file or folder to ("push") or from ("pull") the
// paired computer. A folder's contents are copied into the destination folder.
type TransferRequest struct {
	Device    string
	Direction string
	Local     string
	Remote    string
	Overwrite bool
	Origin    string
}

// DeployRequest pushes a file or folder to the paired computer, replacing what
// is there, then optionally runs a command in the destination folder.
type DeployRequest struct {
	Device      string
	Source      string
	Destination string
	Command     string
	Shell       string
	Timeout     int
	Origin      string
}

func joinRemote(root, rel string) string {
	return strings.TrimRight(root, `/\`) + "/" + rel
}

// putFile sends one local file and returns where the other computer saved it.
func putFile(l *link, j *Job, local, remote, name string, overwrite bool) (string, error) {
	file, err := os.Open(local)
	if err != nil {
		return "", errors.New("cannot read " + local + ": " + err.Error())
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("not a regular file: " + local)
	}
	rid := newID()
	first, err := l.call(msg{T: "put", RID: rid, Path: remote, Name: name, Size: info.Size(), Overwrite: overwrite, Confirmed: overwrite, Origin: j.origin,
		X: runtime.GOOS != "windows" && info.Mode()&0o111 != 0}, 30*time.Second)
	if err != nil {
		return "", err
	}
	if !first.OK {
		return "", errors.New(first.Err)
	}
	sum := sha256.New()
	buffer := make([]byte, fileChunk)
	for {
		if j.cancelled() {
			_ = l.send(msg{T: "abort", RID: rid})
			return "", errors.New("cancelled")
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			sum.Write(buffer[:n])
			if err = l.send(msg{T: "chunk", RID: rid, D: base64.StdEncoding.EncodeToString(buffer[:n])}); err != nil {
				return "", errors.New("the connection dropped during the transfer")
			}
			j.mu.Lock()
			j.done += int64(n)
			j.mu.Unlock()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = l.send(msg{T: "abort", RID: rid})
			return "", errors.New("reading " + local + " failed: " + readErr.Error())
		}
	}
	last, err := l.call(msg{T: "end", RID: rid, Sha: hex.EncodeToString(sum.Sum(nil))}, 2*time.Minute)
	if err != nil {
		return "", err
	}
	if !last.OK {
		return "", errors.New(last.Err)
	}
	return last.Path, nil
}

// getFile fetches one remote file into local, verified before it is published.
func getFile(l *link, j *Job, remote, local string, overwrite bool) error {
	if _, err := os.Lstat(local); err == nil && !overwrite {
		return errors.New(local + " already exists. Nothing was changed; ask for it to be replaced if that is intended (needs approval).")
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return errors.New("cannot create the destination folder: " + err.Error())
	}
	file, err := os.CreateTemp(filepath.Dir(local), ".mac-lan-part-*")
	if err != nil {
		return errors.New("cannot write to the destination folder: " + err.Error())
	}
	temp := file.Name()
	defer os.Remove(temp)
	rid := newID()
	d := &download{file: file, hash: sha256.New(), job: j, done: make(chan error, 1)}
	l.pmu.Lock()
	l.downloads[rid] = d
	l.pmu.Unlock()
	forget := func() {
		l.pmu.Lock()
		delete(l.downloads, rid)
		l.pmu.Unlock()
		_ = file.Close()
	}
	reply, err := l.call(msg{T: "get", RID: rid, Path: remote, Origin: j.origin}, 30*time.Second)
	if err == nil && !reply.OK {
		err = errors.New(reply.Err)
	}
	if err != nil {
		forget()
		return err
	}
	select {
	case err = <-d.done:
	case <-l.closed:
		err = errors.New("the connection dropped during the transfer")
	case <-j.cancel:
		_ = l.send(msg{T: "abort", RID: rid})
		err = errors.New("cancelled")
	}
	forget()
	if err == nil && d.got != reply.Size {
		err = errors.New("the file arrived incomplete")
	}
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		mode := os.FileMode(0o644)
		if reply.X {
			mode = 0o755
		}
		_ = os.Chmod(temp, mode)
	}
	if err = os.Rename(temp, local); err != nil {
		return errors.New("cannot save " + local + ": " + err.Error())
	}
	return nil
}

// push copies a local file or folder; it returns the remote path and whether
// it was a folder.
func push(l *link, j *Job, local, remote string, overwrite bool) (string, bool, int, error) {
	source, err := resolvePath(local, "")
	if err != nil || local == "" || remote == "" {
		return "", false, 0, errors.New("give both a local path and a path on the other computer")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", false, 0, errors.New("no such file or folder on this computer: " + source)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, 0, errors.New("choose the original file or folder instead of a link: " + source)
	}
	if info.Mode().IsRegular() {
		j.mu.Lock()
		j.total = info.Size()
		j.mu.Unlock()
		saved, err := putFile(l, j, source, remote, filepath.Base(source), overwrite)
		return saved, false, 1, err
	}
	if !info.IsDir() {
		return "", false, 0, errors.New("not a regular file or folder: " + source)
	}
	entries, err := walkFiles(source)
	if err != nil {
		return "", false, 0, err
	}
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	j.mu.Lock()
	j.total = total
	j.mu.Unlock()
	for _, e := range entries {
		if _, err = putFile(l, j, filepath.Join(source, filepath.FromSlash(e.Rel)), joinRemote(remote, e.Rel), "", overwrite); err != nil {
			return "", true, 0, fmt.Errorf("%s: %w", e.Rel, err)
		}
	}
	return remote, true, len(entries), nil
}

func pull(l *link, j *Job, remote, local string, overwrite bool) (string, int, error) {
	destination, err := resolvePath(local, "")
	if err != nil || local == "" || remote == "" {
		return "", 0, errors.New("give both a local path and a path on the other computer")
	}
	listing, err := l.call(msg{T: "list", Path: remote, Origin: j.origin}, time.Minute)
	if err != nil {
		return "", 0, err
	}
	if !listing.OK {
		return "", 0, errors.New(listing.Err)
	}
	if !listing.Dir {
		if info, err := os.Stat(destination); err == nil && info.IsDir() {
			name := baseName(listing.Name)
			if name == "" || name == "." || name == ".." {
				return "", 0, errors.New("the other computer sent an invalid file name")
			}
			destination = filepath.Join(destination, name)
		}
		j.mu.Lock()
		j.total = listing.Size
		j.mu.Unlock()
		return destination, 1, getFile(l, j, listing.Path, destination, overwrite)
	}
	var total int64
	for _, e := range listing.Entries {
		// The other computer chooses these names; none may leave the destination.
		clean := filepath.Clean(filepath.FromSlash(e.Rel))
		if e.Rel == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.ContainsAny(e.Rel, ":\x00") || strings.Contains(e.Rel, `\`) {
			return "", 0, errors.New("the other computer sent an unsafe file name; nothing was saved")
		}
		total += e.Size
	}
	j.mu.Lock()
	j.total = total
	j.mu.Unlock()
	for _, e := range listing.Entries {
		if err = getFile(l, j, joinRemote(listing.Path, e.Rel), filepath.Join(destination, filepath.FromSlash(e.Rel)), overwrite); err != nil {
			return "", 0, fmt.Errorf("%s: %w", e.Rel, err)
		}
	}
	return destination, len(listing.Entries), nil
}

func sizeText(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", bytes)
}

// Transfer starts a copy and returns its job at once.
func (n *Node) Transfer(r TransferRequest) (*Job, error) {
	if r.Direction != "push" && r.Direction != "pull" {
		return nil, errors.New("direction must be push (this computer to the other) or pull (the other computer to this one)")
	}
	if !validText(r.Local, 4096) || !validText(r.Remote, 4096) || r.Local == "" || r.Remote == "" {
		return nil, errors.New("give both a local path and a path on the other computer")
	}
	l, err := n.target(r.Device)
	if err != nil {
		return nil, err
	}
	peer, _ := l.peerInfo()
	summary := r.Local + "  ->  " + peer.Name + ":" + r.Remote
	if r.Direction == "pull" {
		summary = peer.Name + ":" + r.Remote + "  ->  " + r.Local
	}
	state := "running"
	if r.Overwrite {
		state = "awaiting-confirmation"
	}
	j := n.newJob("transfer", r.Direction+" "+summary, "", r.Origin, state)
	go func() {
		defer n.changed()
		if r.Overwrite {
			where := peer.Name
			if r.Direction == "pull" {
				where = "this computer"
			}
			if !n.approve(j, ConfirmRequest{Title: "Replace existing files on " + where + "?", Detail: summary, Reason: "Files that already exist at the destination will be replaced.", Origin: r.Origin, Target: peer.Name}) {
				return
			}
			j.mu.Lock()
			j.state = "running"
			j.touch()
			j.mu.Unlock()
		}
		var where string
		var count int
		if r.Direction == "push" {
			where, _, count, err = push(l, j, r.Local, r.Remote, r.Overwrite)
		} else {
			where, count, err = pull(l, j, r.Remote, r.Local, r.Overwrite)
		}
		if err != nil {
			state := "failed"
			if j.cancelled() {
				state = "cancelled"
			}
			j.finish(state, "Transfer stopped: "+err.Error())
			return
		}
		j.mu.Lock()
		done := j.done
		j.mu.Unlock()
		j.note(fmt.Sprintf("Copied %d file(s), %s, to %s. Each file was verified with SHA-256.", count, sizeText(done), where))
		j.finish("succeeded", "")
	}()
	return j, nil
}

// Deploy pushes an update and optionally runs a command there. It always needs
// a person's approval, because it replaces files and runs code.
func (n *Node) Deploy(r DeployRequest) (*Job, error) {
	if !validText(r.Source, 4096) || !validText(r.Destination, 4096) || r.Source == "" || r.Destination == "" || !validText(r.Command, 32<<10) || r.Timeout < 0 {
		return nil, errors.New("give a source path on this computer and a destination path on the other computer")
	}
	l, err := n.target(r.Device)
	if err != nil {
		return nil, err
	}
	peer, _ := l.peerInfo()
	detail := r.Source + "  ->  " + peer.Name + ":" + r.Destination
	if r.Command != "" {
		detail += "\nthen run: " + r.Command
	}
	j := n.newJob("deploy", detail, r.Destination, r.Origin, "awaiting-confirmation")
	go func() {
		defer n.changed()
		if !n.approve(j, ConfirmRequest{Title: "Deploy this update to " + peer.Name + "?", Detail: detail, Reason: "Existing files at the destination are replaced" + map[bool]string{true: ", then the command runs there.", false: "."}[r.Command != ""], Origin: r.Origin, Target: peer.Name}) {
			return
		}
		j.mu.Lock()
		j.state = "running"
		j.touch()
		j.mu.Unlock()
		where, folder, count, err := push(l, j, r.Source, r.Destination, true)
		if err != nil {
			state := "failed"
			if j.cancelled() {
				state = "cancelled"
			}
			j.finish(state, "Deployment stopped during the copy: "+err.Error())
			return
		}
		j.mu.Lock()
		done := j.done
		j.mu.Unlock()
		j.note(fmt.Sprintf("Copied %d file(s), %s, to %s.", count, sizeText(done), where))
		if r.Command == "" {
			j.finish("succeeded", "")
			return
		}
		cwd := where
		if !folder {
			cwd = parentPath(where)
		}
		j.note("Running: " + r.Command)
		n.startRemote(l, j, msg{T: "exec", ID: j.id, Command: r.Command, Cwd: cwd, Shell: r.Shell, Timeout: r.Timeout, Origin: r.Origin, Confirmed: true})
	}()
	return j, nil
}
