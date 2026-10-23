package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Private per-release build configuration, deliberately absent from source control.
var Webhook string
var codePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,48}$`)
var events = strings.Fields("startup shutdown discovery-empty discovery-ready discovery-failed pairing-start pairing-approved pairing-denied pairing-failed stream-request stream-approved stream-denied stream-started stream-ended capture-failed decode-failed fatal manual-report bar-docked bar-restored bar-failed permissions-ok permissions-missing listener-ready advertise-ready advertise-failed native-build-started native-build-ready native-build-failed")
var allowedCounts = map[string]bool{"hosts": true, "monitors": true, "screenPermission": true, "controlPermission": true, "width": true, "fps": true, "bitrate": true, "appOnly": true, "paired": true, "streaming": true, "docked": true}

type Report struct {
	Product string         `json:"product"`
	Version string         `json:"version"`
	OS      string         `json:"os"`
	Arch    string         `json:"arch"`
	Role    string         `json:"role"`
	Event   string         `json:"event"`
	Code    string         `json:"code"`
	Time    string         `json:"time"`
	Counts  map[string]int `json:"state,omitempty"`
}

func NewReport(role, event, code string, counts map[string]int) Report {
	if role != "windows" && role != "mac" {
		role = "unknown"
	}
	known := false
	for _, e := range events {
		if event == e {
			known = true
			break
		}
	}
	if !known {
		event = "manual-report"
	}
	if !codePattern.MatchString(code) {
		code = "unknown"
	}
	safe := map[string]int{}
	for key, value := range counts {
		if allowedCounts[key] && value >= 0 && value <= 100000 {
			safe[key] = value
		}
	}
	return Report{"MAC-DOW", updater.BuildConfig().Version, runtime.GOOS, runtime.GOARCH, role, event, code, time.Now().UTC().Format(time.RFC3339), safe}
}
func validEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host != "discord.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return regexp.MustCompile(`^/api/webhooks/[0-9]{17,22}/[A-Za-z0-9_-]+$`).MatchString(u.Path)
}
func deliver(ctx context.Context, client *http.Client, endpoint string, report Report) error {
	if !validEndpoint(endpoint) {
		return errors.New("diagnostic destination unavailable")
	}
	body, _ := json.Marshal(report)
	payload, _ := json.Marshal(map[string]any{"content": "MAC-DOW diagnostic\n```json\n" + string(body) + "\n```", "allowed_mentions": map[string]any{"parse": []string{}}})
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"?wait=true", bytes.NewReader(payload))
		if err != nil {
			return errors.New("diagnostic request unavailable")
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(response.Body, 8192))
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
			if response.StatusCode != 429 && response.StatusCode < 500 {
				return errors.New("diagnostic destination rejected report")
			}
		}
		if attempt == 0 {
			select {
			case <-ctx.Done():
				return errors.New("diagnostic delivery timed out")
			case <-time.After(150 * time.Millisecond):
			}
		}
	}
	return errors.New("diagnostic delivery unavailable; report retained locally")
}

var mu sync.Mutex

type Result struct {
	Sent  bool `json:"sent"`
	Saved bool `json:"saved"`
}

func Record(role, event, code string, counts map[string]int) Result {
	if role != "mac" || runtime.GOOS != "darwin" {
		return Result{}
	} // User requested Mac-only remote diagnostics.
	mu.Lock()
	defer mu.Unlock()
	report := NewReport(role, event, code, counts)
	data, _ := json.Marshal(report)
	root, err := os.UserCacheDir()
	saved := ""
	if err == nil {
		root = filepath.Join(root, "MAC-DOW", "diagnostics")
		if os.MkdirAll(root, 0700) == nil {
			saved = filepath.Join(root, fmt.Sprintf("report-%d.json", time.Now().UnixNano()))
			if os.WriteFile(saved, data, 0600) != nil {
				saved = ""
			}
		}
	}
	result := Result{Saved: saved != ""}
	if !validEndpoint(Webhook) {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	result.Sent = deliver(ctx, client, Webhook, report) == nil
	if result.Sent && saved != "" {
		os.Rename(saved, filepath.Join(root, "last-report.json"))
	}
	if err != nil || root == "" {
		return result
	}
	// Retry up to two queued reports; retain a bounded recent queue while offline.
	pending, _ := filepath.Glob(filepath.Join(root, "report-*.json"))
	sort.Strings(pending)
	for i, path := range pending {
		if i < len(pending)-32 {
			os.Remove(path)
			continue
		}
		if !result.Sent || path == saved || i < len(pending)-2 {
			continue
		}
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Size() > 8192 {
			continue
		}
		b, err := os.ReadFile(path)
		var queued Report
		if err == nil && json.Unmarshal(b, &queued) == nil {
			clean := NewReport(queued.Role, queued.Event, queued.Code, queued.Counts)
			if deliver(ctx, client, Webhook, clean) == nil {
				os.Remove(path)
			}
		}
	}
	return result
}

var queue = make(chan Report, 32)
var once sync.Once

func Emit(role, event, code string, counts map[string]int) {
	if role != "mac" || runtime.GOOS != "darwin" {
		return
	}
	if !validEndpoint(Webhook) {
		return
	} // Unit tests and generic source builds never transmit.
	once.Do(func() {
		go func() {
			for r := range queue {
				Record(r.Role, r.Event, r.Code, r.Counts)
			}
		}()
	})
	select {
	case queue <- NewReport(role, event, code, counts):
	default:
	}
}
