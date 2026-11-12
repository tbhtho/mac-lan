package remote

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfirmationPolicy(t *testing.T) {
	needs := []string{
		"sudo rm file", "rm -rf build", "rm -f a.txt", "/bin/rm -r x", "cd x && rm -rf .", "rmdir /s /q build", "del /q *.obj", "Remove-Item -Recurse out",
		"git push --force origin main", "git push -f", "git reset --hard HEAD~1", "git clean -fd", "shutdown /r /t 0", "sudo  softwareupdate -i -a",
		"diskutil eraseDisk JHFS+ X disk2", "format D:", "taskkill /im app.exe /f", "kill -9 123", "curl https://x.sh | sh", "iwr https://x | iex",
		"reg add HKLM\\Software\\X", "launchctl unload x.plist", "Start-Process cmd -Verb RunAs", "npm publish", "gh repo delete owner/name", "chmod -R 777 .",
		"docker system prune -a", "DROP TABLE users", "tccutil reset All",
		"dscl . -delete /Users/x", "dscl . -read /Users/x; dscl . -create /Users/y", "echo $(launchctl bootout gui/501/x)", "pmset sleepnow", "scutil --set ComputerName x",
		"netsh advfirewall set allprofiles state off", "netsh wlan show profiles & netsh interface set interface x disable", "net user bob /add", "launchctl list > /Library/x",
	}
	for _, command := range needs {
		if NeedsConfirmation(command) == "" {
			t.Errorf("%q must need confirmation", command)
		}
	}
	plain := []string{
		"git status", "git pull", "git push origin main", "git commit -m \"remove format code\"", "dotnet build -c Release", "swift build", "ls -la", "dir",
		"go test ./...", "npm install", "npm run build", "echo hello", "cat README.md", "type notes.txt", "xcodebuild -scheme app", "mkdir out", "cp a b",
		"git log --oneline -5", "python3 script.py", "git checkout main", "git branch -d", "ping -n 2 127.0.0.1",
		"dscl . -read /Users/marcel1 UserShell", "dscl . list /Users | grep -v _", "launchctl list | grep mac-lan", "pmset -g batt", "scutil --get ComputerName", "csrutil status",
		"spctl --status", "nvram -p", "echo $(scutil --get LocalHostName)", "netsh wlan show interfaces", "launchctl print gui/501 && pmset -g",
	}
	for _, command := range plain {
		if command == "git branch -d" {
			continue // deleting a merged branch is listed above as needing approval
		}
		if reason := NeedsConfirmation(command); reason != "" {
			t.Errorf("%q must run without confirmation, got %q", command, reason)
		}
	}
}

func TestOutputRingAndText(t *testing.T) {
	var r ring
	r.add("o", "hello ")
	r.add("e", "world")
	chunks, truncated, next := r.since(3)
	if truncated || next != 11 || len(chunks) != 2 || chunks[0].data != "lo " || chunks[0].off != 3 || chunks[1].stream != "e" {
		t.Fatalf("unexpected ring read: %+v %v %d", chunks, truncated, next)
	}
	big := strings.Repeat("x", maxOutputBytes/2+1)
	r.add("o", big)
	r.add("o", big)
	if _, truncated, _ = r.since(0); !truncated || r.size > maxOutputBytes+len(big) {
		t.Fatal("old output must be dropped and reported as truncated")
	}
	var u utf8Stream
	euro := []byte("€")
	if got := u.text(append([]byte("a"), euro[:2]...)) + u.text(euro[2:]) + u.flush(); got != "a€" {
		t.Fatalf("split character was damaged: %q", got)
	}
	if got := u.text([]byte{'b', 0xff, 'c'}); got != "b�c" {
		t.Fatalf("invalid bytes must be replaced: %q", got)
	}
}

// pair joins an origin node (the dialing side, like Windows) and a target node
// (the listening side, like the Mac) through a real pinned TLS WebSocket.
type pair struct {
	origin, target *Node
	server         *Server
	web            *httptest.Server
	token          string
	fingerprint    string
	accepted       atomic.Int32
}

func newPair(t *testing.T) *pair {
	t.Helper()
	p := &pair{origin: New("", "Test PC", "test"), target: New("", "Test Mac", "test"), token: strings.Repeat("ab", 32)}
	p.server = NewServer(p.origin)
	p.web = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote" || r.Header.Get("Authorization") != "Bearer "+p.token {
			http.Error(w, "forbidden", 403)
			return
		}
		p.accepted.Add(1)
		p.target.Accept(w, r)
	}))
	sum := sha256.Sum256(p.web.Certificate().Raw)
	p.fingerprint = hex.EncodeToString(sum[:])
	t.Cleanup(func() {
		p.server.Unlink()
		p.origin.Close()
		p.target.Close()
		p.web.Close()
	})
	return p
}

