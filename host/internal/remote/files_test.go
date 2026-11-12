package remote

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The headless suite never looks at the screen; set this to test a real capture.
const captureSwitch = "MACLAN_TEST_CAPTURE"

func TestRemotePathsAndTrustableFolders(t *testing.T) {
	mac := msg{Platform: "darwin", Home: "/Users/sam"}
	pc := msg{Platform: "windows", Home: `C:\Users\Sam`}
	clean := []struct {
		peer     msg
		in, want string
	}{
		{mac, "~/Projects/app", "/Users/sam/Projects/app"}, {mac, "Projects/app/", "/Users/sam/Projects/app"}, {mac, "/tmp/x/../y", "/tmp/y"}, {mac, "~", "/Users/sam"},
		{pc, `~\source\app`, "C:/Users/Sam/source/app"}, {pc, `D:\work\app\..\lib`, "D:/work/lib"}, {pc, "source/app", "C:/Users/Sam/source/app"},
		{msg{Platform: "darwin"}, "~/x", ""},
	}
	for _, c := range clean {
		if got := remoteClean(c.peer, c.in); got != c.want {
			t.Errorf("remoteClean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, folder := range []string{"~/Projects/app", "/tmp/build", "/Users/sam/x"} {
		if !trustable(mac, folder) {
			t.Errorf("%q should be trustable", folder)
		}
	}
	for _, folder := range []string{"~", "/", "/Users", "/Users/sam/", "/tmp", "", "/Users/sam/../sam"} {
		if trustable(mac, folder) {
			t.Errorf("%q is too broad to trust", folder)
		}
	}
	for folder, want := range map[string]bool{`C:\`: false, `C:\Users`: false, `C:\Users\Sam`: false, `D:\work`: false, `D:\work\app`: true, `~\source\app`: true} {
		if trustable(pc, folder) != want {
			t.Errorf("trustable(%q) != %v", folder, want)
		}
	}
}

func TestAgentsReadWriteAndEditFiles(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	remote := t.TempDir()
	file := filepath.Join(remote, "src", "main.txt")
	j, err := p.origin.WriteFile("", file, []byte("one\ntwo\nthree\n"), false, "agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if view := finish(t, p.origin, j, 20*time.Second); view.State != "succeeded" {
		t.Fatalf("a new file needs no approval: %+v", view)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(file, 0o755)
	}
	data, err := p.origin.ReadFile("", file, "agent")
	if err != nil || string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("read back %q, %v", data, err)
	}
	// Replacing needs a person: nobody is there to ask, so it is denied.
	j, _ = p.origin.WriteFile("", file, []byte("gone"), true, "agent", "")
	if view := finish(t, p.origin, j, 20*time.Second); view.State != "denied" {
		t.Fatalf("replacing a file must need approval: %+v", view)
	}
	j, _ = p.origin.WriteFile("", file, []byte("gone"), false, "agent", "")
	if view := finish(t, p.origin, j, 20*time.Second); view.State != "failed" || !strings.Contains(view.Error, "already exists") {
		t.Fatalf("an existing file must not be replaced silently: %+v", view)
	}
	// "Always" on the prompt trusts the folder; later edits in it do not ask.
	var prompts []ConfirmRequest
	p.origin.SetConfirm(func(r ConfirmRequest) Answer { prompts = append(prompts, r); return AllowAlways })
	j, count, err := p.origin.EditFile("", file, "two", "TWO", false, "agent")
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if view := finish(t, p.origin, j, 20*time.Second); view.State != "succeeded" {
		t.Fatalf("approved edit failed: %+v", view)
	}
	if len(prompts) != 1 || !strings.Contains(prompts[0].Remember, "Always allow replacing files in ") {
		t.Fatalf("the prompt must offer to trust the folder: %+v", prompts)
	}
	if trusted := p.origin.Settings().Trusted; len(trusted) != 1 || !strings.EqualFold(filepath.ToSlash(trusted[0]), filepath.ToSlash(filepath.Dir(file))) {
		t.Fatalf("trusted folders: %v", trusted)
	}
	j, count, err = p.origin.EditFile("", file, "e", "E", true, "agent")
	if err != nil || count != 3 {
		t.Fatal(count, err)
	}
	if view := finish(t, p.origin, j, 20*time.Second); view.State != "succeeded" || len(prompts) != 1 {
		t.Fatalf("an edit in a trusted folder must not ask again: %+v, %d prompts", view, len(prompts))
	}
	if got, _ := os.ReadFile(file); string(got) != "onE\nTWO\nthrEE\n" {
		t.Fatalf("file after edits: %q", got)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(file); info.Mode().Perm() != 0o755 {
			t.Fatalf("an edited file must keep its permissions, got %v", info.Mode().Perm())
		}
	}
	// Outside the trusted folder the question comes back.
	other := filepath.Join(remote, "other.txt")
	_ = os.WriteFile(other, []byte("x"), 0o644)
	p.origin.SetConfirm(func(r ConfirmRequest) Answer { prompts = append(prompts, r); return Deny })
	j, _ = p.origin.WriteFile("", other, []byte("y"), true, "agent", "")
	if view := finish(t, p.origin, j, 20*time.Second); view.State != "denied" || len(prompts) != 2 {
		t.Fatalf("trust must not reach beyond its folder: %+v", view)
	}
	if err = p.origin.Untrust(p.origin.Settings().Trusted[0]); err != nil || len(p.origin.Settings().Trusted) != 0 {
		t.Fatal("untrust failed")
	}
	if p.origin.Trust(filepath.Dir(remote)) == nil && filepath.Dir(remote) == filepath.VolumeName(remote)+string(filepath.Separator) {
		t.Fatal("a disk root must not be trusted")
	}
	home, _ := os.UserHomeDir()
	if p.origin.Trust(home) == nil || p.origin.Trust("~") == nil {
		t.Fatal("the home folder must not be trusted as a whole")
	}
	// Edits that cannot be applied exactly are refused before anything is asked.
	for _, bad := range [][2]string{{"missing", "x"}, {"E", "x"}, {"", "x"}, {"onE", "onE"}} {
		if _, _, err = p.origin.EditFile("", file, bad[0], bad[1], false, "agent"); err == nil {
			t.Fatalf("edit %q must be refused", bad[0])
		}
	}
	// Windows line endings in the file match plain ones in the request.
	crlf := filepath.Join(remote, "src", "crlf.txt")
	_ = os.WriteFile(crlf, []byte("a\r\nb\r\nc\r\n"), 0o644)
	_ = p.origin.Trust(filepath.Dir(crlf))
	j, _, err = p.origin.EditFile("", crlf, "a\nb", "a\nB\nb2", false, "agent")
	if err != nil {
		t.Fatal(err)
	}
	finish(t, p.origin, j, 20*time.Second)
	if got, _ := os.ReadFile(crlf); string(got) != "a\r\nB\r\nb2\r\nc\r\n" {
		t.Fatalf("line endings were not kept: %q", got)
	}
	// Reading refuses folders, missing files and binary data clearly.
	if _, err = p.origin.ReadFile("", remote, "agent"); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("reading a folder: %v", err)
	}
	if _, err = p.origin.ReadFile("", filepath.Join(remote, "nope"), "agent"); err == nil {
		t.Fatal("reading a missing file must fail")
	}
	path, entries, more, err := p.origin.ListDir("", remote, "agent")
	if err != nil || more || len(entries) != 2 || !strings.EqualFold(path, remote) {
		t.Fatalf("list: %v %v %v", path, entries, err)
	}
	if entries[0].Name != "other.txt" || entries[0].Folder || entries[0].Size != 1 || entries[1].Name != "src" || !entries[1].Folder || entries[1].Modified == "" {
		t.Fatalf("entries: %+v", entries)
	}
	if _, _, _, err = p.origin.ListDir("", file, "agent"); err == nil {
		t.Fatal("listing a file must fail")
	}
}

func TestRememberedCommandIsNotAskedAgain(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	asked := 0
	p.origin.SetConfirm(func(r ConfirmRequest) Answer {
		asked++
		if r.Remember == "" {
			t.Error("a command prompt must offer not to ask again")
		}
		return AllowAlways
	})
	for i := 0; i < 2; i++ {
		j, err := p.origin.Exec(ExecRequest{Command: "echo sudo remembered", Origin: "agent"})
		if err != nil {
			t.Fatal(err)
		}
		if view := finish(t, p.origin, j, 20*time.Second); view.State != "succeeded" {
			t.Fatalf("run %d: %+v", i, view)
		}
	}
	j, _ := p.origin.Exec(ExecRequest{Command: "echo sudo different", Origin: "agent"})
	finish(t, p.origin, j, 20*time.Second)
	if asked != 2 {
		t.Fatalf("the same command must be asked once and a different one again; asked %d times", asked)
	}
	// Deployments never offer it.
	p.origin.SetConfirm(func(r ConfirmRequest) Answer {
		if r.Remember != "" {
			t.Error("a deployment must not offer to stop asking")
		}
		return AllowAlways
	})
	source := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(source, []byte("a"), 0o644)
	j, _ = p.origin.Deploy(DeployRequest{Source: source, Destination: t.TempDir(), Origin: "agent"})
	if view := finish(t, p.origin, j, 20*time.Second); view.State != "succeeded" {
		t.Fatalf("deploy: %+v", view)
	}
}

func TestWithdrawnPromptIsClosed(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	closed := make(chan struct{})
	p.origin.SetConfirm(func(r ConfirmRequest) Answer {
		<-r.Withdrawn
		close(closed)
		return Deny
	})
	j, _ := p.origin.Exec(ExecRequest{Command: "echo sudo waits", Origin: "agent"})
	time.Sleep(100 * time.Millisecond)
	_ = p.origin.Cancel(j.id)
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling must withdraw the open prompt")
	}
	if view := finish(t, p.origin, j, 10*time.Second); view.State != "cancelled" {
		t.Fatalf("%+v", view)
	}
}

func TestOutputForModelsIsBounded(t *testing.T) {
	long := strings.Repeat("0123456789abcde\n", 20000) // 320 KB
	short := shorten(long)
	if len(short) > mcpOutputHead+mcpOutputTail+200 || !strings.HasPrefix(short, "0123") || !strings.HasSuffix(short, "abcde\n") || !strings.Contains(short, "bytes of output left out") {
		t.Fatalf("shorten kept %d bytes", len(short))
	}
	if shorten("small") != "small" {
		t.Fatal("short output must pass unchanged")
	}
	euro := strings.Repeat("€", 40000)
	if cut := shorten(euro); strings.ContainsRune(cut, '�') || strings.ToValidUTF8(cut, "") != cut {
		t.Fatal("shorten must not split a character")
	}
	text := fileText("f.txt", "a\nb\nc\nd", 2, 2)
	if text != "[f.txt: lines 2-3 of 4; continue with start_line 4]\nb\nc\n" {
		t.Fatalf("%q", text)
	}
	if text = fileText("f.txt", "a\nb\n", 1, 2000); text != "[f.txt: lines 1-2 of 2]\na\nb\n" {
		t.Fatalf("%q", text)
	}
	if text = fileText("f.txt", "a\n", 5, 1); !strings.Contains(text, "past the end") {
		t.Fatalf("%q", text)
	}
	if text = fileText("f.txt", "", 1, 1); !strings.Contains(text, "empty file") {
		t.Fatalf("%q", text)
	}
	big := fileText("f.txt", strings.Repeat(strings.Repeat("x", 99)+"\n", 5000), 1, 5000)
	if len(big) > mcpFileBytes+200 || !strings.Contains(big, "continue with start_line") {
		t.Fatalf("a large file must be returned in parts, got %d bytes", len(big))
	}
	job := jobText(map[string]any{"state": "failed", "finished": true, "exit_code": float64(2), "output": "boom <&>\n", "execution_id": "x"})
	if job != "state: failed, exit code 2\n--- output ---\nboom <&>\n" {
		t.Fatalf("%q", job)
	}
	job = jobText(map[string]any{"state": "running", "finished": false, "output": "", "execution_id": "abc", "next_offset": float64(7)})
	if !strings.Contains(job, "execution_id: abc") || !strings.Contains(job, "next_offset: 7") {
		t.Fatalf("%q", job)
	}
}

func TestShrinkAndScreenshotOverTheLink(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for i := 0; i < len(source.Pix); i += 4 {
		source.Pix[i], source.Pix[i+1], source.Pix[i+2], source.Pix[i+3] = 200, 100, 50, 255
	}
	small := shrink(source, 100)
	if small.Bounds().Dx() != 100 || small.Bounds().Dy() != 50 || small.Pix[0] != 200 || small.Pix[1] != 100 || small.Pix[2] != 50 || small.Pix[len(small.Pix)-1] != 255 {
		t.Fatalf("shrink: %v %v", small.Bounds(), small.Pix[:4])
	}
	if shrink(source, 800) != source {
		t.Fatal("a picture is never enlarged")
	}
	if shotWidth(0) != 1600 || shotWidth(10) != 320 || shotWidth(99999) != 3840 || shotWidth(1280) != 1280 {
		t.Fatal("width limits")
	}
	p := newPair(t)
	p.link(t)
	if _, _, _, err := p.origin.Screenshot("", 0, "agent"); err == nil || !strings.Contains(err.Error(), "switched off") {
		t.Fatalf("a screenshot needs the target's switch: %v", err)
	}
	if os.Getenv(captureSwitch) == "" {
		t.Skip("set " + captureSwitch + "=1 to capture this computer's screen in the test")
	}
	allow(t, p.target)
	time.Sleep(200 * time.Millisecond)
	data, width, height, err := p.origin.Screenshot("", 640, "agent")
	if err != nil {
		// A test runner without a desktop cannot capture; the refusal must be clear.
		if !strings.Contains(err.Error(), "picture") && !strings.Contains(err.Error(), "screenshots are not available") {
			t.Fatalf("unexpected screenshot error: %v", err)
		}
		t.Skip("no desktop to capture here: " + err.Error())
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != width || config.Height != height || width > 640 || width < 16 {
		t.Fatalf("screenshot %dx%d: %v", width, height, err)
	}
}

func TestMCPFileAndScreenTools(t *testing.T) {
	p := newPair(t)
	allow(t, p.target)
	p.link(t)
	dir := t.TempDir()
	if err := p.server.Listen(dir); err != nil {
		t.Fatal(err)
	}
	defer p.server.Close(dir)
	_ = p.origin.SetSettings(Settings{Agents: true})
	remote := t.TempDir()
	file := filepath.Join(remote, "notes.txt")
	call := func(id int, name string, args map[string]any) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}
	}
	// One server per step, because the tools run concurrently within a session.
	step := func(request map[string]any) (string, bool, []any) {
		t.Helper()
		data, _ := json.Marshal(request)
		var output bytes.Buffer
		if err := ServeMCP(bytes.NewReader(append(data, '\n')), &output, dir, "test"); err != nil {
			t.Fatal(err)
		}
		var answer map[string]any
		if err := json.Unmarshal(output.Bytes(), &answer); err != nil {
			t.Fatalf("invalid answer %q", output.String())
		}
		result := answer["result"].(map[string]any)
		content := result["content"].([]any)
		text := ""
		for _, part := range content {
			if s, ok := part.(map[string]any)["text"].(string); ok {
				text += s
			}
		}
		return text, result["isError"].(bool), content
	}
	if text, failed, _ := step(call(1, "write_file", map[string]any{"path": file, "content": "alpha\nbeta <tag> & \"quotes\"\n"})); failed || !strings.Contains(text, "state: succeeded") {
		t.Fatalf("write_file: %s", text)
	}
	if text, failed, _ := step(call(2, "read_file", map[string]any{"path": file})); failed || !strings.HasSuffix(text, "lines 1-2 of 2]\nalpha\nbeta <tag> & \"quotes\"\n") {
		t.Fatalf("read_file: %q", text)
	}
	if text, failed, _ := step(call(3, "read_file", map[string]any{"path": file, "start_line": 2, "max_lines": 1})); failed || !strings.HasSuffix(text, "lines 2-2 of 2]\nbeta <tag> & \"quotes\"\n") {
		t.Fatalf("read_file part: %q", text)
	}
	if text, _, _ := step(call(4, "edit_file", map[string]any{"path": file, "old_text": "alpha", "new_text": "ALPHA"})); !strings.Contains(text, "state: denied") {
		t.Fatalf("an agent's edit must wait for a person: %s", text)
	}
	p.origin.SetConfirm(func(ConfirmRequest) Answer { return AllowOnce })
	if text, failed, _ := step(call(5, "edit_file", map[string]any{"path": file, "old_text": "alpha", "new_text": "ALPHA"})); failed || !strings.Contains(text, "state: succeeded") {
		t.Fatalf("edit_file: %s", text)
	}
	if got, _ := os.ReadFile(file); string(got) != "ALPHA\nbeta <tag> & \"quotes\"\n" {
		t.Fatalf("after edit: %q", got)
	}
	if text, failed, _ := step(call(6, "edit_file", map[string]any{"path": file, "old_text": "nothing like this", "new_text": "x"})); !failed || !strings.Contains(text, "not found") {
		t.Fatalf("a failed edit must say why: %s", text)
	}
	if text, failed, _ := step(call(7, "list_directory", map[string]any{"path": remote})); failed || !strings.Contains(text, "1 entries") || !strings.Contains(text, "notes.txt\t") {
		t.Fatalf("list_directory: %q", text)
	}
	_ = os.WriteFile(filepath.Join(remote, "blob.bin"), []byte{0, 1, 2, 0xff}, 0o644)
	if text, failed, _ := step(call(8, "read_file", map[string]any{"path": filepath.Join(remote, "blob.bin")})); !failed || !strings.Contains(text, "not a text file") {
		t.Fatalf("binary read: %s", text)
	}
	// An interactive command answers what send_input writes to it.
	reader := "set /p line= & call echo got %line%"
	if runtime.GOOS != "windows" {
		reader = "read line; echo got $line"
	}
	text, _, _ := step(call(9, "execute_command", map[string]any{"command": reader, "interactive": true, "wait_seconds": 0}))
	at := strings.Index(text, "execution_id: ")
	if at < 0 {
		t.Fatalf("interactive start: %s", text)
	}
	id := strings.TrimSpace(strings.SplitN(text[at+len("execution_id: "):], "\n", 2)[0])
	if _, failed, _ := step(call(10, "send_input", map[string]any{"execution_id": id, "text": "typed\n"})); failed {
		t.Fatal("send_input failed")
	}
	if text, _, _ = step(call(11, "get_execution_status", map[string]any{"execution_id": id, "wait_seconds": 15})); !strings.Contains(text, "got typed") {
		view, _ := p.origin.WaitDone(id, 10*time.Second)
		if !strings.Contains(view.Output, "got typed") {
			t.Fatalf("interactive command did not receive input: %s / %+v", text, view)
		}
	}
	if os.Getenv(captureSwitch) == "" {
		return
	}
	text, failed, content := step(call(12, "take_screenshot", map[string]any{"max_width": 480}))
	if failed {
		t.Log("no desktop to capture here: " + text)
		return
	}
	picture := content[0].(map[string]any)
	raw, err := base64.StdEncoding.DecodeString(picture["data"].(string))
	if picture["type"] != "image" || picture["mimeType"] != "image/jpeg" || err != nil {
		t.Fatalf("take_screenshot content: %v", picture["type"])
	}
	if config, err := jpeg.DecodeConfig(bytes.NewReader(raw)); err != nil || config.Width > 480 {
		t.Fatalf("take_screenshot picture: %v %v", config, err)
	}
}
