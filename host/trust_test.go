package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTrustStoreRemembersVerifiesAndForgets(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	store := loadTrustStore(dir)
	id, secret, err := store.remember("Gaming PC\x07", now)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "trusted-pcs.json"))
	if strings.Contains(string(data), secret) {
		t.Fatal("the stored file must hold only a hash of the PC secret")
	}
	reloaded := loadTrustStore(dir)
	if !reloaded.verify(id, secret, now) {
		t.Fatal("a remembered PC must still be accepted after the Mac app restarts")
	}
	if reloaded.devices[0].Name != "Gaming PC" {
		t.Fatal("control characters must be removed from the PC name", reloaded.devices[0].Name)
	}
	if reloaded.verify(id, strings.Repeat("0", 64), now) || reloaded.verify(strings.Repeat("0", 32), secret, now) || reloaded.verify(id, "short", now) {
		t.Fatal("a wrong secret or device id must be rejected")
	}
	if n, err := reloaded.forgetAll(); err != nil || n != 1 || loadTrustStore(dir).verify(id, secret, now) {
		t.Fatal("Forget Remembered PCs must revoke saved PCs across restarts", n)
	}
}

func TestTrustStoreKeepsMostRecentDevices(t *testing.T) {
	store := loadTrustStore(t.TempDir())
	start := time.Now()
	first, firstSecret, _ := store.remember("old", start)
	for i := 1; i <= maxTrustedDevices; i++ {
		if _, _, err := store.remember("pc", start.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if store.count() != maxTrustedDevices || store.verify(first, firstSecret, start) {
		t.Fatal("the least recently used PC is dropped beyond the limit")
	}
}

func TestPersistentIdentitySurvivesRestartAndRecoversFromDamage(t *testing.T) {
	dir := t.TempDir()
	_, first, err := persistentIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, again, err := persistentIdentity(dir)
	if err != nil || again != first {
		t.Fatal("the Mac identity must stay the same across launches", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "identity.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, replaced, err := persistentIdentity(dir)
	if err != nil || replaced == first || len(replaced) != 64 {
		t.Fatal("a damaged identity file is replaced by a new identity", err)
	}
}

func TestPairingRemembersPCAndTrustedLoginSkipsPrompt(t *testing.T) {
	prompts := 0
	s := &server{token: strings.Repeat("b", 64), slot: make(chan struct{}, 1), peerAllowed: func(string) bool { return true }, trust: loadTrustStore(t.TempDir()),
		pair:    func(string, string) bool { prompts++; return true },
		catalog: func() ([]window, error) { return []window{}, nil }, consent: func(start) bool { return false }}
	ts := httptest.NewTLSServer(s)
	defer ts.Close()
	roots := x509.NewCertPool()
	roots.AddCert(ts.Certificate())
	dial := func(auth map[string]string) (map[string]any, error) {
		d := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: roots}}
		c, _, err := d.Dial("wss"+strings.TrimPrefix(ts.URL, "https"), nil)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		c.WriteJSON(auth)
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		var catalog map[string]any
		err = c.ReadJSON(&catalog)
		return catalog, err
	}
	catalog, err := dial(map[string]string{"type": "pair", "code": "123456", "name": "Desk PC"})
	if err != nil || prompts != 1 {
		t.Fatal("guided pairing", err, prompts)
	}
	raw, _ := json.Marshal(catalog["trustedDevice"])
	var device struct{ ID, Token string }
	if json.Unmarshal(raw, &device) != nil || len(device.ID) != 32 || len(device.Token) != 64 {
		t.Fatal("an approved pairing must hand Windows a remembered-PC credential", catalog)
	}
	waitForSlot(t, s)
	again, err := dial(map[string]string{"type": "trusted", "device": device.ID, "token": device.Token})
	if err != nil || again["type"] != "catalog" || prompts != 1 {
		t.Fatal("a remembered PC must connect without a new prompt on the Mac", err, prompts)
	}
	if _, ok := again["trustedDevice"]; ok {
		t.Fatal("a remembered login must not mint another credential")
	}
	waitForSlot(t, s)
	refused, _ := dial(map[string]string{"type": "trusted", "device": device.ID, "token": strings.Repeat("0", 64)})
	if refused["type"] != "error" || refused["code"] != "unknown-device" {
		t.Fatal("a wrong remembered-PC secret must be refused with an explicit unknown-device error", refused)
	}
}

func waitForSlot(t *testing.T, s *server) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.tryTakeSlot() {
			<-s.slot
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session slot was not released")
}
