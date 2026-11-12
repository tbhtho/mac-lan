package remote

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// link is one authenticated WebSocket between the two computers. Either side
// may send requests; both serve them subject to their own settings.
type link struct {
	node *Node
	c    *websocket.Conn
	wmu  sync.Mutex

	pmu       sync.Mutex
	pending   map[string]chan msg
	downloads map[string]*download
	uploads   map[string]*upload
	sending   map[string]chan struct{}
	peer      msg
	known     bool
	standby   bool // connected through the Mac's standby service
	learnOnce sync.Once

	closed    chan struct{}
	closeOnce sync.Once
	dropOnce  sync.Once
}

// upload is a file arriving on this computer; download is one this computer asked for.
type upload struct {
	file      *os.File
	temp      string
	path      string
	size, got int64
	hash      hash.Hash
	exec      bool
	mode      os.FileMode // permissions to keep; zero chooses by exec
	failed    string
}
type download struct {
	file *os.File
	hash hash.Hash
	got  int64
	job  *Job
	done chan error
}

func newLink(n *Node, c *websocket.Conn) *link {
	return &link{node: n, c: c, pending: map[string]chan msg{}, downloads: map[string]*download{}, uploads: map[string]*upload{}, sending: map[string]chan struct{}{}, closed: make(chan struct{})}
}

func (l *link) close() {
	l.closeOnce.Do(func() {
		close(l.closed)
		_ = l.c.Close()
	})
}

func (l *link) peerInfo() (msg, bool) {
	l.pmu.Lock()
	defer l.pmu.Unlock()
	return l.peer, l.known
}

func (l *link) send(m msg) error {
	select {
	case <-l.closed:
		return errors.New("connection closed")
	default:
	}
	l.wmu.Lock()
	defer l.wmu.Unlock()
	_ = l.c.SetWriteDeadline(time.Now().Add(20 * time.Second))
	if err := l.c.WriteJSON(m); err != nil {
		l.close()
		return errors.New("connection closed")
	}
	return nil
}

// call sends a request and waits for the reply carrying the same rid.
func (l *link) call(m msg, timeout time.Duration) (msg, error) {
	if m.RID == "" {
		m.RID = newID()
	}
	reply := make(chan msg, 1)
	l.pmu.Lock()
	l.pending[m.RID] = reply
	l.pmu.Unlock()
	defer func() {
		l.pmu.Lock()
		delete(l.pending, m.RID)
		l.pmu.Unlock()
	}()
	if err := l.send(m); err != nil {
		return msg{}, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-reply:
		return r, nil
	case <-l.closed:
		return msg{}, errors.New("the connection dropped")
	case <-timer.C:
		return msg{}, errors.New("the other computer did not answer in time")
	}
}

func (l *link) reply(request msg, r msg) {
	r.T, r.RID = "reply", request.RID
	_ = l.send(r)
}

// run reads messages until the connection ends.
func (l *link) run() {
	defer l.close()
	l.c.SetReadLimit(maxMessage)
	alive := func() { _ = l.c.SetReadDeadline(time.Now().Add(60 * time.Second)) }
	l.c.SetPongHandler(func(string) error { alive(); return nil })
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-l.closed:
				return
			case <-ticker.C:
				_ = l.c.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
		}
	}()
	for {
		alive()
		var m msg
		if err := l.c.ReadJSON(&m); err != nil {
			return
		}
		l.handle(m)
	}
}

// abandon releases everything tied to a connection that is gone.
func (l *link) abandon() {
	l.pmu.Lock()
	defer l.pmu.Unlock()
	for id, u := range l.uploads {
		_ = u.file.Close()
		_ = os.Remove(u.temp)
		delete(l.uploads, id)
	}
	for id, stop := range l.sending {
		close(stop)
		delete(l.sending, id)
	}
	for id, d := range l.downloads {
		select {
		case d.done <- errors.New("the connection dropped"):
		default:
		}
		delete(l.downloads, id)
	}
}

