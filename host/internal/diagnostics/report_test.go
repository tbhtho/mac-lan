package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestReportsExcludeArbitraryMessagesAndCredentials(t *testing.T) {
	r := NewReport("windows", "pairing-failed", "IOException", map[string]int{"hosts": 0, "token": 123, "clipboard": 99})
	b, _ := json.Marshal(r)
	for _, secret := range []string{"token", "clipboard", "fingerprint", "webhook", "username"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("unexpected field", secret)
		}
	}
	invalid := NewReport("windows", "paste token=abc https://discord.com/api/webhooks/secret", "secret value", nil)
	b, _ = json.Marshal(invalid)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "token=") {
		t.Fatal(string(b))
	}
}
func TestDeliveryRetriesAndDisablesMentions(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(body), "allowed_mentions") || !strings.Contains(string(body), `"parse":[]`) {
			t.Fatal("mentions not disabled")
		}
		if req.URL.Query().Get("wait") != "true" {
			t.Fatal("delivery must wait for server acknowledgement")
		}
		if calls == 1 {
			return nil, errors.New("offline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"123"}`)), Header: make(http.Header)}, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := deliver(ctx, client, "https://discord.com/api/webhooks/123456789012345678/abc", NewReport("windows", "startup", "ok", nil)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestEndpointMustBeDiscordWebhook(t *testing.T) {
	for _, url := range []string{"http://discord.com/api/webhooks/123/a", "https://example.com/api/webhooks/123/a", "https://discord.com.evil.test/api/webhooks/123/a", "https://discord.com/api/webhooks/123/a?x=secret", "https://discord.com/api/webhooks/123456789012345678/fixture?", "https://discord.com/api/webhooks/123456789012345678/%66ixture"} {
		if validEndpoint(url) {
			t.Fatal("accepted", url)
		}
	}
}

const fixtureDestination = "https://discord.com/api/webhooks/123456789012345678/fixture"

func privateConfigRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPrivateBootstrapPersistsDestinationForPublicUpdates(t *testing.T) {
	root := privateConfigRoot(t)
	endpoint, err := initMacDestination("darwin", root, fixtureDestination)
	if err != nil || endpoint != fixtureDestination {
		t.Fatalf("bootstrap could not preserve its destination: %v", err)
	}
	endpoint, err = initMacDestination("darwin", root, "")
	if err != nil || endpoint != fixtureDestination {
		t.Fatalf("public update could not load private destination: %v", err)
	}
	other := "https://discord.com/api/webhooks/123456789012345678/different-fixture"
	endpoint, err = initMacDestination("darwin", root, other)
	if err != nil || endpoint != fixtureDestination {
		t.Fatalf("bootstrap replaced the existing destination: %v", err)
	}
	directory := filepath.Join(root, "MAC-DOW")
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 1 || files[0].Name() != "diagnostics.json" {
		t.Fatalf("unexpected configuration files after migration: %v", err)
	}
	if runtime.GOOS != "windows" {
		for _, fixture := range []struct {
			path string
			mode os.FileMode
		}{{directory, 0700}, {filepath.Join(directory, "diagnostics.json"), 0600}} {
			info, err := os.Stat(fixture.path)
			if err != nil || info.Mode().Perm() != fixture.mode {
				t.Fatal("configuration permissions are not private")
			}
		}
	}
}

func TestPublicBuildWithoutDestinationCreatesNothing(t *testing.T) {
	root := privateConfigRoot(t)
	endpoint, err := initMacDestination("darwin", root, "")
	if err != nil || endpoint != "" {
		t.Fatalf("public build should remain unconfigured: %v", err)
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatal("public build created a destination without a private bootstrap")
	}
}

func TestConcurrentBootstrapsPublishOneCompleteConfiguration(t *testing.T) {
	root := privateConfigRoot(t)
	type result struct {
		endpoint string
		err      error
	}
	results := make(chan result, 2)
	other := "https://discord.com/api/webhooks/123456789012345678/different-fixture"
	for _, embedded := range []string{fixtureDestination, other} {
		go func() {
			endpoint, err := initMacDestination("darwin", root, embedded)
			results <- result{endpoint, err}
		}()
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.endpoint != second.endpoint || (first.endpoint != fixtureDestination && first.endpoint != other) {
		t.Fatal("concurrent bootstraps did not preserve one complete configuration")
	}
}

func TestWindowsDoesNotReadOrCreateMacDestination(t *testing.T) {
	root := privateConfigRoot(t)
	endpoint, err := initMacDestination("windows", root, fixtureDestination)
	if err != nil || endpoint != "" {
		t.Fatal("Windows accepted a Mac diagnostic destination")
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatal("Windows created Mac diagnostic configuration")
	}
}

func TestDestinationRejectsMalformedOrUnsafeExistingConfiguration(t *testing.T) {
	for name, contents := range map[string]string{
		"external endpoint": `{"webhook":"https://example.test/collect"}`,
		"unknown property":  `{"webhook":"` + fixtureDestination + `","extra":true}`,
		"trailing document": `{"webhook":"` + fixtureDestination + `"} {}`,
		"missing value":     `{}`,
		"oversize":          strings.Repeat(" ", maxDestinationConfig+1),
	} {
		t.Run(name, func(t *testing.T) {
			root := privateConfigRoot(t)
			directory := filepath.Join(root, "MAC-DOW")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "diagnostics.json")
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			endpoint, err := initMacDestination("darwin", root, fixtureDestination)
			if err == nil || endpoint != "" || strings.Contains(err.Error(), fixtureDestination) || strings.Contains(err.Error(), root) {
				t.Fatal("unsafe configuration was accepted or its details disclosed")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != contents {
				t.Fatal("existing configuration was overwritten")
			}
		})
	}
}

func TestDestinationRejectsSymlinkComponentsAndFiles(t *testing.T) {
	for _, location := range []string{"root", "directory", "file"} {
		t.Run(location, func(t *testing.T) {
			base := privateConfigRoot(t)
			root, target := filepath.Join(base, "config"), filepath.Join(base, "elsewhere")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "MAC-DOW")
			if location == "root" {
				link = filepath.Join(base, "linked-config")
				root = link
			} else if location == "file" {
				if err := os.Mkdir(link, 0700); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(link, "diagnostics.json")
				fileTarget := filepath.Join(target, "destination.json")
				data, _ := json.Marshal(destinationConfig{Webhook: fixtureDestination})
				if err := os.WriteFile(fileTarget, data, 0600); err != nil {
					t.Fatal(err)
				}
				target = fileTarget
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skip("symlinks unavailable on this test account")
			}
			if endpoint, err := initMacDestination("darwin", root, fixtureDestination); err == nil || endpoint != "" {
				t.Fatal("symlink destination accepted")
			}
		})
	}
}

func TestDestinationRejectsRelativeRootAndEmbeddedOverrides(t *testing.T) {
	if endpoint, err := initMacDestination("darwin", "relative", fixtureDestination); err == nil || endpoint != "" {
		t.Fatal("relative configuration root accepted")
	}
	if endpoint, err := initMacDestination("darwin", privateConfigRoot(t), "https://example.test/collect"); err == nil || endpoint != "" {
		t.Fatal("arbitrary embedded destination accepted")
	}
}

func TestDestinationRejectsReadablePrivateConfiguration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode permissions are enforced on the Mac")
	}
	root := privateConfigRoot(t)
	if _, err := initMacDestination("darwin", root, fixtureDestination); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "MAC-DOW", "diagnostics.json")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if endpoint, err := initMacDestination("darwin", root, ""); err == nil || endpoint != "" {
		t.Fatal("public-readable diagnostic destination accepted")
	}
}
func TestWindowsReportsNeverUpload(t *testing.T) {
	oldHook, oldTransport := Webhook, http.DefaultTransport
	defer func() { Webhook = oldHook; http.DefaultTransport = oldTransport }()
	Webhook = "https://discord.com/api/webhooks/123456789012345678/fixture"
	http.DefaultTransport = transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("Windows attempted remote diagnostics")
		return nil, errors.New("blocked")
	})
	if result := Record("windows", "startup", "ok", nil); result.Sent || result.Saved {
		t.Fatal("Windows must not enter Mac remote-report pipeline")
	}
}

