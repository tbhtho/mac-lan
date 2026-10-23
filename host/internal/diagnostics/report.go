package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	"github.com/tbhtho/MAC-DOW/host/internal/updater"
)

// Only the one-time private bootstrap embeds this value. Public update binaries
// read the Mac's private configuration and do not contain its destination.
var Webhook string

var destinationMu sync.Mutex
var destinationInitialized bool
var configuredDestination string

// InitMacDestination preserves the private bootstrap's destination before an
// update can replace it with a public binary. Other platforms never read or
// create this configuration. Errors deliberately contain no paths or endpoint.
func InitMacDestination() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	destinationMu.Lock()
	defer destinationMu.Unlock()
	if destinationInitialized {
		return nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return errors.New("private Mac diagnostic configuration unavailable")
	}
	endpoint, err := initMacDestination(runtime.GOOS, root, Webhook)
	if err != nil {
		return err
	}
	configuredDestination = endpoint
	destinationInitialized = true
	return nil
}

func macDestination() string {
	if InitMacDestination() != nil {
		return ""
	}
	destinationMu.Lock()
	defer destinationMu.Unlock()
	return configuredDestination
}

type destinationConfig struct {
	Webhook string `json:"webhook"`
}

const maxDestinationConfig = 4096

func initMacDestination(platform, root, embedded string) (string, error) {
	if platform != "darwin" {
		return "", nil
	}
	unavailable := errors.New("private Mac diagnostic configuration unavailable")
	if !filepath.IsAbs(root) || (embedded != "" && !validEndpoint(embedded)) {
		return "", unavailable
	}
	directory := filepath.Join(root, "MAC-DOW")
	path := filepath.Join(directory, "diagnostics.json")
	if err := diagnosticDirectory(directory, false); err != nil {
		if !os.IsNotExist(err) {
			return "", unavailable
		}
	} else {
		if err := os.Chmod(directory, 0700); err != nil {
			return "", unavailable
		}
		if endpoint, err := readDestination(path); err == nil {
			// The user's existing private destination remains authoritative even if
			// another private bootstrap has a different embedded destination.
			return endpoint, nil
		} else if !os.IsNotExist(err) {
			return "", unavailable
		}
	}
	if embedded == "" {
		return "", nil
	}
	if err := diagnosticDirectory(directory, true); err != nil {
		return "", unavailable
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return "", unavailable
	}
	data, _ := json.Marshal(destinationConfig{Webhook: embedded})
	file, err := os.CreateTemp(directory, ".diagnostics-*")
	if err != nil {
		return "", unavailable
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return "", unavailable
	}
	// Publishing a completed file with a hard link is atomic and cannot replace
	// a configuration created by another bootstrap at the same time.
	if err := os.Link(temporary, path); err != nil && !os.IsExist(err) {
		return "", unavailable
	}
	endpoint, err := readDestination(path)
	if err != nil {
		return "", unavailable
	}
	return endpoint, nil
}

// Check every existing component, including the user configuration root. The
// private MAC-DOW directory prevents another account from replacing children.
func diagnosticDirectory(path string, create bool) error {
	if !filepath.IsAbs(path) {
		return errors.New("absolute configuration directory required")
	}
	parent := filepath.Dir(path)
	if parent != path {
		if err := diagnosticDirectory(parent, create); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) && create {
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe configuration directory")
	}
	return nil
}

func readDestination(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxDestinationConfig || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return "", errors.New("unsafe diagnostic configuration")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("diagnostic configuration changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDestinationConfig+1))
	if err != nil || len(data) > maxDestinationConfig {
		return "", errors.New("diagnostic configuration too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config destinationConfig
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || !validEndpoint(config.Webhook) {
		return "", errors.New("invalid diagnostic configuration")
	}
	return config.Webhook, nil
}

var codePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,48}$`)
var events = strings.Fields("startup shutdown discovery-empty discovery-ready discovery-failed pairing-start pairing-approved pairing-denied pairing-failed stream-request stream-approved stream-denied stream-started stream-ended control-state control-release capture-failed decode-failed fatal manual-report bar-docked bar-restored bar-failed permissions-ok permissions-missing listener-ready advertise-ready advertise-failed native-build-started native-build-ready native-build-failed menu-build-started menu-build-ready menu-build-failed")
var allowedCounts = map[string]bool{"hosts": true, "monitors": true, "screenPermission": true, "controlPermission": true, "controlActive": true, "width": true, "fps": true, "bitrate": true, "appOnly": true, "paired": true, "streaming": true, "docked": true, "compilerLine": true, "compilerColumn": true, "compilerExit": true}

// ValidControlReleaseReason accepts only product state labels, never client
// messages, input contents, addresses, paths, or credentials.
func ValidControlReleaseReason(reason string) bool {
	switch reason {
	case "foreground-lost", "window-deactivated", "viewer-blur", "pointer-cancel",
		"video-stale", "native-heartbeat-stale", "tab-change", "monitor-change",
		"manual", "disconnected", "mac-permission", "mac-scope", "mac-watchdog",
		"shutdown", "files-open", "updates-open", "keyboard-unavailable",
		"stream-start", "display-change", "mac-paused":
		return true
	}
	return false
}

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
	if event == "control-release" && !ValidControlReleaseReason(code) {
		code = "unknown"
	}
	safe := map[string]int{}
	for key, value := range counts {
		if allowedCounts[key] && value >= 0 && value <= 100000 {
			safe[key] = value
		}
	}
	return Report{"mac-lan", updater.BuildConfig().Version, runtime.GOOS, runtime.GOARCH, role, event, code, time.Now().UTC().Format(time.RFC3339), safe}
}
func validEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 2048 || u.Scheme != "https" || u.Host != "discord.com" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	return regexp.MustCompile(`^/api/webhooks/[0-9]{17,22}/[A-Za-z0-9_-]+$`).MatchString(u.Path)
}
func deliver(ctx context.Context, client *http.Client, endpoint string, report Report) error {
	if !validEndpoint(endpoint) {
		return errors.New("diagnostic destination unavailable")
	}
	body, _ := json.Marshal(report)
	payload, _ := json.Marshal(map[string]any{"content": "mac-lan diagnostic\n```json\n" + string(body) + "\n```", "allowed_mentions": map[string]any{"parse": []string{}}})
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
	endpoint := macDestination()
	if !validEndpoint(endpoint) {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	result.Sent = deliver(ctx, client, endpoint, report) == nil
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
			if deliver(ctx, client, endpoint, clean) == nil {
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
	if !validEndpoint(macDestination()) {
		return
	} // Generic source builds without private Mac configuration never transmit.
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