// disabled answers a request this computer does not accept; true means refused.
func (l *link) disabled(m msg) bool {
	if l.node.settings.get().Accept {
		return false
	}
	l.node.log.record(m.T, m.Origin, m.Command+m.Path, "refused: remote commands are off")
	l.reply(m, msg{Code: "disabled", Err: "Remote commands are switched off on " + l.node.name + ". Its user must turn on \"Allow the paired computer to run commands here\" in mac-lan."})
	return true
}

func (l *link) handle(m msg) {
	switch m.T {
	case "hello":
		if !validText(m.Name, 256) || !validText(m.Platform, 32) || !validText(m.Version, 64) || !validText(m.Shell, 32) {
			return
		}
		if m.Name == "" {
			m.Name = "the other computer"
		}
		l.pmu.Lock()
		l.peer, l.known = m, true
		l.pmu.Unlock()
		l.node.changed()
		if l.node.learns && m.Home != "" {
			// Now, and again from time to time, so a standby link switched on
			// during a long connection is still learned.
			l.learnOnce.Do(func() {
				go func() {
					for {
						l.learnStandby()
						select {
						case <-l.closed:
							return
						case <-time.After(10 * time.Minute):
						}
					}
				}()
			})
		}
	case "reply":
		l.pmu.Lock()
		waiter := l.pending[m.RID]
		l.pmu.Unlock()
		if waiter != nil {
			select {
			case waiter <- m:
			default:
			}
		}
	case "out":
		l.node.output(m)
	case "exit":
		l.node.exited(m)
	case "exec":
		l.handleExec(m)
	case "attach":
		if p := l.node.procs.get(m.ID); p != nil {
			p.attach(m.Off, l.send)
		} else {
			_ = l.send(msg{T: "exit", ID: m.ID, State: "lost", Err: "That command is no longer known on " + l.node.name + "; it ended or mac-lan restarted there."})
		}
	case "cancel":
		if p := l.node.procs.get(m.ID); p != nil {
			p.end("cancelled")
		}
	case "stdin":
		if p := l.node.procs.get(m.ID); p != nil && len(m.D) <= 64<<10 {
			p.write(m.D)
		}
	case "resolve":
		if l.disabled(m) {
			return
		}
		path, err := resolvePath(m.Path, m.Cwd)
		if err == nil {
			if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
				err = errors.New("no such folder: " + path)
			}
		}
		if err != nil {
			l.reply(m, msg{Err: err.Error()})
			return
		}
		l.reply(m, msg{OK: true, Path: path})
	case "list":
		l.handleList(m)
	case "dir":
		l.handleDir(m)
	case "shot":
		l.handleShot(m)
	case "standby":
		l.handleStandby(m)
	case "put":
		l.handlePut(m)
	case "get":
		l.handleGet(m)
	case "chunk":
		l.handleChunk(m)
	case "end":
		l.handleEnd(m)
	case "abort":
		l.pmu.Lock()
		if stop := l.sending[m.RID]; stop != nil {
			close(stop)
			delete(l.sending, m.RID)
		}
		if u := l.uploads[m.RID]; u != nil {
			_ = u.file.Close()
			_ = os.Remove(u.temp)
			delete(l.uploads, m.RID)
		}
		l.pmu.Unlock()
	}
}

func (l *link) handleExec(m msg) {
	if l.disabled(m) {
		return
	}
	if reason := NeedsConfirmation(m.Command); reason != "" && !m.Confirmed {
		l.node.log.record("exec", m.Origin, m.Command, "refused: not confirmed")
		l.reply(m, msg{Code: "needs-confirmation", Err: "This command " + reason + " and was not confirmed by a person."})
		return
	}
	_, err := l.node.procs.start(m.ID, m.Command, m.Cwd, m.Shell, time.Duration(m.Timeout)*time.Second, m.Interactive, l.send)
	if err != nil {
		l.node.log.record("exec", m.Origin, m.Command, "failed to start: "+err.Error())
		l.reply(m, msg{Err: err.Error()})
		return
	}
	l.node.log.record("exec", m.Origin, m.Command, "started")
	l.reply(m, msg{OK: true})
	l.node.changed()
}