func TestCompilerReportAllowsOnlyBoundedNumericLocations(t *testing.T) {
	r := NewReport("mac", "native-build-failed", "compile-type", map[string]int{"compilerLine": 167, "compilerColumn": 18, "compilerExit": 1, "compilerOutput": 99, "sourcePath": 3, "token": 1})
	if len(r.Counts) != 3 || r.Counts["compilerLine"] != 167 || r.Counts["compilerColumn"] != 18 || r.Counts["compilerExit"] != 1 {
		t.Fatalf("unexpected compiler fields: %+v", r.Counts)
	}
	invalid := NewReport("mac", "native-build-failed", "compile-failed", map[string]int{"compilerLine": 100001, "compilerColumn": -1, "compilerExit": 100001})
	if len(invalid.Counts) != 0 {
		t.Fatalf("unbounded compiler counters: %+v", invalid.Counts)
	}
}

func TestControlReportRetainsOnlyPermissionAndActiveCounts(t *testing.T) {
	r := NewReport("mac", "control-state", "accessibility-missing", map[string]int{"controlPermission": 0, "controlActive": 0, "keyCode": 55, "pointerX": 99, "token": 1})
	if r.Event != "control-state" || len(r.Counts) != 2 || r.Counts["controlPermission"] != 0 || r.Counts["controlActive"] != 0 {
		t.Fatalf("unexpected control diagnostic: %+v", r)
	}
}
