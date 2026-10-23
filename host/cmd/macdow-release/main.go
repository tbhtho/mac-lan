// macdow-release prepares signed update metadata locally. It never uploads files
// and never puts a private key into an application or update envelope.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tbhtho/MAC-DOW/host/internal/updater"
)

const maximumFeedBytes = 1024 * 1024

type feedEnvelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type feedPayload struct {
	Schema             int           `json:"schema"`
	Product            string        `json:"product"`
	SourceRepositoryID int64         `json:"sourceRepositoryId"`
	PublishedAt        string        `json:"publishedAt"`
	ExpiresAt          string        `json:"expiresAt"`
	Releases           []feedRelease `json:"releases"`
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
type manifestOptions struct {
	Windows, Mac, Tag, Commit   string
	ReleaseID, WindowsID, MacID int64
	MacApp                      string
	MacAppID                    int64
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "macdow-release:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("use keygen, manifest, sign, or verify")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(io.Discard)
	key := f.String("key", "", "private key path")
	output := f.String("out", "", "output file")
	payloadPath := f.String("payload", "", "exact UTF-8 payload file")
	feedPath := f.String("feed", "", "signed envelope file")
	previousPath := f.String("previous", "", "previous signed envelope")
	public := f.String("public-key", "", "trusted public key; defaults to embedded build config")
	windows := f.String("windows", "", "Windows EXE")
	mac := f.String("mac", "", "public Mac archive")
	macApp := f.String("mac-app", "", "optional public Mac menu-bar app archive")
	tag := f.String("tag", "", "v-prefixed SemVer")
	commit := f.String("commit", "", "source commit")
	releaseID := f.Int64("release-id", 0, "release identifier")
	windowsID := f.Int64("windows-id", 0, "Windows asset identifier")
	macID := f.Int64("mac-id", 0, "Mac asset identifier")
	macAppID := f.Int64("mac-app-id", 0, "optional Mac app asset identifier")
	if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 {
		return errors.New("invalid command arguments")
	}
	configured := updater.BuildConfig().UpdatePublicKey
	if *public == "" {
		*public = configured
	}
	if configured != "" && *public != configured {
		return errors.New("public key differs from application build config")
	}
	now := time.Now().UTC()
	switch args[0] {
	case "keygen":
		if *key == "" {
			return errors.New("keygen requires --key")
		}
		return generateKey(*key, out)
	case "sign":
		if *key == "" || *payloadPath == "" || *output == "" {
			return errors.New("sign requires --key, --payload, and --out")
		}
		if samePath(*key, *output) {
			return errors.New("signed output must never replace the private key")
		}
		payload, err := readBoundedRegular(*payloadPath, maximumFeedBytes)
		if err != nil {
			return fmt.Errorf("read payload: %w", err)
		}
		envelope, err := signPayload(*key, payload, *public, now)
		if err != nil {
			return err
		}
		if err = writeOutput(*output, envelope); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "Signed update feed prepared locally.")
		return err
	case "verify":
		if *feedPath == "" || *public == "" {
			return errors.New("verify requires --feed and a trusted public key")
		}
		envelope, err := readBoundedRegular(*feedPath, maximumFeedBytes*2)
		if err != nil {
			return err
		}
		if _, err = verifyEnvelope(envelope, *public, now); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "Update feed signature and bindings verified.")
		return err
	case "manifest":
		if *output == "" {
			return errors.New("manifest requires --out")
		}
		var previous []byte
		var err error
		if *previousPath != "" {
			previous, err = readBoundedRegular(*previousPath, maximumFeedBytes*2)
			if err != nil {
				return err
			}
		}
		payload, err := makeManifest(manifestOptions{Windows: *windows, Mac: *mac, Tag: *tag, Commit: *commit, ReleaseID: *releaseID, WindowsID: *windowsID, MacID: *macID, MacApp: *macApp, MacAppID: *macAppID}, previous, *public, now)
		if err != nil {
			return err
		}
		if err = writeOutput(*output, payload); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "Update payload prepared locally from artifact sizes and hashes.")
		return err
	default:
		return errors.New("unknown command; use keygen, manifest, sign, or verify")
	}
}

func generateKey(path string, out io.Writer) error {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// O_EXCL also refuses a dangling symlink and protects an existing key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("private key already exists or cannot be created; it was not overwritten")
	}
	if _, err = f.Write(private); err != nil {
		f.Close()
		return errors.New("private key write failed; inspect the new key file locally")
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, base64.StdEncoding.EncodeToString(public))
	return err
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != ed25519.PrivateKeySize {
		return nil, errors.New("private key must be a regular 64-byte Ed25519 key file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private key permissions must deny group and other access")
	}
	b, err := readBoundedRegular(path, ed25519.PrivateKeySize)
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("private key cannot be read")
	}
	derived := ed25519.NewKeyFromSeed(b[:ed25519.SeedSize])
	if !bytes.Equal(derived, b) {
		return nil, errors.New("private key is inconsistent")
	}
	return ed25519.PrivateKey(b), nil
}

