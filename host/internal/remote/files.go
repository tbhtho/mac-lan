package remote

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// This file holds what makes the link practical for a program rather than a
// person at a terminal: reading, writing and editing a text file in one step,
// listing a folder, a picture of the other computer's screen, and folders in
// which replacing files does not ask every time.

const (
	maxReadBytes  = 8 << 20 // largest file read_file or edit_file will fetch
	maxWriteBytes = 4 << 20
	maxDirEntries = 2000
	maxShotBytes  = 2800 << 10 // must fit one link message after base64
)

// remoteClean is the absolute, tidy form of a path on the paired computer, or
// "" when that computer has not told us its home folder (an older mac-lan).
func remoteClean(peer msg, p string) string {
	if peer.Home == "" || p == "" {
		return ""
	}
	windows := peer.Platform == "windows"
	slash := func(s string) string {
		if windows {
			s = strings.ReplaceAll(s, `\`, "/")
		}
		return s
	}
	p = slash(p)
	home := strings.TrimRight(slash(peer.Home), "/")
	switch {
	case p == "~":
		p = home
	case strings.HasPrefix(p, "~/"):
		p = home + "/" + p[2:]
	case strings.HasPrefix(p, "/"), windows && len(p) >= 3 && p[1] == ':' && p[2] == '/':
	default:
		p = home + "/" + p
	}
	return path.Clean(p)
}

// remoteKey compares paths; both computers' default file systems ignore case.
func remoteKey(peer msg, p string) string { return strings.ToLower(remoteClean(peer, p)) }

// trustable refuses folders that are too broad to trust as a whole: the home
// folder, anything above it, and the top level of a disk.
func trustable(peer msg, folder string) bool {
	key, home := remoteKey(peer, folder), remoteKey(peer, "~")
	if key == "" || key == home || strings.HasPrefix(home+"/", key+"/") {
		return false
	}
	rest := key
	if len(rest) >= 2 && rest[1] == ':' {
		rest = rest[2:]
	}
	return strings.Count(strings.Trim(rest, "/"), "/") >= 1
}

// trusts reports whether p on the paired computer lies in a trusted folder.
func (n *Node) trusts(peer msg, p string) bool {
	key := remoteKey(peer, p)
	if key == "" {
		return false
	}
	for _, folder := range n.settings.get().Trusted {
		if trusted := remoteKey(peer, folder); trusted != "" && (key == trusted || strings.HasPrefix(key, trusted+"/")) {
			return true
		}
	}
	return false
}

// Trust adds a folder on the paired computer in which files may be replaced
// without asking. Only a person may call this (the window, or their answer to
// an approval prompt).
func (n *Node) Trust(folder string) error {
	l := n.current()
	if l == nil {
		return errors.New("connect the two computers first")
	}
	peer, _ := l.peerInfo()
	clean := remoteClean(peer, strings.TrimSpace(folder))
	if clean == "" {
		return errors.New("update mac-lan on the other computer to use trusted folders")
	}
	if !validText(clean, 1024) || !trustable(peer, clean) {
		return errors.New("choose a project folder, not the home folder or a whole disk")
	}
	settings := n.settings.get()
	for _, existing := range settings.Trusted {
		if remoteKey(peer, existing) == strings.ToLower(clean) {
			return nil
		}
	}
	if len(settings.Trusted) >= maxTrusted {
		return errors.New("too many trusted folders; remove one first")
	}
	settings.Trusted = append(settings.Trusted, clean)
	return n.SetSettings(settings)
}

// Untrust removes a trusted folder.
func (n *Node) Untrust(folder string) error {
	settings := n.settings.get()
	kept := settings.Trusted[:0]
	for _, existing := range settings.Trusted {
		if !strings.EqualFold(existing, strings.TrimSpace(folder)) {
			kept = append(kept, existing)
		}
	}
	settings.Trusted = kept
	return n.SetSettings(settings)
}

// approveReplace asks before files on the paired computer are replaced, and
// offers to trust folder from now on when that is a reasonable thing to trust.
func (n *Node) approveReplace(j *Job, peer msg, request ConfirmRequest, folder string) bool {
	clean := remoteClean(peer, folder)
	if clean != "" && trustable(peer, clean) {
		request.Remember = "Always allow replacing files in " + clean
	}
	reply := n.approve(j, request)
	if reply == Deny {
		return false
	}
	if reply == AllowAlways {
		if err := n.Trust(clean); err != nil {
			j.note("The folder could not be remembered: " + err.Error())
		}
	}
	return true
}

// current capabilities are announced by the home folder in the greeting.
func modern(peer msg) error {
	if peer.Home == "" {
		return errors.New("update mac-lan on " + peer.Name + " to use this; it runs an older version")
	}
	return nil
}

// scratch returns a private folder for a short-lived local copy.
func (n *Node) scratch() (string, error) {
	if n.dir == "" {
		return os.MkdirTemp("", "mac-lan-*")
	}
	root := filepath.Join(n.dir, "tmp")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "f-*")
}

func quietJob(origin string) *Job {
	return &Job{id: newID(), kind: "transfer", origin: origin, started: time.Now(), changed: make(chan struct{}), cancel: make(chan struct{})}
}

// ReadFile fetches a file of at most maxReadBytes from the paired computer.
func (n *Node) ReadFile(device, remote, origin string) ([]byte, error) {
	if remote == "" || !validText(remote, 4096) {
		return nil, errors.New("give the path of a file on the other computer")
	}
	l, err := n.target(device)
	if err != nil {
		return nil, err
	}
	folder, err := n.scratch()
	if err != nil {
		return nil, errors.New("cannot use mac-lan's private folder on this computer")
	}
	defer os.RemoveAll(folder)
	local := filepath.Join(folder, "file")
	if err = getFile(l, quietJob(origin), remote, local, false, maxReadBytes); err != nil {
		return nil, err
	}
	return os.ReadFile(local)
}

// WriteFile saves data as a file on the paired computer. Replacing an existing
// file asks a person unless it is in a trusted folder; then the file keeps its
// permissions.
func (n *Node) WriteFile(device, remote string, data []byte, overwrite bool, origin, label string) (*Job, error) {
	if remote == "" || !validText(remote, 4096) {
		return nil, errors.New("give the path of a file on the other computer")
	}
	if len(data) > maxWriteBytes {
		return nil, errors.New("that is too large to write in one step; save it to a file and use transfer_file")
	}
	l, err := n.target(device)
	if err != nil {
		return nil, err
	}
	peer, _ := l.peerInfo()
	folder, err := n.scratch()
	if err != nil {
		return nil, errors.New("cannot use mac-lan's private folder on this computer")
	}
	local := filepath.Join(folder, "file")
	if err = os.WriteFile(local, data, 0o600); err != nil {
		_ = os.RemoveAll(folder)
		return nil, errors.New("cannot use mac-lan's private folder on this computer")
	}
	ask := overwrite && !n.trusts(peer, remote)
	state := "running"
	if ask {
		state = "awaiting-confirmation"
	}
	if label == "" {
		label = "write"
	}
	j := n.newJob("transfer", label+" "+peer.Name+":"+remote, "", origin, state)
	go func() {
		defer n.changed()
		defer os.RemoveAll(folder)
		if ask {
			if !n.approveReplace(j, peer, ConfirmRequest{Title: "Replace this file on " + peer.Name + "?", Detail: remote, Reason: "The file's contents will be replaced (" + label + ").", Origin: origin, Target: peer.Name}, parentPath(remote)) {
				return
			}
			j.mu.Lock()
			j.state = "running"
			j.touch()
			j.mu.Unlock()
		}
		j.mu.Lock()
		j.total = int64(len(data))
		j.mu.Unlock()
		saved, err := putFile(l, j, local, remote, "", overwrite, true)
		if err != nil {
			state := "failed"
			if j.cancelled() {
				state = "cancelled"
			}
			j.finish(state, "Not saved: "+err.Error())
			return
		}
		j.note(fmt.Sprintf("Saved %s (%s), verified with SHA-256.", saved, sizeText(int64(len(data)))))
		j.finish("succeeded", "")
	}()
	return j, nil
}

// EditFile replaces old with replacement in a text file on the paired
// computer. old must occur exactly once unless all is set.
func (n *Node) EditFile(device, remote, old, replacement string, all bool, origin string) (*Job, int, error) {
	if old == "" {
		return nil, 0, errors.New("old_text must not be empty; use write_file to create a file")
	}
	if old == replacement {
		return nil, 0, errors.New("old_text and new_text are the same")
	}
	data, err := n.ReadFile(device, remote, origin)
	if err != nil {
		return nil, 0, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, 0, errors.New("that is not a text file")
	}
	text := string(data)
	count := strings.Count(text, old)
	if count == 0 && strings.Contains(text, "\r\n") && !strings.Contains(old, "\r\n") {
		// The file uses Windows line endings and the caller wrote plain ones.
		old, replacement = strings.ReplaceAll(old, "\n", "\r\n"), strings.ReplaceAll(strings.ReplaceAll(replacement, "\r\n", "\n"), "\n", "\r\n")
		count = strings.Count(text, old)
	}
	switch {
	case count == 0:
		return nil, 0, errors.New("old_text was not found in the file; read the file again and copy the text exactly, including indentation")
	case count > 1 && !all:
		return nil, 0, errors.New("old_text occurs " + strconv.Itoa(count) + " times; include more surrounding lines to make it unique, or set replace_all")
	}
	j, err := n.WriteFile(device, remote, []byte(strings.ReplaceAll(text, old, replacement)), true, origin, "edit")
	return j, count, err
}

// DirEntry is one name in a folder on the paired computer.
type DirEntry struct {
	Name     string `json:"name"`
	Folder   bool   `json:"folder,omitempty"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
}

// ListDir lists one folder (not its subfolders) on the paired computer.
func (n *Node) ListDir(device, remote, origin string) (string, []DirEntry, bool, error) {
	if !validText(remote, 4096) {
		return "", nil, false, errors.New("invalid path")
	}
	l, err := n.target(device)
	if err != nil {
		return "", nil, false, err
	}
	peer, _ := l.peerInfo()
	if err = modern(peer); err != nil {
		return "", nil, false, err
	}
	reply, err := l.call(msg{T: "dir", Path: remote, Origin: origin}, 30*time.Second)
	if err != nil {
		return "", nil, false, err
	}
	if !reply.OK {
		return "", nil, false, errors.New(reply.Err)
	}
	out := make([]DirEntry, 0, len(reply.Entries))
	for _, e := range reply.Entries {
		entry := DirEntry{Name: e.Rel, Folder: e.Dir, Size: e.Size}
		if e.Mod > 0 {
			entry.Modified = time.Unix(e.Mod, 0).UTC().Format(time.RFC3339)
		}
		out = append(out, entry)
	}
	return reply.Path, out, reply.More, nil
}

func (l *link) handleDir(m msg) {
	if l.disabled(m) {
		return
	}
	dir, err := resolvePath(m.Path, "")
	if err != nil {
		l.reply(m, msg{Err: err.Error()})
		return
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		if info, statErr := os.Stat(dir); statErr == nil && !info.IsDir() {
			l.reply(m, msg{Err: "that is a file, not a folder: " + dir})
			return
		}
		l.reply(m, msg{Err: "cannot list " + dir + ": " + err.Error()})
		return
	}
	reply := msg{OK: true, Path: dir}
	for _, name := range names {
		if len(reply.Entries) >= maxDirEntries {
			reply.More = true
			break
		}
		// Stat follows a link, so a linked folder is shown as a folder.
		info, err := os.Stat(filepath.Join(dir, name.Name()))
		if err != nil {
			if info, err = name.Info(); err != nil {
				continue
			}
		}
		e := entry{Rel: name.Name(), Dir: info.IsDir(), Mod: info.ModTime().Unix()}
		if !info.IsDir() {
			e.Size = info.Size()
		}
		reply.Entries = append(reply.Entries, e)
	}
	l.node.log.record("folder-list", m.Origin, dir, "sent")
	l.reply(m, reply)
}

// Screenshot returns a JPEG of the paired computer's main display, at most
// width pixels wide.
func (n *Node) Screenshot(device string, width int, origin string) ([]byte, int, int, error) {
	l, err := n.target(device)
	if err != nil {
		return nil, 0, 0, err
	}
	peer, _ := l.peerInfo()
	if err = modern(peer); err != nil {
		return nil, 0, 0, err
	}
	reply, err := l.call(msg{T: "shot", Width: width, Origin: origin}, 45*time.Second)
	if err != nil {
		return nil, 0, 0, err
	}
	if !reply.OK {
		return nil, 0, 0, errors.New(reply.Err)
	}
	data, err := base64.StdEncoding.DecodeString(reply.D)
	if err != nil || len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, 0, 0, errors.New("the other computer sent an invalid picture")
	}
	return data, reply.Width, reply.Height, nil
}

func shotWidth(width int) int {
	switch {
	case width <= 0:
		return 1600
	case width < 320:
		return 320
	case width > 3840:
		return 3840
	}
	return width
}

// handleShot captures off the reading goroutine; a capture takes a moment.
func (l *link) handleShot(m msg) {
	if l.disabled(m) {
		return
	}
	go func() {
		data, width, height, err := captureScreen(shotWidth(m.Width))
		if err == nil && len(data) > maxShotBytes {
			err = errors.New("the picture is too large to send; ask for a smaller max_width")
		}
		if err != nil {
			l.node.log.record("screenshot", m.Origin, "", "failed: "+err.Error())
			l.reply(m, msg{Err: err.Error()})
			return
		}
		l.node.log.record("screenshot", m.Origin, strconv.Itoa(width)+"x"+strconv.Itoa(height), "sent")
		l.reply(m, msg{OK: true, D: base64.StdEncoding.EncodeToString(data), Width: width, Height: height})
	}()
}

// shrink scales a picture down to at most width pixels wide by averaging.
func shrink(source *image.RGBA, width int) *image.RGBA {
	bounds := source.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= width || width < 1 || h < 1 {
		return source
	}
	height := h * width / w
	if height < 1 {
		height = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		y0, y1 := y*h/height, (y+1)*h/height
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < width; x++ {
			x0, x1 := x*w/width, (x+1)*w/width
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, count uint32
			for sy := y0; sy < y1; sy++ {
				row := source.Pix[sy*source.Stride+x0*4 : sy*source.Stride+x1*4]
				for i := 0; i < len(row); i += 4 {
					r += uint32(row[i])
					g += uint32(row[i+1])
					b += uint32(row[i+2])
					count++
				}
			}
			at := y*out.Stride + x*4
			out.Pix[at], out.Pix[at+1], out.Pix[at+2], out.Pix[at+3] = uint8(r/count), uint8(g/count), uint8(b/count), 255
		}
	}
	return out
}

func userName() string {
	for _, name := range []string{"USER", "USERNAME", "LOGNAME"} {
		if value := os.Getenv(name); value != "" && validText(value, 128) {
			return value
		}
	}
	return ""
}
