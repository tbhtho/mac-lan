package updater

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type feedTransport func(*http.Request) (*http.Response, error)

func (f feedTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testFeed(t *testing.T) (*SignedFeed, signedFeedPayload, ed25519.PrivateKey, []byte) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 18, 0, 0, 0, time.UTC)
	data := []byte("signed test package")
	sum := sha256.Sum256(data)
	payload := signedFeedPayload{Schema: 1, Product: "MAC-DOW", RepositoryID: 1430459188, PublishedAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour), Releases: []feedRelease{{ID: 100, Tag: "v0.2.0", Commit: strings.Repeat("a", 40), Assets: []feedAsset{{ID: 101, Name: "MAC-DOW.exe", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}}}}}
	return newSignedFeed(&http.Client{}, publicFeedBase, pub, func() time.Time { return now }), payload, key, data
}

func signFeed(t *testing.T, p signedFeedPayload, key ed25519.PrivateKey) []byte {
	t.Helper()
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(signedFeedEnvelope{Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func response(body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}

func TestSignedFeedAuthenticatesExactBytes(t *testing.T) {
	s, p, key, _ := testFeed(t)
	data := signFeed(t, p, key)
	if releases, err := s.authenticate(data); err != nil || len(releases) != 1 {
		t.Fatal(releases, err)
	}
	var envelope signedFeedEnvelope
	json.Unmarshal(data, &envelope)
	payload, _ := base64.StdEncoding.DecodeString(envelope.Payload)
	envelope.Payload = base64.StdEncoding.EncodeToString(bytes.Replace(payload, []byte("0.2.0"), []byte("9.9.9"), 1))
	tampered, _ := json.Marshal(envelope)
	if _, err := s.authenticate(tampered); !errors.Is(err, ErrSignedFeed) {
		t.Fatal("tampered feed accepted", err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := s.authenticate(signFeed(t, p, other)); !errors.Is(err, ErrSignedFeed) {
		t.Fatal("wrong signer accepted", err)
	}
	if _, err := s.authenticate(append(data, []byte(" {}")...)); err == nil {
		t.Fatal("trailing data accepted")
	}
	if _, err := s.authenticate(bytes.Repeat([]byte{'x'}, maxSignedFeed+1)); err == nil {
		t.Fatal("oversized envelope accepted")
	}
}

func TestSignedFeedRejectsInvalidPublisherMetadata(t *testing.T) {
	for name, mutate := range map[string]func(*signedFeedPayload){
		"product":            func(p *signedFeedPayload) { p.Product = "other" },
		"repository":         func(p *signedFeedPayload) { p.RepositoryID++ },
		"schema":             func(p *signedFeedPayload) { p.Schema++ },
		"expired":            func(p *signedFeedPayload) { p.ExpiresAt = p.PublishedAt },
		"far expiry":         func(p *signedFeedPayload) { p.ExpiresAt = p.PublishedAt.Add(32 * 24 * time.Hour) },
		"future":             func(p *signedFeedPayload) { p.PublishedAt = p.PublishedAt.Add(2 * time.Hour) },
		"commit":             func(p *signedFeedPayload) { p.Releases[0].Commit = "main" },
		"tag":                func(p *signedFeedPayload) { p.Releases[0].Tag = "v0.2.0/../../oops" },
		"prerelease":         func(p *signedFeedPayload) { p.Releases[0].Prerelease = true },
		"asset path":         func(p *signedFeedPayload) { p.Releases[0].Assets[0].Name = "../MAC-DOW.exe" },
		"asset architecture": func(p *signedFeedPayload) { p.Releases[0].Assets[0].Name = "MAC-DOW-windows-arm64.exe" },
		"asset zero size":    func(p *signedFeedPayload) { p.Releases[0].Assets[0].Size = 0 },
		"asset huge":         func(p *signedFeedPayload) { p.Releases[0].Assets[0].Size = MaxArchive + 1 },
		"digest":             func(p *signedFeedPayload) { p.Releases[0].Assets[0].SHA256 = "SHA256:bad" },
		"duplicate release":  func(p *signedFeedPayload) { p.Releases = append(p.Releases, p.Releases[0]) },
		"duplicate asset": func(p *signedFeedPayload) {
			p.Releases[0].Assets = append(p.Releases[0].Assets, p.Releases[0].Assets[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, p, key, _ := testFeed(t)
			mutate(&p)
			if _, err := s.authenticate(signFeed(t, p, key)); err == nil {
				t.Fatal("invalid signed metadata accepted")
			}
		})
	}
}

func TestSignedFeedDownloadAndReverification(t *testing.T) {
	s, p, key, data := testFeed(t)
	feed := signFeed(t, p, key)
	feeds, downloads := 0, 0
	s.client.Transport = feedTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("authenticated public request")
		}
		if r.URL.String() == publicFeedBase+"feed.json" {
			feeds++
			return response(feed), nil
		}
		if r.URL.String() == publicDownloadBase+"v0.2.0/MAC-DOW.exe" {
			downloads++
			return response(data), nil
		}
		t.Fatal("unexpected URL", r.URL)
		return nil, errors.New("unexpected URL")
	})
	e := Engine{Current: "0.1.0", OS: "windows", Arch: "amd64", Source: s, Verify: s.Verify}
	plan, err := e.Check(context.Background(), "stable")
	if err != nil || plan.Commit != p.Releases[0].Commit {
		t.Fatal(plan, err)
	}
	path := filepath.Join(t.TempDir(), "MAC-DOW.exe")
	if err = s.Download(context.Background(), plan, path); err != nil {
		t.Fatal(err)
	}
	if err = s.Verify(context.Background(), plan.Tag, path); err != nil {
		t.Fatal(err)
	}
	if downloads != 1 || feeds != 2 {
		t.Fatal("metadata was not freshly authenticated", feeds, downloads)
	}
	if err = os.WriteFile(path, []byte("tampered test package"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Verify(context.Background(), plan.Tag, path); err == nil {
		t.Fatal("changed staged package accepted")
	}
	feed[len(feed)/2] ^= 1
	if err = s.Verify(context.Background(), plan.Tag, path); err == nil {
		t.Fatal("changed publisher feed accepted")
	}
}

func TestSignedFeedRejectsModifiedPlanAndPartialDownload(t *testing.T) {
	s, p, key, data := testFeed(t)
	s.client.Transport = feedTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/feed.json") {
			return response(signFeed(t, p, key)), nil
		}
		resp := response(data[:len(data)-1])
		resp.ContentLength = -1
		return resp, nil
	})
	e := Engine{Current: "0.1.0", OS: "windows", Arch: "amd64", Source: s}
	plan, err := e.Check(context.Background(), "stable")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Plan){func(p *Plan) { p.RepositoryID++ }, func(p *Plan) { p.ReleaseID++ }, func(p *Plan) { p.Asset.ID++ }, func(p *Plan) { p.Asset.Digest = "sha256:" + strings.Repeat("0", 64) }, func(p *Plan) { p.Commit = strings.Repeat("f", 40) }, func(p *Plan) { p.OS = "darwin" }, func(p *Plan) { p.Arch = "arm64" }} {
		changed := plan
		mutate(&changed)
		if err = s.Download(context.Background(), changed, filepath.Join(t.TempDir(), "download")); err == nil {
			t.Fatal("modified plan accepted", changed)
		}
	}
	path := filepath.Join(t.TempDir(), "download")
	if err = s.Download(context.Background(), plan, path); err == nil {
		t.Fatal("partial download accepted")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("partial download left behind", err)
	}
}

func TestSignedFeedRedirectPolicy(t *testing.T) {
	for name, destination := range map[string]string{"http": "http://release-assets.githubusercontent.com/x", "foreign": "https://example.com/package", "other repository": "https://github.com/evil/repo/releases/download/v0.2.0/MAC-DOW.exe", "userinfo": "https://user@release-assets.githubusercontent.com/x", "port": "https://release-assets.githubusercontent.com:8443/x"} {
		t.Run(name, func(t *testing.T) {
			s, p, key, _ := testFeed(t)
			s.client.Transport = feedTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/feed.json") {
					return response(signFeed(t, p, key)), nil
				}
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{destination}}, Body: io.NopCloser(strings.NewReader("")), ContentLength: 0}, nil
			})
			e := Engine{Current: "0.1.0", OS: "windows", Arch: "amd64", Source: s}
			plan, err := e.Check(context.Background(), "stable")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Download(context.Background(), plan, filepath.Join(t.TempDir(), "download")); err == nil {
				t.Fatal("unsafe redirect accepted")
			}
		})
	}
	s, p, key, data := testFeed(t)
	s.client.Transport = feedTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/feed.json") {
			return response(signFeed(t, p, key)), nil
		}
		if r.URL.Host == "github.com" {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://release-assets.githubusercontent.com/signed-package?token=temporary"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return response(data), nil
	})
	e := Engine{Current: "0.1.0", OS: "windows", Arch: "amd64", Source: s}
	plan, err := e.Check(context.Background(), "stable")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Download(context.Background(), plan, filepath.Join(t.TempDir(), "download")); err != nil {
		t.Fatal("valid GitHub asset redirect refused", err)
	}
}

