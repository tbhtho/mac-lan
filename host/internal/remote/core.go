package remote

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxOutputBytes   = 1 << 20  // output kept per command for late readers
	outputChunk      = 32 << 10 // largest piece of output in one message
	fileChunk        = 192 << 10
	maxMessage       = 4 << 20
	maxJobsKept      = 200
	maxRunning       = 8
	maxTransferFiles = 5000
	// A command keeps running this long after its connection drops, waiting for
	// the same paired computer to return and pick its output up again.
	detachGrace    = 2 * time.Minute
	defaultTimeout = 30 * time.Minute
	maxTimeout     = 12 * time.Hour
	confirmTimeout = 60 * time.Second
)

// msg is one JSON message on the link between the two computers.
type msg struct {
	T           string  `json:"t"`
	ID          string  `json:"id,omitempty"`
	RID         string  `json:"rid,omitempty"`
	OK          bool    `json:"ok,omitempty"`
	Err         string  `json:"err,omitempty"`
	Code        string  `json:"code,omitempty"`
	Command     string  `json:"command,omitempty"`
	Cwd         string  `json:"cwd,omitempty"`
	Shell       string  `json:"shell,omitempty"`
	Origin      string  `json:"origin,omitempty"`
	Timeout     int     `json:"timeout,omitempty"`
	Interactive bool    `json:"interactive,omitempty"`
	Confirmed   bool    `json:"confirmed,omitempty"`
	Off         int64   `json:"off,omitempty"`
	S           string  `json:"s,omitempty"`
	D           string  `json:"d,omitempty"`
	State       string  `json:"state,omitempty"`
	Exit        *int    `json:"exit,omitempty"`
	Path        string  `json:"path,omitempty"`
	Size        int64   `json:"size,omitempty"`
	Overwrite   bool    `json:"overwrite,omitempty"`
	Sha         string  `json:"sha,omitempty"`
	Name        string  `json:"name,omitempty"`
	Platform    string  `json:"platform,omitempty"`
	Version     string  `json:"version,omitempty"`
	Accept      bool    `json:"accept,omitempty"`
	Dir         bool    `json:"dir,omitempty"`
	X           bool    `json:"x,omitempty"` // the file is executable
	Home        string  `json:"home,omitempty"`
	User        string  `json:"user,omitempty"`
	OS          string  `json:"os,omitempty"`
	Arch        string  `json:"arch,omitempty"`
	Width       int     `json:"width,omitempty"`
	Height      int     `json:"height,omitempty"`
	More        bool    `json:"more,omitempty"` // the listing was cut short
	Keep        bool    `json:"keep,omitempty"` // a replaced file keeps its permissions
	Entries     []entry `json:"entries,omitempty"`
}

