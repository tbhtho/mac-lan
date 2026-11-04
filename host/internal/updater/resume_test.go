package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// rangeServer serves one asset over real HTTP with Range support. It can drop
// the connection after a number of bytes, or ignore Range requests entirely.
type rangeServer struct {
	mu          sync.Mutex
	data        []byte
	cutAfter    int  // >0: next response ends after this many body bytes
	ignoreRange bool // answer every request with the whole file
	ranges      []string
	sent        int
}

func (s *rangeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.ranges = append(s.ranges, r.Header.Get("Range"))
	cut, data, ignore := s.cutAfter, s.data, s.ignoreRange
	s.cutAfter = 0
	s.mu.Unlock()
	if cut > 0 {
		// Declare the whole length, send part of it and drop the connection.
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		w.Write(data[:cut])
		s.count(cut)
		return
	}
	if ignore {
		r.Header.Del("Range")
	}
	counter := &countingWriter{ResponseWriter: w, server: s}
	http.ServeContent(counter, r, "asset", time.Time{}, bytes.NewReader(data))
}

func (s *rangeServer) count(n int) { s.mu.Lock(); s.sent += n; s.mu.Unlock() }
func (s *rangeServer) snapshot() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ranges...), s.sent
}
func (s *rangeServer) reset() { s.mu.Lock(); s.ranges, s.sent = nil, 0; s.mu.Unlock() }

type countingWriter struct {
	http.ResponseWriter
	server *rangeServer
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.ResponseWriter.Write(b)
	c.server.count(n)
	return n, err
}

// resumeFixture returns an engine whose signed feed downloads from a local
// HTTP server, plus that server and the signed package bytes.
func resumeFixture(t *testing.T) (*Engine, *rangeServer, []byte) {
	t.Helper()
	s, p, key, _ := testFeed(t)
	commit := p.Releases[0].Commit
	// Pad the package so a cut in the middle leaves a meaningful partial.
	data := standaloneFixture(t, "0.2.0", commit)
	data = append(append(append([]byte(nil), data[:2]...), bytes.Repeat([]byte{'P'}, 64<<10)...), data[2:]...)
	sum := sha256.Sum256(data)
	p.Releases[0].Assets[0].Name = "mac-lan.exe"
	p.Releases[0].Assets[0].Size = int64(len(data))
	p.Releases[0].Assets[0].SHA256 = hex.EncodeToString(sum[:])
	feed := signFeed(t, p, key)
	server := &rangeServer{data: data}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	target, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.client.Transport = feedTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/feed.json") {
			return response(feed), nil
		}
		if r.URL.String() != publicDownloadBase+"v0.2.0/mac-lan.exe" {
			t.Errorf("unexpected download URL %s", r.URL)
		}
		out := r.Clone(r.Context())
		out.URL.Scheme, out.URL.Host, out.Host = "http", target.Host, ""
		return http.DefaultTransport.RoundTrip(out)
	})
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	install := filepath.Join(base, "mac-lan.exe")
	if err = os.WriteFile(install, standaloneFixture(t, "0.1.0", commit), 0600); err != nil {
		t.Fatal(err)
	}
	return &Engine{Current: "0.1.0", OS: "windows", Arch: "amd64", Root: filepath.Join(base, "updates"), Source: s, Verify: s.Verify}, server, data
}

func stageDirs(t *testing.T, root string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "stage-*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func partialPath(t *testing.T, e *Engine) string {
	t.Helper()
	plan, err := e.Check(context.Background(), "stable")
	if err != nil {
		t.Fatal(err)
	}
	key := downloadKey(plan)
	if key == "" {
		t.Fatal("signed plan has no download key")
	}
	return filepath.Join(e.Root, downloadsFolder, key)
}

