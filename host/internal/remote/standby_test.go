package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStandbyLinkTakesOverAndYields(t *testing.T) {
	macDir, pcDir := t.TempDir(), t.TempDir()
	// The Mac: the app's node and the standby service's node share one folder.
	app, service := New(macDir, "Test Mac", "test"), New(macDir, "Test Mac", "test")
	origin := New(pcDir, "Test PC", "test")
	origin.learns = true
	server := NewServer(origin)
	appToken, serviceToken := strings.Repeat("ab", 32), strings.Repeat("cd", 32)
	appWeb := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote" || r.Header.Get("Authorization") != "Bearer "+appToken {
			http.Error(w, "forbidden", 403)
			return
		}
		app.Accept(w, r)
	}))
	serviceWeb := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { service.ServeStandby(w, r, serviceToken) }))
	t.Cleanup(func() {
		server.Close(pcDir)
		origin.Close()
		app.Close()
		service.Close()
		appWeb.Close()
		serviceWeb.Close()
	})
	pin := func(web *httptest.Server) string {
		sum := sha256.Sum256(web.Certificate().Raw)
		return hex.EncodeToString(sum[:])
	}
	port := func(web *httptest.Server) (string, int) {
		host, text, _ := net.SplitHostPort(web.Listener.Addr().String())
		number, _ := strconv.Atoi(text)
		return host, number
	}
	_, servicePort := port(serviceWeb)
	if err := WriteStandbyOffer(macDir, StandbyOffer{Port: servicePort, Token: serviceToken, Fingerprint: pin(serviceWeb)}); err != nil {
		t.Fatal(err)
	}
	// The switch is shared: the service sees what the app's node saved.
	if service.Settings().Accept {
		t.Fatal("commands must start switched off")
	}
	if err := app.SetSettings(Settings{Accept: true}); err != nil {
		t.Fatal(err)
	}
	if !service.Settings().Accept {
		t.Fatal("the standby service must follow the Mac's switch")
	}
	if err := server.Listen(pcDir); err != nil {
		t.Fatal(err)
	}
	until := func(what string, limit time.Duration, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if ok() {
				return
			}
		}
		t.Fatal("timed out waiting for " + what)
	}
	onStandby := func() bool {
		l := origin.current()
		if l == nil || !l.standby {
			return false
		}
		_, known := l.peerInfo()
		return known
	}
	onNormal := func() bool {
		l := origin.current()
		if l == nil || l.standby {
			return false
		}
		_, known := l.peerInfo()
		return known
	}
	// Nothing is known yet, so there is no standby connection.
	time.Sleep(3500 * time.Millisecond)
	if origin.current() != nil {
		t.Fatal("the PC must not know a standby link before the Mac tells it")
	}
	host, appPort := port(appWeb)
	if err := server.Link(host, appPort, pin(appWeb), appToken); err != nil {
		t.Fatal(err)
	}
	until("the normal link", 10*time.Second, onNormal)
	until("the standby details", 10*time.Second, func() bool { _, ok := readRoute(pcDir); return ok })
	if route, _ := readRoute(pcDir); route.Port != servicePort || route.Token != serviceToken || len(route.Addresses) != 1 || route.Addresses[0] != host {
		t.Fatalf("remembered route: %+v", route)
	}
	// mac-lan on the Mac goes away: the standby link takes over by itself.
	server.Unlink()
	app.Drop()
	until("the standby link", 15*time.Second, onStandby)
	note := ""
	for _, device := range origin.Devices() {
		if !device.ThisDevice {
			note = device.Note
		}
	}
	if !strings.Contains(note, "standby") {
		t.Fatalf("tools must be told they are on the standby link: %q", note)
	}
	j, err := origin.Exec(ExecRequest{Command: "echo over-standby", Origin: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if view := finish(t, origin, j, 20*time.Second); view.State != "succeeded" || !strings.Contains(view.Output, "over-standby") {
		t.Fatalf("command over the standby link: %+v", view)
	}
	// The normal link returns while a command runs on the standby one: it waits.
	slow, _ := origin.Exec(ExecRequest{Command: slowCommand(4), Origin: "agent"})
	time.Sleep(300 * time.Millisecond)
	if err = server.Link(host, appPort, pin(appWeb), appToken); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if !onStandby() {
		t.Fatal("the normal link must not interrupt a command running over the standby link")
	}
	if view := finish(t, origin, slow, 30*time.Second); view.State != "succeeded" {
		t.Fatalf("the command on the standby link was cut off: %+v", view)
	}
	until("the normal link to take over", 20*time.Second, onNormal)
	// With commands switched off on the Mac the standby link refuses like the normal one.
	_ = app.SetSettings(Settings{})
	server.Unlink()
	app.Drop()
	until("the standby link again", 15*time.Second, onStandby)
	until("the switch to arrive", 5*time.Second, func() bool { peer, _ := origin.current().peerInfo(); return !peer.Accept })
	if _, err = origin.Exec(ExecRequest{Command: "echo no", Origin: "agent"}); err == nil || !strings.Contains(err.Error(), "switched off") {
		t.Fatalf("the Mac's switch must govern the standby link: %v", err)
	}
	// A wrong token or a missing one gets nothing.
	for _, header := range []string{"", "Bearer " + appToken, "Bearer short"} {
		request, _ := http.NewRequest("GET", serviceWeb.URL+"/remote", nil)
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		response, err := serviceWeb.Client().Do(request)
		if err != nil || response.StatusCode != 403 {
			t.Fatalf("the standby service must refuse %q", header)
		}
		response.Body.Close()
	}
	ForgetStandby(pcDir)
	if _, err = os.Stat(routePath(pcDir)); err == nil {
		t.Fatal("forgetting must remove the standby details")
	}
}
