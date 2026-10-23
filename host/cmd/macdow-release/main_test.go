package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPayload(t *testing.T) []byte {
	t.Helper()
	p := feedPayload{Schema: 1, Product: "MAC-DOW", SourceRepositoryID: 1430459188,
		PublishedAt: "2026-10-23T12:00:00Z", ExpiresAt: "2026-11-22T12:00:00Z",
		Releases: []feedRelease{{ID: 1, Tag: "v0.1.0-alpha.12", Commit: strings.Repeat("a", 40), Prerelease: true,
			Assets: []feedAsset{{ID: 2, Name: "MAC-DOW.exe", Size: 4, SHA256: strings.Repeat("b", 64)}}}}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestKeygenExclusiveAndSignExactBytes(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "private", "updates.ed25519")
	var out bytes.Buffer
	if err := generateKey(keyPath, &out); err != nil {
		t.Fatal(err)
	}
	public, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(out.String()))
	if err != nil || len(public) != ed25519.PublicKeySize {
		t.Fatalf("invalid public key: %v", err)
	}
	original, _ := os.ReadFile(keyPath)
	if err := generateKey(keyPath, &out); err == nil {
		t.Fatal("overwrote private key")
	}
	after, _ := os.ReadFile(keyPath)
	if !bytes.Equal(original, after) {
		t.Fatal("private key changed")
	}
	// Whitespace belongs to the signature, too: the signer must never reserialize.
	payload := append([]byte(" \n"), testPayload(t)...)
	payload = append(payload, '\n')
	envelope, err := signPayload(keyPath, payload, base64.StdEncoding.EncodeToString(public), time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := verifyEnvelope(envelope, base64.StdEncoding.EncodeToString(public), time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	if err != nil || !bytes.Equal(decoded, payload) {
		t.Fatalf("payload bytes changed: %v", err)
	}
	var e feedEnvelope
	json.Unmarshal(envelope, &e)
	raw, _ := base64.StdEncoding.DecodeString(e.Payload)
	raw[0] = '\t'
	e.Payload = base64.StdEncoding.EncodeToString(raw)
	tampered, _ := json.Marshal(e)
	if _, err := verifyEnvelope(tampered, base64.StdEncoding.EncodeToString(public), time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("accepted tampered feed")
	}
}

func TestSigningRejectsMismatchedConfiguredKey(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "key")
	if err := generateKey(keyPath, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	wrong, _, _ := ed25519.GenerateKey(nil)
	if _, err := signPayload(keyPath, testPayload(t), base64.StdEncoding.EncodeToString(wrong), time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("signed with a key that differs from build config")
	}
}

func TestPayloadRejectsInvalidBindings(t *testing.T) {
	valid := string(testPayload(t))
	for name, changed := range map[string]string{
		"product":             strings.Replace(valid, `"MAC-DOW"`, `"other"`, 1),
		"source":              strings.Replace(valid, `1430459188`, `123`, 1),
		"asset":               strings.Replace(valid, `MAC-DOW.exe`, `anything.exe`, 1),
		"sha":                 strings.Replace(valid, strings.Repeat("b", 64), strings.Repeat("B", 64), 1),
		"commit":              strings.Replace(valid, strings.Repeat("a", 40), strings.Repeat("a", 39), 1),
		"long validity":       strings.Replace(valid, `2026-11-22`, `2026-11-24`, 1),
		"prerelease mismatch": strings.Replace(valid, `"prerelease":true`, `"prerelease":false`, 1),
		"unknown field":       strings.Replace(valid, `"schema":1`, `"schema":1,"url":"https://example.com"`, 1),
		"trailing value":      valid + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validatePayload([]byte(changed), time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)); err == nil {
				t.Fatal("accepted malformed manifest")
			}
		})
	}
}

func TestManifestUsesArtifactBytesAndVerifiesPrevious(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	var keyOutput bytes.Buffer
	if err := generateKey(key, &keyOutput); err != nil {
		t.Fatal(err)
	}
	public := strings.TrimSpace(keyOutput.String())
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	windows := filepath.Join(dir, "MAC-DOW.exe")
	mac := filepath.Join(dir, "MAC-DOW-macos-arm64-commandline-preview.tar.gz")
	os.WriteFile(windows, []byte("win"), 0600)
	os.WriteFile(mac, macArchiveFixture(t, []byte("mac"), []byte(`{"publicUpdate":true}`)), 0600)
	options := manifestOptions{Windows: windows, Mac: mac, Tag: "v0.1.0-alpha.13", Commit: strings.Repeat("c", 40), ReleaseID: 4, WindowsID: 5, MacID: 6}
	previous, err := signPayload(key, testPayload(t), public, now)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := makeManifest(options, previous, public, now)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := validatePayload(payload, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Releases) != 2 || parsed.Releases[0].Assets[0].Size != 3 || parsed.Releases[0].Assets[0].Name != "mac-lan.exe" || parsed.Releases[0].Assets[1].Name != "mac-lan-macos-arm64-commandline-preview.tar.gz" || parsed.Releases[1].Assets[0].Name != "MAC-DOW.exe" {
		t.Fatal("missing current artifact metadata or previous history")
	}
	previous[len(previous)/2] ^= 1
	if _, err := makeManifest(options, previous, public, now); err == nil {
		t.Fatal("merged untrusted previous feed")
	}
}
func TestPublisherAcceptsRenamedAssetsAndRetainsLegacyHistory(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"mac-lan.exe", "mac-lan-macos-arm64-commandline-preview.tar.gz", "MAC-DOW.exe", "MAC-DOW-macos-arm64-commandline-preview.tar.gz"} {
		raw := bytes.Replace(testPayload(t), []byte("MAC-DOW.exe"), []byte(name), 1)
		if _, err := validatePayload(raw, now); err != nil {
			t.Fatalf("supported asset %s refused: %v", name, err)
		}
	}
}

