package updater

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	publicFeedBase     = "https://raw.githubusercontent.com/tbhtho/MAC-DOW-Downloads/main/updates/"
	publicDownloadBase = "https://github.com/tbhtho/MAC-DOW-Downloads/releases/download/"
	maxSignedFeed      = 2 << 20
)

var (
	ErrSignedFeed = errors.New("automatic update metadata could not be authenticated")
	feedSHA       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	feedCommit    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// SignedFeed authenticates publisher metadata with the public key compiled into
// the app. Downloads are public binaries; no GitHub account or CLI is needed.
type SignedFeed struct {
	client   *http.Client
	key      ed25519.PublicKey
	base     string
	now      func() time.Time
	mu       sync.RWMutex
	releases []feedRelease
}

type signedFeedEnvelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type signedFeedPayload struct {
	Schema       int           `json:"schema"`
	Product      string        `json:"product"`
	RepositoryID int64         `json:"sourceRepositoryId"`
	PublishedAt  time.Time     `json:"publishedAt"`
	ExpiresAt    time.Time     `json:"expiresAt"`
	Releases     []feedRelease `json:"releases"`
}
type feedRelease struct {
	ID         int64       `json:"id"`
	Tag        string      `json:"tag"`
	Commit     string      `json:"commit"`
	Prerelease bool        `json:"prerelease"`
	Assets     []feedAsset `json:"assets"`
}
type feedAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// NewSignedFeed accepts only the compiled distribution location and key. There
// is deliberately no environment variable, command flag, or persisted setting
// that can replace either production trust anchor.
func NewSignedFeed(c Config) (*SignedFeed, error) {
	if c.UpdateFeedBase != publicFeedBase {
		return nil, errors.New("automatic updates are not configured for this build")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(c.UpdatePublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("automatic update signing key is not configured")
	}
	return newSignedFeed(&http.Client{Timeout: 10 * time.Minute}, c.UpdateFeedBase, key, time.Now), nil
}

func newSignedFeed(client *http.Client, base string, key ed25519.PublicKey, now func() time.Time) *SignedFeed {
	copyClient := *client
	return &SignedFeed{client: &copyClient, base: base, key: append(ed25519.PublicKey(nil), key...), now: now}
}

func decodeFeedJSON(data []byte, dest any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dest); err != nil {
		return ErrSignedFeed
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrSignedFeed
	}
	return nil
}

func knownFeedAsset(name string) bool {
	switch name {
	case "MAC-DOW.exe", "MAC-DOW-windows-x64-unsigned.zip", "MAC-DOW-macos-arm64-unsigned.tar.gz", "MAC-DOW-macos-arm64-commandline-preview.tar.gz":
		return true
	}
	return false
}

func (s *SignedFeed) authenticate(data []byte) ([]feedRelease, error) {
	if len(data) > maxSignedFeed || len(s.key) != ed25519.PublicKeySize {
		return nil, ErrSignedFeed
	}
	var envelope signedFeedEnvelope
	if err := decodeFeedJSON(data, &envelope); err != nil {
		return nil, err
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(envelope.Payload)
	if err != nil || len(payload) > maxSignedFeed {
		return nil, ErrSignedFeed
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(s.key, payload, signature) {
		return nil, ErrSignedFeed
	}
	// Parse metadata only after its exact bytes have been authenticated.
	var p signedFeedPayload
	if err := decodeFeedJSON(payload, &p); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if p.Schema != 1 || p.Product != "MAC-DOW" || p.RepositoryID != 1430459188 || p.PublishedAt.IsZero() || p.PublishedAt.After(now.Add(5*time.Minute)) || !p.ExpiresAt.After(now) || !p.ExpiresAt.After(p.PublishedAt) || p.ExpiresAt.Sub(p.PublishedAt) > 31*24*time.Hour || len(p.Releases) == 0 || len(p.Releases) > 128 {
		return nil, ErrSignedFeed
	}
	releaseIDs, tags, assetIDs := map[int64]bool{}, map[string]bool{}, map[int64]bool{}
	for _, r := range p.Releases {
		if r.ID <= 0 || releaseIDs[r.ID] || tags[r.Tag] || !strings.HasPrefix(r.Tag, "v") || !feedCommit.MatchString(r.Commit) || len(r.Assets) == 0 || len(r.Assets) > 8 {
			return nil, ErrSignedFeed
		}
		v, err := ParseVersion(strings.TrimPrefix(r.Tag, "v"))
		if err != nil || r.Prerelease != (len(v.Pre) > 0) {
			return nil, ErrSignedFeed
		}
		releaseIDs[r.ID], tags[r.Tag] = true, true
		names := map[string]bool{}
		for _, a := range r.Assets {
			if a.ID <= 0 || assetIDs[a.ID] || names[a.Name] || !knownFeedAsset(a.Name) || a.Size <= 0 || a.Size > MaxArchive || !feedSHA.MatchString(a.SHA256) {
				return nil, ErrSignedFeed
			}
			assetIDs[a.ID], names[a.Name] = true, true
		}
	}
	return p.Releases, nil
}

func safeHTTPS(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && u.User == nil && u.Fragment == "" && (u.Port() == "" || u.Port() == "443") && len(u.RawQuery) <= 16384
}

func (s *SignedFeed) get(ctx context.Context, raw string, download bool) (*http.Response, error) {
	u, err := url.Parse(raw)
	if err != nil || !safeHTTPS(u) {
		return nil, ErrSignedFeed
	}
	client := *s.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 || !safeHTTPS(req.URL) {
			return ErrSignedFeed
		}
		if !download {
			if req.URL.String() != raw {
				return ErrSignedFeed
			}
			return nil
		}
		switch req.URL.Hostname() {
		case "github.com":
			if req.URL.String() != raw {
				return ErrSignedFeed
			}
		case "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		default:
			return ErrSignedFeed
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "MAC-DOW-Updater")
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("automatic update download is unavailable")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, errors.New("automatic update download is unavailable")
	}
	return resp, nil
}

func (s *SignedFeed) Releases(ctx context.Context) ([]Release, error) {
	resp, err := s.get(ctx, s.base+"feed.json", false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > maxSignedFeed {
		return nil, ErrSignedFeed
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSignedFeed+1))
	if err != nil {
		return nil, err
	}
	releases, err := s.authenticate(data)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.releases = releases
	s.mu.Unlock()
	result := make([]Release, 0, len(releases))
	for _, r := range releases {
		x := Release{ID: r.ID, Tag: r.Tag, Prerelease: r.Prerelease}
		for _, a := range r.Assets {
			x.Assets = append(x.Assets, Asset{ID: a.ID, Name: a.Name, Size: a.Size, Digest: "sha256:" + a.SHA256})
		}
		result = append(result, x)
	}
	return result, nil
}

func (s *SignedFeed) release(tag string) (feedRelease, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.releases {
		if r.Tag == tag {
			return r, true
		}
	}
	return feedRelease{}, false
}

func (s *SignedFeed) Commit(_ context.Context, tag string) (string, error) {
	if r, ok := s.release(tag); ok {
		return r.Commit, nil
	}
	return "", ErrSignedFeed
}

func (s *SignedFeed) Download(ctx context.Context, p Plan, path string) error {
	r, ok := s.release(p.Tag)
	if !ok || p.RepositoryID != 1430459188 || p.ReleaseID != r.ID || p.Commit != r.Commit {
		return ErrSignedFeed
	}
	name, err := assetName(p.OS, p.Arch)
	if err != nil {
		return err
	}
	if p.OS == "windows" && p.Asset.Name == "MAC-DOW.exe" {
		name = p.Asset.Name
	}
	if p.OS == "darwin" && p.Asset.Name == "MAC-DOW-macos-arm64-commandline-preview.tar.gz" {
		name = p.Asset.Name
	}
	if p.Asset.Name != name {
		return ErrSignedFeed
	}
	matched := false
	for _, a := range r.Assets {
		if reflect.DeepEqual(p.Asset, Asset{ID: a.ID, Name: a.Name, Size: a.Size, Digest: "sha256:" + a.SHA256}) {
			matched = true
			break
		}
	}
	if !matched {
		return ErrSignedFeed
	}
	resp, err := s.get(ctx, publicDownloadBase+r.Tag+"/"+p.Asset.Name, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.ContentLength >= 0 && resp.ContentLength != p.Asset.Size {
		return errors.New("update download length mismatch")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	okDownload := false
	defer func() {
		f.Close()
		if !okDownload {
			_ = os.Remove(path)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(resp.Body, p.Asset.Size+1))
	if err != nil {
		return err
	}
	if n != p.Asset.Size {
		return errors.New("update download length mismatch")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	okDownload = true
	return nil
}

// Verify refetches authenticated publisher metadata and hashes the file again.
// Neither staged JSON nor a previous successful check grants apply permission.
func (s *SignedFeed) Verify(ctx context.Context, tag, path string) error {
	if _, err := s.Releases(ctx); err != nil {
		return err
	}
	r, ok := s.release(tag)
	if !ok {
		return ErrSignedFeed
	}
	for _, a := range r.Assets {
		if a.Name != filepath.Base(path) {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		err = CheckDigest(f, a.Size, "sha256:"+a.SHA256)
		f.Close()
		return err
	}
	return fmt.Errorf("%w: package is not in the signed release", ErrSignedFeed)
}