func baseName(path string) string {
	path = strings.TrimRight(path, `/\`)
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	return path
}

func parentPath(path string) string {
	trimmed := strings.TrimRight(path, `/\`)
	if i := strings.LastIndexAny(trimmed, `/\`); i > 0 {
		return trimmed[:i]
	}
	return path
}

func (l *link) handleList(m msg) {
	if l.disabled(m) {
		return
	}
	path, err := resolvePath(m.Path, "")
	if err != nil {
		l.reply(m, msg{Err: err.Error()})
		return
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		l.reply(m, msg{Err: "no such file or folder: " + path})
		return
	}
	if info.Mode().IsRegular() {
		l.reply(m, msg{OK: true, Path: path, Name: filepath.Base(path), Size: info.Size()})
		return
	}
	if !info.IsDir() {
		l.reply(m, msg{Err: "not a regular file or folder: " + path})
		return
	}
	entries, err := walkFiles(path)
	if err != nil {
		l.reply(m, msg{Err: err.Error()})
		return
	}
	l.reply(m, msg{OK: true, Dir: true, Path: path, Name: filepath.Base(path), Entries: entries})
}

// walkFiles lists the regular files under root with slash-separated relative
// paths. Links and special files are skipped, never followed.
func walkFiles(root string) ([]entry, error) {
	var out []entry
	names := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		names += len(rel)
		if len(out) >= maxTransferFiles || names > 1<<20 {
			return errors.New("this folder has too many files for one transfer; send a smaller folder or an archive")
		}
		out = append(out, entry{Rel: rel, Size: info.Size()})
		return nil
	})
	return out, err
}

func (l *link) handlePut(m msg) {
	if l.disabled(m) {
		return
	}
	fail := func(code, text string) {
		l.node.log.record("file-write", m.Origin, m.Path, "refused: "+text)
		l.reply(m, msg{Code: code, Err: text})
	}
	if m.Overwrite && !m.Confirmed {
		fail("needs-confirmation", "Replacing files must be confirmed by a person.")
		return
	}
	path, err := resolvePath(m.Path, "")
	if err != nil || m.Size < 0 || m.Path == "" {
		fail("", "invalid destination")
		return
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		name := m.Name
		if name == "" || name != baseName(name) || name == "." || name == ".." || !validText(name, 255) {
			fail("", "the destination is a folder: "+path)
			return
		}
		path = filepath.Join(path, name)
	}
	var keep os.FileMode
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			fail("", "the destination is a folder: "+path)
			return
		}
		if m.Keep && info.Mode().IsRegular() {
			keep = info.Mode().Perm()
		}
		if !m.Overwrite {
			fail("exists", path+" already exists. Nothing was changed; ask for it to be replaced if that is intended (needs approval).")
			return
		}
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fail("", "cannot create the destination folder: "+err.Error())
		return
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".mac-lan-part-*")
	if err != nil {
		fail("", "cannot write to the destination folder: "+err.Error())
		return
	}
	l.pmu.Lock()
	l.uploads[m.RID] = &upload{file: file, temp: file.Name(), path: path, size: m.Size, hash: sha256.New(), exec: m.X, mode: keep}
	l.pmu.Unlock()
	l.reply(m, msg{OK: true, Path: path})
}

func (l *link) handleChunk(m msg) {
	data, err := base64.StdEncoding.DecodeString(m.D)
	l.pmu.Lock()
	u, d := l.uploads[m.RID], l.downloads[m.RID]
	l.pmu.Unlock()
	switch {
	case u != nil:
		if u.failed != "" {
			return
		}
		if err == nil {
			u.got += int64(len(data))
			if u.got > u.size {
				err = errors.New("more data than announced")
			} else if _, err = u.file.Write(data); err == nil {
				u.hash.Write(data)
			}
		}
		if err != nil {
			u.failed = err.Error()
		}
	case d != nil:
		if err == nil {
			if _, err = d.file.Write(data); err == nil {
				d.hash.Write(data)
				d.got += int64(len(data))
				d.job.mu.Lock()
				d.job.done += int64(len(data))
				d.job.mu.Unlock()
			}
		}
		if err != nil {
			l.finishDownload(m.RID, err)
		}
	}
}

func (l *link) finishDownload(rid string, err error) {
	l.pmu.Lock()
	d := l.downloads[rid]
	delete(l.downloads, rid)
	l.pmu.Unlock()
	if d != nil {
		d.done <- err
	}
}

func (l *link) handleEnd(m msg) {
	l.pmu.Lock()
	u, d := l.uploads[m.RID], l.downloads[m.RID]
	delete(l.uploads, m.RID)
	l.pmu.Unlock()
	switch {
	case u != nil:
		closeErr := u.file.Close()
		text := u.failed
		if text == "" && closeErr != nil {
			text = closeErr.Error()
		}
		if text == "" && (u.got != u.size || hex.EncodeToString(u.hash.Sum(nil)) != m.Sha) {
			text = "the file arrived incomplete or changed; nothing was replaced"
		}
		if text == "" {
			mode := os.FileMode(0o644)
			if u.exec {
				mode = 0o755
			}
			if u.mode != 0 {
				mode = u.mode
			}
			if runtime.GOOS != "windows" {
				_ = os.Chmod(u.temp, mode)
			}
			if err := os.Rename(u.temp, u.path); err != nil {
				text = "cannot save the file: " + err.Error()
			}
		}
		if text != "" {
			_ = os.Remove(u.temp)
			l.node.log.record("file-write", "", u.path, "failed: "+text)
			l.reply(m, msg{Err: text})
			return
		}
		l.node.log.record("file-write", "", u.path, "saved")
		l.reply(m, msg{OK: true, Path: u.path, Size: u.got})
	case d != nil:
		var err error
		if hex.EncodeToString(d.hash.Sum(nil)) != m.Sha {
			err = errors.New("the file arrived incomplete or changed")
		}
		if m.Err != "" {
			err = errors.New(m.Err)
		}
		l.finishDownload(m.RID, err)
	}
}

// handleGet streams a file to the other computer without blocking the reader.
func (l *link) handleGet(m msg) {
	if l.disabled(m) {
		return
	}
	path, err := resolvePath(m.Path, "")
	var info os.FileInfo
	if err == nil {
		info, err = os.Lstat(path)
	}
	if err == nil && info.IsDir() {
		l.reply(m, msg{Err: "that is a folder, not a file: " + path})
		return
	}
	if err != nil || !info.Mode().IsRegular() {
		l.reply(m, msg{Err: "no such file: " + m.Path})
		return
	}
	file, err := os.Open(path)
	if err != nil {
		l.reply(m, msg{Err: "cannot read " + path + ": " + err.Error()})
		return
	}
	stop := make(chan struct{})
	l.pmu.Lock()
	l.sending[m.RID] = stop
	l.pmu.Unlock()
	l.node.log.record("file-read", m.Origin, path, "sent")
	l.reply(m, msg{OK: true, Path: path, Size: info.Size(), X: info.Mode()&0o111 != 0 && runtime.GOOS != "windows"})
	go func() {
		defer file.Close()
		defer func() {
			l.pmu.Lock()
			delete(l.sending, m.RID)
			l.pmu.Unlock()
		}()
		sum := sha256.New()
		buffer := make([]byte, fileChunk)
		for {
			select {
			case <-stop:
				return
			case <-l.closed:
				return
			default:
			}
			n, err := file.Read(buffer)
			if n > 0 {
				sum.Write(buffer[:n])
				if l.send(msg{T: "chunk", RID: m.RID, D: base64.StdEncoding.EncodeToString(buffer[:n])}) != nil {
					return
				}
			}
			if err == io.EOF {
				_ = l.send(msg{T: "end", RID: m.RID, Sha: hex.EncodeToString(sum.Sum(nil))})
				return
			}
			if err != nil {
				_ = l.send(msg{T: "end", RID: m.RID, Err: "reading the file failed: " + err.Error()})
				return
			}
		}
	}()
}