func signPayload(keyPath string, payload []byte, configured string, now time.Time) ([]byte, error) {
	if _, err := validatePayload(payload, now); err != nil {
		return nil, err
	}
	key, err := readPrivateKey(keyPath)
	if err != nil {
		return nil, err
	}
	if configured != "" {
		public, err := decodePublicKey(configured)
		if err != nil || !bytes.Equal(public, key.Public().(ed25519.PublicKey)) {
			return nil, errors.New("private key does not match the trusted application public key")
		}
	}
	e := feedEnvelope{base64.StdEncoding.EncodeToString(payload), base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))}
	return json.Marshal(e)
}

func verifyEnvelope(raw []byte, trusted string, now time.Time) ([]byte, error) {
	payload, err := authenticatedPayload(raw, trusted)
	if err != nil {
		return nil, err
	}
	if _, err = validatePayload(payload, now); err != nil {
		return nil, err
	}
	return payload, nil
}

// Publication can renew expired, authenticated history after an inactive period.
// This never makes an expired envelope valid for a client or the verify command.
func verifyPreviousEnvelope(raw []byte, trusted string, now time.Time) (feedPayload, error) {
	payload, err := authenticatedPayload(raw, trusted)
	if err != nil {
		return feedPayload{}, err
	}
	var p feedPayload
	if err = decodeStrict(payload, &p); err != nil {
		return feedPayload{}, err
	}
	published, err := time.Parse(time.RFC3339, p.PublishedAt)
	if err != nil || published.After(now.Add(5*time.Minute)) {
		return feedPayload{}, errors.New("previous feed publication time is invalid")
	}
	return validatePayload(payload, published)
}