func TestStageResumesInterruptedDownloadWithRange(t *testing.T) {
	e, server, data := resumeFixture(t)
	half := len(data) / 2
	server.cutAfter = half
	if _, err := e.Stage(context.Background(), "stable"); err == nil {
		t.Fatal("interrupted download staged")
	}
	partial := partialPath(t, e)
	info, err := os.Stat(partial)
	if err != nil || info.Size() != int64(half) {
		t.Fatal("partial download was not kept for resuming", info, err)
	}
	if dirs := stageDirs(t, e.Root); len(dirs) != 0 {
		t.Fatal("an interrupted download created a stage folder", dirs)
	}
	server.reset()
	stage, err := e.Stage(context.Background(), "stable")
	if err != nil || !stage.CanApply {
		t.Fatal(stage, err)
	}
	ranges, sent := server.snapshot()
	if len(ranges) != 1 || ranges[0] != "bytes="+strconv.Itoa(half)+"-" || sent != len(data)-half {
		t.Fatal("download did not resume from the partial file", ranges, sent)
	}
	if got, _ := os.ReadFile(filepath.Join(stage.Directory, "mac-lan.exe")); !bytes.Equal(got, data) {
		t.Fatal("resumed archive differs from the signed package")
	}
	if fileExists(partial) {
		t.Fatal("completed partial left behind")
	}
	// A later poll reuses the verified stage instead of downloading or creating another folder.
	server.reset()
	again, err := e.Stage(context.Background(), "stable")
	if err != nil || !again.CanApply || again.Directory != stage.Directory {
		t.Fatal("completed stage was not reused", again, err)
	}
	if ranges, _ := server.snapshot(); len(ranges) != 0 {
		t.Fatal("reused stage downloaded again", ranges)
	}
	if dirs := stageDirs(t, e.Root); len(dirs) != 1 {
		t.Fatal("polling created extra stage folders", dirs)
	}
	install := filepath.Join(filepath.Dir(e.Root), "mac-lan.exe")
	if _, err = e.Apply(context.Background(), again.Directory, install, again.Plan.confirmation(), false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(install); !bytes.Equal(got, data) {
		t.Fatal("resumed update was not installed")
	}
}

func TestStageRejectsResumedDownloadWithWrongHash(t *testing.T) {
	e, server, data := resumeFixture(t)
	partial := partialPath(t, e)
	if err := os.MkdirAll(filepath.Dir(partial), 0700); err != nil {
		t.Fatal(err)
	}
	// Same length as a genuine first half, different bytes.
	corrupt := append([]byte(nil), data[:len(data)/2]...)
	corrupt[0] ^= 0xff
	if err := os.WriteFile(partial, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Stage(context.Background(), "stable"); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatal("resumed file with a wrong hash was accepted", err)
	}
	if ranges, _ := server.snapshot(); len(ranges) != 1 || ranges[0] == "" {
		t.Fatal("corrupt partial was not resumed with Range", ranges)
	}
	if fileExists(partial) {
		t.Fatal("mismatched download kept for reuse")
	}
	if dirs := stageDirs(t, e.Root); len(dirs) != 0 {
		t.Fatal("mismatched download created a stage", dirs)
	}
	server.reset()
	stage, err := e.Stage(context.Background(), "stable")
	if err != nil || !stage.CanApply {
		t.Fatal("fresh download after mismatch failed", stage, err)
	}
	if ranges, sent := server.snapshot(); len(ranges) != 1 || ranges[0] != "" || sent != len(data) {
		t.Fatal("download after a mismatch did not restart from zero", ranges, sent)
	}
}

func TestStageRestartsWhenServerIgnoresRange(t *testing.T) {
	e, server, data := resumeFixture(t)
	server.cutAfter = len(data) / 3
	if _, err := e.Stage(context.Background(), "stable"); err == nil {
		t.Fatal("interrupted download staged")
	}
	server.ignoreRange = true
	server.reset()
	stage, err := e.Stage(context.Background(), "stable")
	if err != nil || !stage.CanApply {
		t.Fatal(stage, err)
	}
	if got, _ := os.ReadFile(filepath.Join(stage.Directory, "mac-lan.exe")); !bytes.Equal(got, data) {
		t.Fatal("full response did not replace the partial file")
	}
}

func TestDownloadResumeRejectsWrongContentRange(t *testing.T) {
	s, p, key, data := testFeed(t)
	feed := signFeed(t, p, key)
	s.client.Transport = feedTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/feed.json") {
			return response(feed), nil
		}
		resp := response(data[2:])
		resp.StatusCode = http.StatusPartialContent
		resp.Header.Set("Content-Range", "bytes 2-99/100") // another file's range
		return resp, nil
	})
	e := Engine{Current: "0.1.0", OS: "windows", Arch: "amd64", Source: s}
	plan, err := e.Check(context.Background(), "stable")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "download.partial")
	if err = os.WriteFile(path, data[:2], 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.DownloadResume(context.Background(), plan, path); err == nil {
		t.Fatal("mismatched Content-Range accepted")
	}
	if fileExists(path) {
		t.Fatal("partial kept after a range mismatch")
	}
	// Plain downloads keep their all-or-nothing behavior.
	if err = s.Download(context.Background(), plan, path); err == nil {
		t.Fatal("206 accepted without a Range request")
	}
}