func TestProductionFeedTrustAnchorsCannotBeRedirected(t *testing.T) {
	s, _, _, _ := testFeed(t)
	config := Config{UpdateFeedBase: publicFeedBase, UpdatePublicKey: base64.StdEncoding.EncodeToString(s.key)}
	if _, err := NewSignedFeed(config); err != nil {
		t.Fatal(err)
	}
	config.UpdateFeedBase = "https://example.com/updates/"
	if _, err := NewSignedFeed(config); err == nil {
		t.Fatal("foreign feed accepted")
	}
	config.UpdateFeedBase = publicFeedBase
	config.UpdatePublicKey = "bad"
	if _, err := NewSignedFeed(config); err == nil {
		t.Fatal("invalid key accepted")
	}
}
func TestProductionFeedUsesRenamedSourceRepository(t *testing.T) {
	s, _, _, _ := testFeed(t)
	config := Config{UpdateFeedBase: "https://raw.githubusercontent.com/tbhtho/mac-lan/main/updates/", UpdatePublicKey: base64.StdEncoding.EncodeToString(s.key)}
	if _, err := NewSignedFeed(config); err != nil {
		t.Fatal("renamed source repository feed refused", err)
	}
	for _, old := range []string{"https://raw.githubusercontent.com/tbhtho/MAC-DOW/main/updates/", "https://raw.githubusercontent.com/tbhtho/MAC-DOW-Downloads/main/updates/"} {
		config.UpdateFeedBase = old
		if _, err := NewSignedFeed(config); err == nil {
			t.Fatal("withdrawn feed still accepted", old)
		}
	}
}

