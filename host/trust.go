package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
	"unicode"
)

// Remembered PCs and a stable Mac identity let an approved Windows PC reconnect
// after either app restarts without a new approval on the Mac. Both files live in
// the per-user application support folder with owner-only permissions. Only a
// SHA-256 hash of each PC's secret is stored, so the file cannot be replayed.

const (
	maxTrustedDevices = 16
	maxTrustFile      = 64 << 10
	identityLifetime  = 10 * 365 * 24 * time.Hour
)

type trustedDevice struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Hash     string    `json:"hash"`
	Added    time.Time `json:"added"`
	LastSeen time.Time `json:"lastSeen"`
}

type trustStore struct {
	mu      sync.Mutex
	path    string
	devices []trustedDevice
}

// readPrivate reads a small owner-only regular file, refusing links and files
// another user could have written.
func readPrivate(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > limit || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("unsafe private mac-lan file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("private mac-lan file changed")
	}
	return io.ReadAll(io.LimitReader(file, limit+1))
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
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func loadTrustStore(dir string) *trustStore {
	t := &trustStore{path: filepath.Join(dir, "trusted-pcs.json")}
	data, err := readPrivate(t.path, maxTrustFile)
	if err != nil {
		return t
	}
	var devices []trustedDevice
	if json.Unmarshal(data, &devices) != nil {
		return t
	}
	for _, d := range devices {
		if validHex(d.ID, 32) && validHex(d.Hash, 64) {
			t.devices = append(t.devices, d)
		}
	}
	return t
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// deviceName keeps a short printable label supplied by Windows for the menu.
func deviceName(name string) string {
	out := make([]rune, 0, 64)
	for _, r := range name {
		if len(out) == 64 {
			break
		}
		if unicode.IsPrint(r) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "Windows PC"
	}
	return string(out)
}

func (t *trustStore) saveLocked() error {
	data, err := json.MarshalIndent(t.devices, "", " ")
	if err != nil {
		return err
	}
	return writePrivate(t.path, data)
}

// remember issues a new credential for a PC the user just approved on the Mac.
func (t *trustStore) remember(name string, now time.Time) (id, secret string, err error) {
	idBytes, secretBytes := make([]byte, 16), make([]byte, 32)
	if _, err = rand.Read(idBytes); err != nil {
		return "", "", err
	}
	if _, err = rand.Read(secretBytes); err != nil {
		return "", "", err
	}
	id, secret = hex.EncodeToString(idBytes), hex.EncodeToString(secretBytes)
	sum := sha256.Sum256([]byte(secret))
	t.mu.Lock()
	defer t.mu.Unlock()
	previous := append([]trustedDevice(nil), t.devices...)
	t.devices = append(t.devices, trustedDevice{ID: id, Name: deviceName(name), Hash: hex.EncodeToString(sum[:]), Added: now, LastSeen: now})
	if len(t.devices) > maxTrustedDevices {
		sort.Slice(t.devices, func(i, j int) bool { return t.devices[i].LastSeen.After(t.devices[j].LastSeen) })
		t.devices = t.devices[:maxTrustedDevices]
	}
	if err = t.saveLocked(); err != nil {
		t.devices = previous
		return "", "", err
	}
	return id, secret, nil
}

// verify checks a remembered PC's credential in constant time.
func (t *trustStore) verify(id, secret string, now time.Time) bool {
	if !validHex(id, 32) || !validHex(secret, 64) {
		return false
	}
	sum := sha256.Sum256([]byte(secret))
	presented := hex.EncodeToString(sum[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.devices {
		if t.devices[i].ID == id && subtle.ConstantTimeCompare([]byte(t.devices[i].Hash), []byte(presented)) == 1 {
			t.devices[i].LastSeen = now
			_ = t.saveLocked()
			return true
		}
	}
	return false
}

func (t *trustStore) forgetAll() (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(t.devices)
	t.devices = nil
	if err := t.saveLocked(); err != nil {
		if rerr := os.Remove(t.path); rerr != nil && !os.IsNotExist(rerr) {
			return n, err
		}
	}
	return n, nil
}

// forgetRemembered revokes every remembered PC, including this run's approvals
// and reconnect tokens, and disconnects the PC that is connected now.
func (s *server) forgetRemembered() (int, error) {
	n, err := s.trust.forgetAll()
	s.approvedMu.Lock()
	s.approved, s.resumes = nil, nil
	s.approvedMu.Unlock()
	s.connectionMu.Lock()
	c := s.active
	s.connectionMu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	return n, err
}

func (t *trustStore) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.devices)
}

type storedIdentity struct {
	Key  []byte `json:"key"`
	Cert []byte `json:"cert"`
}

// persistentIdentity keeps the Mac's TLS identity, and so its Mac ID and the
// fingerprint remembered PCs pin, across launches. A missing or unreadable file
// is replaced by a new identity, which makes remembered PCs ask once again.
func persistentIdentity(dir string) (tls.Certificate, string, error) {
	path := filepath.Join(dir, "identity.json")
	if data, err := readPrivate(path, maxTrustFile); err == nil {
		var stored storedIdentity
		if json.Unmarshal(data, &stored) == nil {
			if key, err := x509.ParseECPrivateKey(stored.Key); err == nil {
				if cert, err := x509.ParseCertificate(stored.Cert); err == nil && time.Now().Before(cert.NotAfter) {
					if pub, ok := cert.PublicKey.(*ecdsa.PublicKey); ok && pub.Equal(&key.PublicKey) {
						sum := sha256.Sum256(stored.Cert)
						return tls.Certificate{Certificate: [][]byte{stored.Cert}, PrivateKey: key}, hex.EncodeToString(sum[:]), nil
					}
				}
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "mac-lan Mac identity"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(identityLifetime), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	data, err := json.Marshal(storedIdentity{Key: keyDER, Cert: der})
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err = writePrivate(path, data); err != nil {
		return tls.Certificate{}, "", err
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(sum[:]), nil
}