func TestRenamedMacArtifactStillRequiresPublicProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mac-lan-macos-arm64-commandline-preview.tar.gz")
	if err := os.WriteFile(path, macArchiveFixture(t, []byte("host"), []byte(`{"publicUpdate":false}`)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := artifactMetadata(path, "mac-lan-macos-arm64-commandline-preview.tar.gz", 1); err == nil {
		t.Fatal("renamed Mac archive bypassed public provenance gate")
	}
}

func TestManifestRenewsExpiredAuthenticatedHistory(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	var keyOutput bytes.Buffer
	if err := generateKey(key, &keyOutput); err != nil {
		t.Fatal(err)
	}
	public := strings.TrimSpace(keyOutput.String())
	oldTime := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	now := oldTime.Add(40 * 24 * time.Hour)
	previous, err := signPayload(key, testPayload(t), public, oldTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyEnvelope(previous, public, now); err == nil {
		t.Fatal("expired feed accepted for current verification")
	}
	windows, mac := filepath.Join(dir, "mac-lan.exe"), filepath.Join(dir, "mac.tar.gz")
	if err := os.WriteFile(windows, []byte("win"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mac, macArchiveFixture(t, []byte("mac"), []byte(`{"publicUpdate":true}`)), 0600); err != nil {
		t.Fatal(err)
	}
	options := manifestOptions{Windows: windows, Mac: mac, Tag: "v0.1.0-alpha.14", Commit: strings.Repeat("c", 40), ReleaseID: 4, WindowsID: 5, MacID: 6}
	payload, err := makeManifest(options, previous, public, now)
	if err != nil {
		t.Fatal("authenticated history could not be renewed", err)
	}
	parsed, err := validatePayload(payload, now)
	if err != nil || len(parsed.Releases) != 2 || parsed.Releases[1].Tag != "v0.1.0-alpha.12" {
		t.Fatal("renewed feed lost history", parsed, err)
	}
	previous[len(previous)/2] ^= 1
	if _, err := makeManifest(options, previous, public, now); err == nil {
		t.Fatal("renewal accepted forged expired history")
	}
}

func TestPrivateKeyRejectsCorruption(t *testing.T) {
	p := filepath.Join(t.TempDir(), "key")
	if err := generateKey(p, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	b[len(b)-1] ^= 1
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateKey(p); err == nil {
		t.Fatal("accepted inconsistent key")
	}
}

func TestSignCommandCannotReplacePrivateKey(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if err := generateKey(key, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(key)
	if err := run([]string{"sign", "--key", key, "--payload", filepath.Join(dir, "payload.json"), "--out", key}, &bytes.Buffer{}); err == nil {
		t.Fatal("allowed signed output to overwrite private key")
	}
	after, _ := os.ReadFile(key)
	if !bytes.Equal(before, after) {
		t.Fatal("private key changed")
	}
}

func TestManifestCommandIncludesOptionalSignedMenuApp(t *testing.T) {
	dir := t.TempDir()
	windows, mac, app, out := filepath.Join(dir, "mac-lan.exe"), filepath.Join(dir, "preview.tar.gz"), filepath.Join(dir, "app.tar.gz"), filepath.Join(dir, "payload.json")
	for path, data := range map[string][]byte{windows: []byte("win"), mac: macArchiveFixture(t, []byte("host"), []byte(`{"publicUpdate":true}`)), app: menuArchiveFixture(t, nil)} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"manifest", "--windows", windows, "--mac", mac, "--tag", "v0.1.0-alpha.15", "--commit", strings.Repeat("c", 40), "--release-id", "4", "--windows-id", "5", "--mac-id", "6", "--out", out}
	if err := run(append(append([]string{}, args...), "--mac-app", app, "--mac-app-id", "7"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var payload feedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Releases) != 1 || len(payload.Releases[0].Assets) != 3 {
		t.Fatal("app asset missing", payload)
	}
	asset := payload.Releases[0].Assets[2]
	if asset.Name != "MAC-DOW-macos-arm64-unsigned.tar.gz" || asset.ID != 7 || asset.Size <= 0 || len(asset.SHA256) != 64 {
		t.Fatal("app feed binding incorrect", asset)
	}
	for _, extra := range [][]string{{"--mac-app", app}, {"--mac-app-id", "7"}, {"--mac-app", app, "--mac-app-id", "0"}, {"--mac-app", app, "--mac-app-id", "-1"}} {
		if err := run(append(append([]string{}, args...), extra...), &bytes.Buffer{}); err == nil {
			t.Fatal("accepted partial or invalid app binding", extra)
		}
	}
}
