package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tbhtho/MAC-DOW/host/internal/remote"
)

// The remote-command link is reachable only with the current pairing's token on
// the pinned listener, runs nothing until the Mac's user switches it on, and
// ends when the pairing's token is revoked.
func TestRemoteLinkNeedsPairingAndTheMacSwitch(t *testing.T) {
	mac := remote.New("", "Test Mac", "test")
	defer mac.Close()
	s := &server{peerAllowed: func(string) bool { return true }, transfers: newTransferReceiver(t.TempDir()), remote: mac}
	token, revoke, err := s.transfers.grant("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(s)
	defer ts.Close()
	for _, bearer := range []string{"", strings.Repeat("0", 64)} {
		r, _ := http.NewRequest(http.MethodGet, ts.URL+"/remote", nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := ts.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || mac.Connected() {
			t.Fatal("remote link reachable without the pairing token", resp.StatusCode)
		}
	}
	r, _ := http.NewRequest(http.MethodGet, ts.URL+"/remote", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", "https://example.test")
	if resp, err := ts.Client().Do(r); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatal("a browser origin must be refused")
	}

	pc := remote.New("", "Test PC", "test")
	defer pc.Close()
	link := remote.NewServer(pc)
	defer link.Unlink()
	sum := sha256.Sum256(ts.Certificate().Raw)
	host, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	number, _ := strconv.Atoi(port)
	if err = link.Link(host, number, hex.EncodeToString(sum[:]), token); err != nil {
		t.Fatal(err)
	}
	accepts := func() bool {
		for _, d := range pc.Devices() {
			if !d.ThisDevice {
				return d.Connected && d.Name == "Test Mac" && d.CommandsEnabled
			}
		}
		return false
	}
	wait := func(what string, ready func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !ready(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal(what)
			}
		}
	}
	wait("the PC never linked to the Mac", func() bool { return mac.Connected() && pc.Connected() })
	if accepts() {
		t.Fatal("commands must be off on a Mac whose user has not switched them on")
	}
	if _, err = pc.Exec(remote.ExecRequest{Command: "echo hi"}); err == nil {
		t.Fatal("a command was accepted while the Mac's switch is off")
	}
	if err = mac.SetSettings(remote.Settings{Accept: true}); err != nil {
		t.Fatal(err)
	}
	wait("the PC never saw the Mac's switch turn on", accepts)
	job, err := pc.Exec(remote.ExecRequest{Command: "echo ran-on-mac"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := pc.WaitDone(job.View().ID, 20*time.Second)
	if err != nil || view.State != "succeeded" || !strings.Contains(view.Output, "ran-on-mac") {
		t.Fatalf("command over the pinned listener failed: %+v %v", view, err)
	}
	// Ending the pairing ends the link, and the old token cannot reopen it.
	revoke()
	mac.Drop()
	wait("the link survived the end of the pairing", func() bool { return !mac.Connected() })
	time.Sleep(1500 * time.Millisecond) // the PC redials with the revoked token
	if mac.Connected() {
		t.Fatal("a revoked token reopened the remote link")
	}
}