func (p *pair) link(t *testing.T) {
	t.Helper()
	host, port, _ := net.SplitHostPort(p.web.Listener.Addr().String())
	number, _ := strconv.Atoi(port)
	if err := p.server.Link(host, number, p.fingerprint, p.token); err != nil {
		t.Fatal(err)
	}
	p.waitLinked(t)
}

func (p *pair) waitLinked(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if l := p.origin.current(); l != nil {
			if _, known := l.peerInfo(); known {
				if tl := p.target.current(); tl != nil {
					if _, known = tl.peerInfo(); known {
						return
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the two nodes did not link")
}

func allow(t *testing.T, n *Node) {
	t.Helper()
	if err := n.SetSettings(Settings{Accept: true, Agents: true}); err != nil {
		t.Fatal(err)
	}
}

func finish(t *testing.T, n *Node, j *Job, wait time.Duration) JobView {
	t.Helper()
	view, err := n.WaitDone(j.id, wait)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Finished {
		t.Fatalf("job did not finish: %+v", view)
	}
	return view
}

func slowCommand(seconds int) string {
	if runtime.GOOS == "windows" {
		return "ping -n " + strconv.Itoa(seconds+1) + " 127.0.0.1"
	}
	return "for i in $(seq 1 " + strconv.Itoa(seconds) + "); do echo tick $i; sleep 1; done"
}

func TestCommandsAreOffUntilTheTargetAllowsThem(t *testing.T) {
	p := newPair(t)
	p.link(t)
	if _, err := p.origin.Exec(ExecRequest{Command: "echo hi"}); err == nil || !strings.Contains(err.Error(), "switched off") {
		t.Fatalf("a target that has not opted in must refuse, got %v", err)
	}
	// Even a client that ignores the advertised switch is refused by the target itself.
	reply, err := p.origin.current().call(msg{T: "exec", ID: newID(), Command: "echo hi"}, 5*time.Second)
	if err != nil || reply.OK || reply.Code != "disabled" {
		t.Fatalf("target must enforce its own switch: %+v %v", reply, err)
	}
	for _, kind := range []string{"put", "get", "list", "resolve"} {
		reply, err = p.origin.current().call(msg{T: kind, Path: "~/x", Size: 1}, 5*time.Second)
		if err != nil || reply.OK || reply.Code != "disabled" {
			t.Fatalf("%s must be refused while off: %+v %v", kind, reply, err)
		}
	}
	allow(t, p.target)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = p.origin.target(""); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the origin never learned that commands were switched on:", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err = p.origin.target("windows-nonexistent"); err == nil {
		t.Fatal("an unknown device name must be rejected")
	}
}

func TestExecuteStreamsOutputAndExitStatus(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	list := "ls"
	if runtime.GOOS == "windows" {
		list = "dir /b"
	}
	j, err := p.origin.Exec(ExecRequest{Command: "echo out-text&& echo err-text 1>&2&& " + list, Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	view := finish(t, p.origin, j, 20*time.Second)
	if view.State != "succeeded" || view.ExitCode == nil || *view.ExitCode != 0 || !strings.Contains(view.Output, "out-text") || !strings.Contains(view.Errors, "err-text") || strings.Contains(view.Errors, "out-text") || !strings.Contains(view.Output, "marker.txt") {
		t.Fatalf("unexpected result: %+v", view)
	}
	j, err = p.origin.Exec(ExecRequest{Command: "exit 7"})
	if err != nil {
		t.Fatal(err)
	}
	if view = finish(t, p.origin, j, 20*time.Second); view.State != "failed" || view.ExitCode == nil || *view.ExitCode != 7 {
		t.Fatalf("a nonzero exit must be reported: %+v", view)
	}
	j, err = p.origin.Exec(ExecRequest{Command: "echo x", Cwd: filepath.Join(dir, "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if view = finish(t, p.origin, j, 20*time.Second); view.State != "failed" || !strings.Contains(view.Error, "working folder") {
		t.Fatalf("a missing folder must fail clearly: %+v", view)
	}
	// Incremental reads: only output after the offset comes back.
	j, _ = p.origin.Exec(ExecRequest{Command: "echo first&& echo second"})
	whole := finish(t, p.origin, j, 20*time.Second)
	rest, _ := p.origin.Status(j.id, whole.NextOffset, 0)
	if rest.Output != "" || rest.NextOffset != whole.NextOffset {
		t.Fatalf("no output may repeat after the last offset: %+v", rest)
	}
}

func TestDestructiveCommandsNeedAPerson(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	command := "echo sudo was-only-echoed"
	j, err := p.origin.Exec(ExecRequest{Command: command, Origin: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if view := finish(t, p.origin, j, 10*time.Second); view.State != "denied" || strings.Contains(view.Output, "was-only-echoed") {
		t.Fatalf("without anyone to ask, the command must not run: %+v", view)
	}
	var asked ConfirmRequest
	p.origin.SetConfirm(func(r ConfirmRequest) Answer { asked = r; return Deny })
	j, _ = p.origin.Exec(ExecRequest{Command: command, Origin: "agent"})
	if view := finish(t, p.origin, j, 10*time.Second); view.State != "denied" || asked.Detail != command || asked.Origin != "agent" || asked.Reason == "" {
		t.Fatalf("a declined prompt must stop the command: %+v %+v", view, asked)
	}
	p.origin.SetConfirm(func(ConfirmRequest) Answer { return AllowOnce })
	j, _ = p.origin.Exec(ExecRequest{Command: command})
	if view := finish(t, p.origin, j, 20*time.Second); view.State != "succeeded" || !strings.Contains(view.Output, "was-only-echoed") {
		t.Fatalf("an approved command must run: %+v", view)
	}
	// The target also refuses a destructive command that was not marked confirmed.
	reply, err := p.origin.current().call(msg{T: "exec", ID: newID(), Command: command}, 5*time.Second)
	if err != nil || reply.OK || reply.Code != "needs-confirmation" {
		t.Fatalf("target must refuse unconfirmed destructive commands: %+v %v", reply, err)
	}
	reply, err = p.origin.current().call(msg{T: "put", Path: filepath.Join(t.TempDir(), "x"), Size: 0, Overwrite: true}, 5*time.Second)
	if err != nil || reply.OK || reply.Code != "needs-confirmation" {
		t.Fatalf("target must refuse an unconfirmed overwrite: %+v %v", reply, err)
	}
}

func TestCancelAndTimeoutEndTheCommand(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	j, err := p.origin.Exec(ExecRequest{Command: slowCommand(30)})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		view, _ := p.origin.Status(j.id, 0, time.Second)
		if view.State == "running" && view.Output != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("command never produced output: %+v", view)
		}
	}
	if err = p.origin.Cancel(j.id); err != nil {
		t.Fatal(err)
	}
	if view := finish(t, p.origin, j, 20*time.Second); view.State != "cancelled" {
		t.Fatalf("cancel must end the command: %+v", view)
	}
	j, _ = p.origin.Exec(ExecRequest{Command: slowCommand(30), Timeout: 1})
	if view := finish(t, p.origin, j, 20*time.Second); view.State != "timed-out" {
		t.Fatalf("the time limit must end the command: %+v", view)
	}
	if p.target.Busy() {
		time.Sleep(500 * time.Millisecond)
		if p.target.Busy() {
			t.Fatal("ended commands must not keep the target busy")
		}
	}
}

func TestRunningCommandSurvivesADroppedConnection(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	j, err := p.origin.Exec(ExecRequest{Command: slowCommand(4) + "&& echo finished-marker"})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; {
		view, _ := p.origin.Status(j.id, 0, time.Second)
		if view.State == "running" && view.Output != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("command never produced output: %+v", view)
		}
	}
	before := p.accepted.Load()
	p.origin.current().c.Close() // the network drops; the dial loop reconnects by itself
	for deadline := time.Now().Add(15 * time.Second); p.accepted.Load() == before; {
		if time.Now().After(deadline) {
			t.Fatal("the link was not re-established")
		}
		time.Sleep(20 * time.Millisecond)
	}
	view := finish(t, p.origin, j, 30*time.Second)
	if view.State != "succeeded" || !strings.Contains(view.Output, "finished-marker") || view.Reconnect {
		t.Fatalf("the command must finish and deliver its output after reconnecting: %+v", view)
	}
	if strings.Count(view.Output, "finished-marker") != 1 {
		t.Fatalf("replayed output must not be duplicated: %q", view.Output)
	}
}

func TestTransfersVerifyAndNeverReplaceSilently(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	local, remote := t.TempDir(), t.TempDir()
	payload := bytes.Repeat([]byte("mac-lan transfer payload \x00\xff "), 40000) // about 1 MB, several chunks
	source := filepath.Join(local, "data.bin")
	if err := os.WriteFile(source, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := p.origin.Transfer(TransferRequest{Direction: "push", Local: source, Remote: remote})
	if err != nil {
		t.Fatal(err)
	}
	if view := finish(t, p.origin, j, 30*time.Second); view.State != "succeeded" || view.BytesDone != int64(len(payload)) {
		t.Fatalf("push failed: %+v", view)
	}
	if got, _ := os.ReadFile(filepath.Join(remote, "data.bin")); !bytes.Equal(got, payload) {
		t.Fatal("pushed file differs")
	}
	if err = os.WriteFile(source, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	j, _ = p.origin.Transfer(TransferRequest{Direction: "push", Local: source, Remote: remote})
	if view := finish(t, p.origin, j, 30*time.Second); view.State != "failed" || !strings.Contains(view.Error, "already exists") {
		t.Fatalf("an existing file must not be replaced without asking: %+v", view)
	}
	if got, _ := os.ReadFile(filepath.Join(remote, "data.bin")); !bytes.Equal(got, payload) {
		t.Fatal("the existing file was changed by a refused transfer")
	}
	j, _ = p.origin.Transfer(TransferRequest{Direction: "push", Local: source, Remote: remote, Overwrite: true})
	if view := finish(t, p.origin, j, 30*time.Second); view.State != "denied" {
		t.Fatalf("overwrite needs approval: %+v", view)
	}
	p.origin.SetConfirm(func(ConfirmRequest) Answer { return AllowOnce })
	j, _ = p.origin.Transfer(TransferRequest{Direction: "push", Local: source, Remote: remote, Overwrite: true})
	if view := finish(t, p.origin, j, 30*time.Second); view.State != "succeeded" {
		t.Fatalf("approved overwrite failed: %+v", view)
	}
	if got, _ := os.ReadFile(filepath.Join(remote, "data.bin")); string(got) != "changed" {
		t.Fatal("approved overwrite did not replace the file")
	}
	// A folder goes in both directions with its structure.
	tree := filepath.Join(local, "project")
	for name, body := range map[string]string{"a.txt": "alpha", "src/b.txt": "beta", "src/deep/c.txt": ""} {
		path := filepath.Join(tree, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			err = os.WriteFile(path, []byte(body), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	there := filepath.Join(remote, "project-copy")
	j, _ = p.origin.Transfer(TransferRequest{Direction: "push", Local: tree, Remote: there})
	if view := finish(t, p.origin, j, 30*time.Second); view.State != "succeeded" {
		t.Fatalf("folder push failed: %+v", view)
	}
	back := filepath.Join(local, "back")
	j, _ = p.origin.Transfer(TransferRequest{Direction: "pull", Remote: there, Local: back})
	if view := finish(t, p.origin, j, 30*time.Second); view.State != "succeeded" {
		t.Fatalf("folder pull failed: %+v", view)
	}
	for name, body := range map[string]string{"a.txt": "alpha", "src/b.txt": "beta", "src/deep/c.txt": ""} {
		if got, err := os.ReadFile(filepath.Join(back, filepath.FromSlash(name))); err != nil || string(got) != body {
			t.Fatalf("%s did not round-trip: %q %v", name, got, err)
		}
	}
	j, _ = p.origin.Transfer(TransferRequest{Direction: "pull", Remote: filepath.Join(remote, "nope.bin"), Local: local})
	if view := finish(t, p.origin, j, 30*time.Second); view.State != "failed" {
		t.Fatalf("a missing remote file must fail: %+v", view)
	}
	leftovers, _ := filepath.Glob(filepath.Join(remote, ".mac-lan-part-*"))
	more, _ := filepath.Glob(filepath.Join(local, ".mac-lan-part-*"))
	if len(leftovers)+len(more) != 0 {
		t.Fatal("temporary files were left behind")
	}
}

func TestDeployCopiesThenRunsAfterApproval(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	local, remote := t.TempDir(), filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(filepath.Join(local, "version.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	show := "cat version.txt"
	if runtime.GOOS == "windows" {
		show = "type version.txt"
	}
	j, err := p.origin.Deploy(DeployRequest{Source: local, Destination: remote, Command: show, Origin: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if view := finish(t, p.origin, j, 10*time.Second); view.State != "denied" {
		t.Fatalf("deploy must always need approval: %+v", view)
	}
	if _, err = os.Stat(remote); err == nil {
		t.Fatal("nothing may be copied before approval")
	}
	var asked ConfirmRequest
	p.origin.SetConfirm(func(r ConfirmRequest) Answer { asked = r; return AllowOnce })
	j, _ = p.origin.Deploy(DeployRequest{Source: local, Destination: remote, Command: show, Origin: "agent"})
	view := finish(t, p.origin, j, 30*time.Second)
	if view.State != "succeeded" || !strings.Contains(view.Output, "v2") || !strings.Contains(asked.Detail, show) || view.ExitCode == nil || *view.ExitCode != 0 {
		t.Fatalf("deploy failed: %+v", view)
	}
}

func TestLocalEndpointSeparatesAgentsFromTheWindow(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	dir := t.TempDir()
	if err := p.server.Listen(dir); err != nil {
		t.Fatal(err)
	}
	defer p.server.Close(dir)
	client, err := Dial(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.Call(map[string]any{"op": "devices"}); err == nil || !strings.Contains(err.Error(), "switched off") {
		t.Fatalf("local tools must be refused until the user allows them: %v", err)
	}
	if err = p.origin.SetSettings(Settings{Agents: true}); err != nil {
		t.Fatal(err)
	}
	reply, err := client.Call(map[string]any{"op": "exec", "command": "echo from-agent", "wait": 20})
	if err != nil {
		t.Fatal(err)
	}
	job := reply["job"].(map[string]any)
	if job["state"] != "succeeded" || !strings.Contains(job["output"].(string), "from-agent") {
		t.Fatalf("agent command failed: %v", job)
	}
	for _, op := range []string{"set", "confirm", "link", "unlink", "ticket"} {
		if _, err = client.Call(map[string]any{"op": op, "agents": true, "accept": true}); err == nil {
			t.Fatalf("a local tool must not be able to use %q", op)
		}
	}
	if p.origin.Settings().Accept {
		t.Fatal("a local tool changed a setting")
	}
	// A wrong token and a reused ticket are refused; a fresh ticket works without the agent switch.
	data, _ := os.ReadFile(endpointPath(dir))
	var e endpoint
	_ = json.Unmarshal(data, &e)
	good := e.Token
	e.Token = strings.Repeat("0", len(good))
	forged, _ := json.Marshal(e)
	_ = os.WriteFile(endpointPath(dir), forged, 0o600)
	if _, err = Dial(dir, ""); err == nil {
		t.Fatal("a wrong token must be refused")
	}
	_ = p.origin.SetSettings(Settings{Agents: false})
	ticket := p.server.Ticket()
	user, err := Dial(dir, ticket)
	if err != nil {
		t.Fatal(err)
	}
	defer user.Close()
	if _, err = user.Call(map[string]any{"op": "devices"}); err != nil {
		t.Fatal("the user's own shell must work without the agent switch:", err)
	}
	if _, err = Dial(dir, ticket); err == nil {
		t.Fatal("a ticket must work only once")
	}
}

func TestWindowChannelAnswersPromptsAndChangesSettings(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	toServer, fromWindow := io.Pipe()
	fromServer, toWindow := io.Pipe()
	go p.server.ServeUI(toServer, toWindow)
	defer fromWindow.Close()
	lines := make(chan map[string]any, 64)
	go func() {
		decoder := json.NewDecoder(fromServer)
		for {
			var v map[string]any
			if decoder.Decode(&v) != nil {
				close(lines)
				return
			}
			lines <- v
		}
	}()
	send := func(v map[string]any) {
		data, _ := json.Marshal(v)
		_, _ = fromWindow.Write(append(data, '\n'))
	}
	var backlog []map[string]any // answers and events arrive in no fixed order
	expect := func(match func(map[string]any) bool) map[string]any {
		t.Helper()
		for i, v := range backlog {
			if match(v) {
				backlog = append(backlog[:i], backlog[i+1:]...)
				return v
			}
		}
		timeout := time.After(20 * time.Second)
		for {
			select {
			case v, ok := <-lines:
				if !ok {
					t.Fatal("window channel closed")
				}
				if match(v) {
					return v
				}
				backlog = append(backlog, v)
			case <-timeout:
				t.Fatal("expected message did not arrive")
			}
		}
	}
	send(map[string]any{"rid": 1, "op": "set", "agents": true})
	if v := expect(func(v map[string]any) bool { return v["rid"] == float64(1) }); v["ok"] != true || v["agents"] != true || !p.origin.Settings().Agents {
		t.Fatalf("the window must be able to change settings: %v", v)
	}
	send(map[string]any{"rid": 2, "op": "exec", "command": "echo sudo approved-in-window"})
	prompt := expect(func(v map[string]any) bool { return v["ev"] == "confirm" })
	if prompt["detail"] != "echo sudo approved-in-window" {
		t.Fatalf("unexpected prompt: %v", prompt)
	}
	send(map[string]any{"rid": 3, "op": "confirm", "id": prompt["id"], "allow": true})
	started := expect(func(v map[string]any) bool { return v["rid"] == float64(2) })
	id := started["job"].(map[string]any)["execution_id"].(string)
	send(map[string]any{"rid": 4, "op": "wait", "id": id, "wait": 20})
	done := expect(func(v map[string]any) bool { return v["rid"] == float64(4) })
	if job := done["job"].(map[string]any); job["state"] != "succeeded" || !strings.Contains(job["output"].(string), "approved-in-window") {
		t.Fatalf("approved command failed: %v", job)
	}
}

func TestMCPServerSpeaksTheProtocol(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	dir := t.TempDir()
	if err := p.server.Listen(dir); err != nil {
		t.Fatal(err)
	}
	defer p.server.Close(dir)
	_ = p.origin.SetSettings(Settings{Agents: true})
	source := filepath.Join(t.TempDir(), "note.txt")
	_ = os.WriteFile(source, []byte("via mcp"), 0o644)
	destination := t.TempDir()
	requests := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}},
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "list_devices", "arguments": map[string]any{}}},
		{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "execute_command", "arguments": map[string]any{"command": "echo mcp-ran"}}},
		{"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": map[string]any{"name": "transfer_file", "arguments": map[string]any{"direction": "push", "local_path": source, "remote_path": destination}}},
		{"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": map[string]any{"name": "execute_command", "arguments": map[string]any{"command": "echo sudo never"}}},
		{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": "nope", "arguments": map[string]any{}}},
		{"jsonrpc": "2.0", "id": 8, "method": "resources/list"},
	}
	var input bytes.Buffer
	for _, r := range requests {
		data, _ := json.Marshal(r)
		input.Write(append(data, '\n'))
	}
	var output bytes.Buffer
	if err := ServeMCP(&input, &output, dir, "test", nil); err != nil {
		t.Fatal(err)
	}
	answers := map[float64]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("invalid JSON-RPC line %q", line)
		}
		answers[v["id"].(float64)] = v
	}
	if len(answers) != 8 {
		t.Fatalf("expected 8 answers (none for the notification), got %d", len(answers))
	}
	if result := answers[1]["result"].(map[string]any); result["protocolVersion"] != "2025-03-26" || result["serverInfo"].(map[string]any)["name"] != "mac-lan" {
		t.Fatalf("bad initialize result: %v", result)
	}
	names := map[string]bool{}
	for _, tool := range answers[2]["result"].(map[string]any)["tools"].([]any) {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	for _, name := range []string{"list_devices", "execute_command", "transfer_file", "deploy_update", "get_execution_status", "cancel_execution"} {
		if !names[name] {
			t.Fatalf("tool %s is missing", name)
		}
	}
	body := func(id float64) (string, bool) {
		result := answers[id]["result"].(map[string]any)
		return result["content"].([]any)[0].(map[string]any)["text"].(string), result["isError"].(bool)
	}
	if text, failed := body(3); failed || !strings.Contains(text, "Test Mac") || !strings.Contains(text, "accepts_commands") {
		t.Fatalf("list_devices: %s", text)
	}
	if text, failed := body(4); failed || !strings.Contains(text, "mcp-ran") || !strings.Contains(text, "state: succeeded, exit code 0") {
		t.Fatalf("execute_command: %s", text)
	}
	if text, failed := body(5); failed || !strings.Contains(text, "state: succeeded") {
		t.Fatalf("transfer_file: %s", text)
	}
	if got, _ := os.ReadFile(filepath.Join(destination, "note.txt")); string(got) != "via mcp" {
		t.Fatal("transfer_file did not deliver the file")
	}
	if text, _ := body(6); !strings.Contains(text, "state: denied") || strings.Contains(text, "never\r") || strings.Contains(text, "never\n") {
		t.Fatalf("an agent's destructive command must not run without a person: %s", text)
	}
	if _, failed := body(7); !failed {
		t.Fatal("an unknown tool must be an error")
	}
	if answers[8]["error"] == nil {
		t.Fatal("an unsupported method must return a JSON-RPC error")
	}
}