func TestSignedFeedDownloadsRenamedPlatformAssets(t *testing.T) {
	for _, platform := range []struct{ os, arch, name string }{
		{"windows", "amd64", "mac-lan.exe"},
		{"darwin", "arm64", "mac-lan-macos-arm64-commandline-preview.tar.gz"},
	} {
		t.Run(platform.os, func(t *testing.T) {
			s, p, key, data := testFeed(t)
			p.Releases[0].Assets[0].Name = platform.name
			s.client.Transport = feedTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == publicFeedBase+"feed.json" {
					return response(signFeed(t, p, key)), nil
				}
				if r.URL.String() != "https://github.com/tbhtho/mac-lan/releases/download/v0.2.0/"+platform.name {
					t.Fatalf("unexpected download URL %s", r.URL)
				}
				return response(data), nil
			})
			e := Engine{Current: "0.1.0", OS: platform.os, Arch: platform.arch, Source: s}
			plan, err := e.Check(context.Background(), "stable")
			if err != nil || plan.Asset.Name != platform.name {
				t.Fatal(plan, err)
			}
			path := filepath.Join(t.TempDir(), platform.name)
			if err := s.Download(context.Background(), plan, path); err != nil {
				t.Fatal(err)
			}
			if err := s.Verify(context.Background(), plan.Tag, path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSignedFeedEngineStagesRevalidatesAndApplies(t *testing.T) {
	s, p, key, _ := testFeed(t)
	commit := p.Releases[0].Commit
	data := standaloneFixture(t, "0.2.0", commit)
	sum := sha256.Sum256(data)
	p.Releases[0].Assets[0].Size = int64(len(data))
	p.Releases[0].Assets[0].SHA256 = hex.EncodeToString(sum[:])
	feed := signFeed(t, p, key)
	s.client.Transport = feedTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/feed.json") {
			return response(feed), nil
		}
		return response(data), nil
	})
	root := t.TempDir()
	install := filepath.Join(root, "MAC-DOW.exe")
	old := standaloneFixture(t, "0.1.0", commit)
	if err := os.WriteFile(install, old, 0600); err != nil {
		t.Fatal(err)
	}
	e := Engine{Current: "0.1.0", OS: "windows", Arch: "amd64", Root: filepath.Join(root, "updates"), Source: s, Verify: s.Verify}
	stage, err := e.Stage(context.Background(), "stable")
	if err != nil || !stage.CanApply {
		t.Fatal(stage, err)
	}
	feed[len(feed)/2] ^= 1
	if _, err = e.Apply(context.Background(), stage.Directory, install, stage.Plan.confirmation(), false); err == nil {
		t.Fatal("apply accepted a changed signed feed")
	}
	if got, _ := os.ReadFile(install); !bytes.Equal(got, old) {
		t.Fatal("failed verification changed installed app")
	}
	feed = signFeed(t, p, key)
	if _, err = e.Apply(context.Background(), stage.Directory, install, stage.Plan.confirmation(), false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(install); !bytes.Equal(got, data) {
		t.Fatal("verified signed update was not installed")
	}
}

func TestSignedFeedPlatformSelectionUsesActualMacPackage(t *testing.T) {
	s, p, key, _ := testFeed(t)
	a := p.Releases[0].Assets[0]
	a.ID++
	a.Name = "MAC-DOW-macos-arm64-commandline-preview.tar.gz"
	p.Releases[0].Assets = []feedAsset{a}
	s.client.Transport = feedTransport(func(*http.Request) (*http.Response, error) { return response(signFeed(t, p, key)), nil })
	e := Engine{Current: "0.1.0", OS: "darwin", Arch: "arm64", Source: s}
	plan, err := e.Check(context.Background(), "stable")
	if err != nil || plan.Asset.Name != a.Name {
		t.Fatal(plan, err)
	}
	e.OS, e.Arch = "windows", "amd64"
	if _, err = e.Check(context.Background(), "stable"); !errors.Is(err, ErrNoUpdate) {
		t.Fatal("Mac-only release selected for Windows", err)
	}
}