func authenticatedPayload(raw []byte, trusted string) ([]byte, error) {
	if len(raw) > maximumFeedBytes*2 {
		return nil, errors.New("signed envelope is too large")
	}
	public, err := decodePublicKey(trusted)
	if err != nil {
		return nil, err
	}
	var e feedEnvelope
	if err = decodeStrict(raw, &e); err != nil {
		return nil, errors.New("invalid signed envelope")
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(e.Payload)
	if err != nil || len(payload) > maximumFeedBytes {
		return nil, errors.New("invalid payload encoding")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(e.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(public, payload, signature) {
		return nil, errors.New("update signature does not match the trusted public key")
	}
	return payload, nil
}

func decodePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("a valid trusted Ed25519 public key is required")
	}
	return ed25519.PublicKey(b), nil
}

func decodeStrict(raw []byte, target any) error {
	if !utf8.Valid(raw) {
		return errors.New("JSON must be UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validatePayload(raw []byte, now time.Time) (feedPayload, error) {
	var p feedPayload
	if len(raw) == 0 || len(raw) > maximumFeedBytes {
		return p, errors.New("payload size is outside limits")
	}
	if err := decodeStrict(raw, &p); err != nil {
		return p, errors.New("payload has invalid fields or encoding")
	}
	if p.Schema != 1 || p.Product != "MAC-DOW" || p.SourceRepositoryID != 1430459188 {
		return p, errors.New("payload identity does not match MAC-DOW")
	}
	published, err := time.Parse(time.RFC3339, p.PublishedAt)
	if err != nil {
		return p, errors.New("invalid publishedAt")
	}
	expires, err := time.Parse(time.RFC3339, p.ExpiresAt)
	if err != nil || !expires.After(published) || expires.Sub(published) > 31*24*time.Hour || !expires.After(now) || published.After(now.Add(5*time.Minute)) {
		return p, errors.New("payload validity must be current and no longer than 31 days")
	}
	if len(p.Releases) == 0 || len(p.Releases) > 128 {
		return p, errors.New("payload requires 1 to 128 releases")
	}
	releaseIDs := map[int64]bool{}
	tags := map[string]bool{}
	assetIDs := map[int64]bool{}
	for _, r := range p.Releases {
		if r.ID <= 0 || releaseIDs[r.ID] || tags[r.Tag] || !strings.HasPrefix(r.Tag, "v") || !commitPattern.MatchString(r.Commit) {
			return p, errors.New("invalid or duplicate release binding")
		}
		version, err := updater.ParseVersion(strings.TrimPrefix(r.Tag, "v"))
		if err != nil || r.Prerelease != (len(version.Pre) > 0) {
			return p, errors.New("release tag and prerelease state disagree")
		}
		releaseIDs[r.ID], tags[r.Tag] = true, true
		if len(r.Assets) == 0 || len(r.Assets) > 8 {
			return p, errors.New("release requires a supported binary asset")
		}
		names := map[string]bool{}
		for _, a := range r.Assets {
			if a.ID <= 0 || assetIDs[a.ID] || names[a.Name] || a.Size <= 0 || !digestPattern.MatchString(a.SHA256) || !publisherAssetName(a.Name) {
				return p, errors.New("invalid or duplicate asset binding")
			}
			assetIDs[a.ID], names[a.Name] = true, true
		}
	}
	return p, nil
}

func publisherAssetName(name string) bool {
	switch name {
	case "mac-lan.exe", "mac-lan-macos-arm64-commandline-preview.tar.gz", "MAC-DOW.exe", "MAC-DOW-macos-arm64-commandline-preview.tar.gz", "MAC-DOW-macos-arm64-unsigned.tar.gz":
		return true
	}
	return false
}

func makeManifest(o manifestOptions, previous []byte, trusted string, now time.Time) ([]byte, error) {
	if o.Windows == "" || o.Mac == "" {
		return nil, errors.New("manifest requires --windows and --mac")
	}
	if (o.MacApp == "") != (o.MacAppID == 0) || o.MacAppID < 0 {
		return nil, errors.New("--mac-app and a positive --mac-app-id must be provided together")
	}
	p := feedPayload{Schema: 1, Product: "MAC-DOW", SourceRepositoryID: 1430459188, PublishedAt: now.UTC().Format(time.RFC3339), ExpiresAt: now.UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)}
	version, err := updater.ParseVersion(strings.TrimPrefix(o.Tag, "v"))
	if err != nil || !strings.HasPrefix(o.Tag, "v") {
		return nil, errors.New("manifest requires a v-prefixed canonical SemVer tag")
	}
	r := feedRelease{ID: o.ReleaseID, Tag: o.Tag, Commit: o.Commit, Prerelease: len(version.Pre) != 0}
	files := []struct {
		path, name string
		id         int64
	}{{o.Windows, "mac-lan.exe", o.WindowsID}, {o.Mac, "mac-lan-macos-arm64-commandline-preview.tar.gz", o.MacID}}
	if o.MacApp != "" {
		files = append(files, struct {
			path, name string
			id         int64
		}{o.MacApp, "MAC-DOW-macos-arm64-unsigned.tar.gz", o.MacAppID})
	}
	for _, file := range files {
		a, err := artifactMetadataBound(file.path, file.name, file.id, strings.TrimPrefix(o.Tag, "v"), o.Commit)
		if err != nil {
			return nil, err
		}
		r.Assets = append(r.Assets, a)
	}
	p.Releases = []feedRelease{r}
	if len(previous) != 0 {
		old, err := verifyPreviousEnvelope(previous, trusted, now)
		if err != nil {
			return nil, fmt.Errorf("previous feed cannot be trusted: %w", err)
		}
		for _, release := range old.Releases {
			if release.Tag == r.Tag {
				return nil, errors.New("release tag already exists in previous feed; do not replace published assets")
			}
			p.Releases = append(p.Releases, release)
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if _, err = validatePayload(raw, now); err != nil {
		return nil, err
	}
	return raw, nil
}

func artifactMetadata(path, name string, id int64) (feedAsset, error) {
	return artifactMetadataBound(path, name, id, "", "")
}

func artifactMetadataBound(path, name string, id int64, version, commit string) (feedAsset, error) {
	var a feedAsset
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return a, errors.New("artifact must be a nonempty regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return a, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return a, errors.New("artifact changed while opening")
	}
	if name == "mac-lan-macos-arm64-commandline-preview.tar.gz" || name == "MAC-DOW-macos-arm64-commandline-preview.tar.gz" || name == "MAC-DOW-macos-arm64-unsigned.tar.gz" {
		root := "macos-arm64"
		if name == "MAC-DOW-macos-arm64-unsigned.tar.gz" {
			root = "mac-lan.app"
		}
		if err = validatePublicMacArchiveBound(f, root, version, commit); err != nil {
			return a, err
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return a, err
		}
	}
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return a, err
	}
	after, err := f.Stat()
	if err != nil || size != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return a, errors.New("artifact changed while hashing")
	}
	return feedAsset{id, name, size, hex.EncodeToString(h.Sum(nil))}, nil
}

func readBoundedRegular(path string, max int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > max {
		return nil, errors.New("input must be a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("input changed while opening")
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, errors.New("input exceeds size limit")
	}
	return b, nil
}

func writeOutput(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("output must not be a symlink or directory")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".macdow-feed-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func samePath(a, b string) bool {
	left, leftErr := filepath.Abs(a)
	right, rightErr := filepath.Abs(b)
	if leftErr == nil && rightErr == nil && (left == right || runtime.GOOS == "windows" && strings.EqualFold(left, right)) {
		return true
	}
	l, le := os.Stat(a)
	r, re := os.Stat(b)
	return le == nil && re == nil && os.SameFile(l, r)
}