type entry struct {
	Rel  string `json:"rel"`
	Size int64  `json:"size"`
	Dir  bool   `json:"dir,omitempty"`
	Mod  int64  `json:"mod,omitempty"` // modification time, Unix seconds
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// ring keeps the most recent output of a command with absolute byte offsets, so
// a reader that reconnects continues exactly where it stopped.
type ring struct {
	chunks []chunk
	size   int
	next   int64
}
type chunk struct {
	off    int64
	stream string // "o" stdout, "e" stderr, "i" mac-lan's own notes
	data   string
}

func (r *ring) add(stream, data string) int64 {
	off := r.next
	r.chunks = append(r.chunks, chunk{off, stream, data})
	r.size += len(data)
	r.next += int64(len(data))
	for r.size > maxOutputBytes && len(r.chunks) > 1 {
		r.size -= len(r.chunks[0].data)
		r.chunks = r.chunks[1:]
	}
	return off
}

// since returns the chunks at or after off, whether older output was dropped,
// and the offset to ask for next.
func (r *ring) since(off int64) ([]chunk, bool, int64) {
	truncated := len(r.chunks) > 0 && off < r.chunks[0].off
	var out []chunk
	for _, c := range r.chunks {
		end := c.off + int64(len(c.data))
		if end <= off {
			continue
		}
		if c.off < off {
			c = chunk{off, c.stream, c.data[off-c.off:]}
		}
		out = append(out, c)
	}
	return out, truncated, r.next
}

// utf8Stream turns raw process output into valid UTF-8 text, holding back a
// multi-byte character that was split across two reads.
type utf8Stream struct{ carry []byte }

func (u *utf8Stream) text(b []byte) string {
	data := append(u.carry, b...)
	u.carry = nil
	cut := len(data)
	for i := 1; i <= 3 && i <= len(data); i++ {
		c := data[len(data)-i]
		if c < 0x80 {
			break
		}
		if c >= 0xC0 { // start byte: complete only if enough bytes follow it
			need := 2
			if c >= 0xF0 {
				need = 4
			} else if c >= 0xE0 {
				need = 3
			}
			if i < need {
				cut = len(data) - i
			}
			break
		}
	}
	u.carry = append([]byte(nil), data[cut:]...)
	return strings.ToValidUTF8(string(data[:cut]), "�")
}

func (u *utf8Stream) flush() string {
	if len(u.carry) == 0 {
		return ""
	}
	s := strings.ToValidUTF8(string(u.carry), "�")
	u.carry = nil
	return s
}

func validText(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// StateDir is mac-lan's per-user private folder on this computer.
func StateDir() (string, error) {
	var root string
	var err error
	if runtime.GOOS == "windows" {
		root, err = os.UserCacheDir() // %LOCALAPPDATA%
	} else {
		root, err = os.UserConfigDir()
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "MAC-DOW"), nil
}

// resolvePath expands ~ and makes a relative path absolute against base (the
// home folder when base is empty).
func resolvePath(path, base string) (string, error) {
	if !validText(path, 4096) || !validText(base, 4096) {
		return "", errors.New("invalid path")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot locate the home folder")
	}
	expand := func(p string) string {
		if p == "~" {
			return home
		}
		if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
			return filepath.Join(home, p[2:])
		}
		return p
	}
	path, base = expand(filepath.FromSlash(path)), expand(filepath.FromSlash(base))
	if base == "" {
		base = home
	} else if !filepath.IsAbs(base) {
		base = filepath.Join(home, base)
	}
	if path == "" {
		return filepath.Clean(base), nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path), nil
}

// Settings are this computer's two opt-in switches. Both are off until the user
// turns them on in mac-lan.
type Settings struct {
	// Accept lets the paired computer run commands and read or write files here.
	Accept bool `json:"accept"`
	// Agents lets local programs (an MCP server, the command-line shell) use the
	// connection to the paired computer.
	Agents bool `json:"agents"`
	// Trusted lists folders on the paired computer in which files may be
	// replaced without asking each time. Only the mac-lan window can change it.
	Trusted []string `json:"trusted,omitempty"`
}

const maxTrusted = 32

type settingsStore struct {
	mu   sync.Mutex
	path string
	now  Settings
	mod  time.Time // the file's time when it was last read or written
}

// refresh picks up a change made by another mac-lan process on this computer
// (the app and its standby service share the switches).
func (s *settingsStore) refresh() {
	if s.path == "" {
		return
	}
	info, err := os.Lstat(s.path)
	if err != nil || !info.Mode().IsRegular() || info.Size() >= 64<<10 || info.ModTime().Equal(s.mod) {
		return
	}
	if data, err := os.ReadFile(s.path); err == nil {
		var next Settings
		if json.Unmarshal(data, &next) == nil {
			s.now, s.mod = next, info.ModTime()
		}
	}
}

func loadSettings(dir string) *settingsStore {
	s := &settingsStore{path: filepath.Join(dir, "remote-settings.json")}
	if dir == "" {
		s.path = ""
		return s
	}
	s.refresh()
	return s
}

func (s *settingsStore) get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh()
	now := s.now
	now.Trusted = append([]string(nil), now.Trusted...)
	return now
}

func (s *settingsStore) set(next Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		data, _ := json.Marshal(next)
		if err := writePrivate(s.path, data); err != nil {
			return err
		}
		if info, err := os.Lstat(s.path); err == nil {
			s.mod = info.ModTime()
		}
	}
	s.now = next
	return nil
}

// writePrivate replaces path atomically with owner-only permissions.
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".mac-lan-private-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(data)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

// audit appends one line per accepted or refused request to a bounded private log.
type audit struct {
	mu   sync.Mutex
	path string
}

func (a *audit) record(kind, origin, detail, outcome string) {
	if a == nil || a.path == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if info, err := os.Stat(a.path); err == nil && info.Size() > 1<<20 {
		_ = os.Rename(a.path, a.path+".1")
	}
	file, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	line, _ := json.Marshal(map[string]string{"time": time.Now().UTC().Format(time.RFC3339), "kind": kind, "origin": origin, "detail": detail, "outcome": outcome})
	_, _ = file.Write(append(line, '\n'))
}
