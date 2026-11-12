package remote

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Job is a command, transfer or deployment this computer asked the paired one
// to perform. It is what the terminal, the shell and MCP tools report on.
type Job struct {
	mu       sync.Mutex
	id       string
	kind     string // exec, transfer, deploy
	command  string
	cwd      string
	origin   string // user or agent
	state    string // awaiting-confirmation, starting, running, succeeded, failed, cancelled, denied, timed-out, lost
	err      string
	exit     *int
	started  time.Time
	ended    time.Time
	out      ring
	remote   int64 // next output offset expected from the other computer
	done     int64
	total    int64
	detached bool
	changed  chan struct{}
	cancel   chan struct{}
	once     sync.Once
}

// JobView is the JSON form of a Job at one moment.
type JobView struct {
	ID          string `json:"execution_id"`
	Kind        string `json:"kind"`
	Command     string `json:"command,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	State       string `json:"state"`
	Finished    bool   `json:"finished"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	Error       string `json:"error,omitempty"`
	Started     string `json:"started"`
	Ended       string `json:"ended,omitempty"`
	Output      string `json:"output"`
	Errors      string `json:"stderr,omitempty"`
	NextOffset  int64  `json:"next_offset"`
	Truncated   bool   `json:"output_truncated,omitempty"`
	Reconnect   bool   `json:"reconnecting,omitempty"`
	BytesDone   int64  `json:"bytes_done,omitempty"`
	BytesTotal  int64  `json:"bytes_total,omitempty"`
	Explanation string `json:"note,omitempty"`
}

func finished(state string) bool {
	switch state {
	case "succeeded", "failed", "cancelled", "denied", "timed-out", "lost":
		return true
	}
	return false
}

func (j *Job) touch() {
	close(j.changed)
	j.changed = make(chan struct{})
}

func (j *Job) note(text string) {
	j.mu.Lock()
	j.out.add("i", text+"\n")
	j.touch()
	j.mu.Unlock()
}

func (j *Job) finish(state, err string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if finished(j.state) {
		return
	}
	j.state, j.err, j.ended, j.detached = state, err, time.Now(), false
	if err != "" {
		j.out.add("i", err+"\n")
	}
	j.touch()
}

func (j *Job) cancelled() bool {
	select {
	case <-j.cancel:
		return true
	default:
		return false
	}
}

// View is the job with all of its kept output.
func (j *Job) View() JobView { return j.view(0) }

func (j *Job) view(since int64) JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := JobView{ID: j.id, Kind: j.kind, Command: j.command, Cwd: j.cwd, State: j.state, Finished: finished(j.state), ExitCode: j.exit, Error: j.err,
		Started: j.started.UTC().Format(time.RFC3339), Reconnect: j.detached, BytesDone: j.done, BytesTotal: j.total}
	if !j.ended.IsZero() {
		v.Ended = j.ended.UTC().Format(time.RFC3339)
	}
	chunks, truncated, next := j.out.since(since)
	var all, errs strings.Builder
	for _, c := range chunks {
		all.WriteString(c.data)
		if c.stream == "e" {
			errs.WriteString(c.data)
		}
	}
	v.Output, v.Errors, v.NextOffset, v.Truncated = all.String(), errs.String(), next, truncated
	switch j.state {
	case "awaiting-confirmation":
		v.Explanation = "Waiting for a person to approve this in the mac-lan window on this computer."
	case "denied":
		v.Explanation = "Not approved. Do not retry without the user's agreement."
	}
	if j.detached {
		v.Explanation = "The connection dropped; the command keeps running on the other computer and output resumes when mac-lan reconnects."
	}
	return v
}

// ConfirmRequest describes an action a person must approve before it is sent.
type ConfirmRequest struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Reason string `json:"reason"`
	Origin string `json:"origin"`
	Target string `json:"target"`
	// Remember, when not empty, is the wording of a third choice that also
	// stops this kind of request from asking again (see Answer).
	Remember string `json:"remember,omitempty"`
	// Withdrawn closes when the request no longer needs an answer.
	Withdrawn <-chan struct{} `json:"-"`
}

// Answer is a person's reply to a ConfirmRequest.
type Answer int

const (
	Deny Answer = iota
	AllowOnce
	// AllowAlways is offered only when the request carries Remember.
	AllowAlways
)

// Device is one end of the link as shown to tools.
type Device struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Platform        string `json:"platform"`
	Version         string `json:"version,omitempty"`
	ThisDevice      bool   `json:"this_device"`
	OS              string `json:"os,omitempty"`
	Arch            string `json:"arch,omitempty"`
	User            string `json:"user,omitempty"`
	Home            string `json:"home_folder,omitempty"`
	Connected       bool   `json:"connected"`
	CommandsEnabled bool   `json:"accepts_commands"`
	DefaultShell    string `json:"default_shell,omitempty"`
	Note            string `json:"note,omitempty"`
}

// Node is this computer's end of the remote-command link.
type Node struct {
	name     string
	version  string
	dir      string
	settings *settingsStore
	log      *audit
	procs    *procTable

	mu       sync.Mutex
	link     *link
	jobs     map[string]*Job
	order    []string
	confirm  func(ConfirmRequest) Answer
	onChange func()
	// Commands a person chose not to be asked about again, until mac-lan restarts.
	remembered map[string]bool
	// learns makes this node remember the Mac's standby service (the PC's side).
	learns bool
}

// New creates the node. dir is the private settings folder ("" keeps settings
// in memory, for tests).
func New(dir, name, version string) *Node {
	n := &Node{name: name, version: version, dir: dir, settings: loadSettings(dir), procs: newProcTable(), jobs: map[string]*Job{}, remembered: map[string]bool{}, learns: runtime.GOOS == "windows"}
	if dir != "" {
		n.log = &audit{path: filepath.Join(dir, "remote-audit.log")}
	}
	return n
}

// SetConfirm installs the function that asks a person to approve an action.
// Without one, every action that needs approval is refused.
func (n *Node) SetConfirm(ask func(ConfirmRequest) Answer) {
	n.mu.Lock()
	n.confirm = ask
	n.mu.Unlock()
}

// OnChange installs a callback for connection and activity changes.
func (n *Node) OnChange(f func()) {
	n.mu.Lock()
	n.onChange = f
	n.mu.Unlock()
}

func (n *Node) changed() {
	n.mu.Lock()
	f := n.onChange
	n.mu.Unlock()
	if f != nil {
		f()
	}
}

func (n *Node) Settings() Settings { return n.settings.get() }

// SetSettings saves the opt-in switches and tells the paired computer.
func (n *Node) SetSettings(next Settings) error {
	if len(next.Trusted) > maxTrusted {
		return errors.New("too many folders")
	}
	if err := n.settings.set(next); err != nil {
		return err
	}
	n.log.record("settings", "user", "accept="+boolText(next.Accept)+" agents="+boolText(next.Agents)+" trusted="+strings.Join(next.Trusted, " | "), "saved")
	if l := n.current(); l != nil {
		_ = l.send(n.hello())
	}
	n.changed()
	return nil
}

func boolText(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (n *Node) Close() {
	if l := n.current(); l != nil {
		l.close()
	}
	n.procs.close()
}

// Busy reports work in progress in either direction; updates wait for it.
func (n *Node) Busy() bool {
	if n.procs.running() > 0 {
		return true
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, j := range n.jobs {
		j.mu.Lock()
		active := !finished(j.state)
		j.mu.Unlock()
		if active {
			return true
		}
	}
	return false
}

func (n *Node) current() *link {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.link
}

func platformID(goos string) string {
	if goos == "darwin" {
		return "mac"
	}
	return goos
}

func (n *Node) hello() msg {
	home, _ := os.UserHomeDir()
	return msg{T: "hello", Name: n.name, Platform: runtime.GOOS, Version: n.version, Accept: n.settings.get().Accept, Shell: defaultShellName(),
		Home: home, User: userName(), OS: systemName(), Arch: runtime.GOARCH}
}

// Devices lists this computer and the paired one.
func (n *Node) Devices() []Device {
	settings := n.settings.get()
	home, _ := os.UserHomeDir()
	local := Device{ID: platformID(runtime.GOOS), Name: n.name, Platform: runtime.GOOS, Version: n.version, ThisDevice: true, Connected: true, CommandsEnabled: settings.Accept, DefaultShell: defaultShellName(),
		OS: systemName(), Arch: runtime.GOARCH, User: userName(), Home: home,
		Note: "The computer this tool runs on. Tools act on the other device."}
	l := n.current()
	if l == nil {
		return []Device{local, {ID: "remote", Name: "Paired computer", Connected: false, Note: "Not connected. Open mac-lan on both computers and connect them; the link returns by itself after a drop."}}
	}
	peer, known := l.peerInfo()
	if !known {
		return []Device{local, {ID: "remote", Name: "Paired computer", Connected: true, Note: "Connected; waiting for the other computer to introduce itself."}}
	}
	other := Device{ID: platformID(peer.Platform), Name: peer.Name, Platform: peer.Platform, Version: peer.Version, Connected: true, CommandsEnabled: peer.Accept, DefaultShell: peer.Shell,
		OS: peer.OS, Arch: peer.Arch, User: peer.User, Home: peer.Home}
	if !peer.Accept {
		other.Note = "Remote commands are switched off on that computer. Its user must turn on \"Allow the paired computer to run commands here\" in mac-lan."
	} else if l.standby {
		other.Note = "Connected through the Mac's standby link: mac-lan on the Mac is restarting, updating or not sharing. Commands and files work; a screenshot may be refused."
	}
	return []Device{local, other}
}

// target checks the optional device name a tool passed and returns the link.
func (n *Node) target(device string) (*link, error) {
	l := n.current()
	if l == nil {
		return nil, errors.New("the other computer is not connected. Open mac-lan on both computers; it reconnects by itself")
	}
	peer, known := l.peerInfo()
	if !known {
		return nil, errors.New("the other computer has not finished connecting; try again in a moment")
	}
	if device != "" && device != "remote" && !strings.EqualFold(device, platformID(peer.Platform)) && !strings.EqualFold(device, peer.Name) {
		if strings.EqualFold(device, platformID(runtime.GOOS)) || strings.EqualFold(device, n.name) || device == "local" {
			return nil, errors.New("that is this computer; these tools act on the other device (" + platformID(peer.Platform) + ")")
		}
		return nil, errors.New("unknown device; the connected device is \"" + platformID(peer.Platform) + "\"")
	}
	if !peer.Accept {
		return nil, errors.New("remote commands are switched off on " + peer.Name + ". Its user must turn on \"Allow the paired computer to run commands here\" in mac-lan")
	}
	return l, nil
}

func (n *Node) newJob(kind, command, cwd, origin, state string) *Job {
	j := &Job{id: newID(), kind: kind, command: command, cwd: cwd, origin: origin, state: state, started: time.Now(), changed: make(chan struct{}), cancel: make(chan struct{})}
	n.mu.Lock()
	n.jobs[j.id] = j
	n.order = append(n.order, j.id)
	for len(n.order) > maxJobsKept {
		delete(n.jobs, n.order[0])
		n.order = n.order[1:]
	}
	n.mu.Unlock()
	n.changed()
	return j
}

func (n *Node) job(id string) *Job {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.jobs[id]
}

// approve asks a person. It returns Deny when nobody can be asked or they
// decline, and AllowAlways only for a request that offered it.
func (n *Node) approve(j *Job, request ConfirmRequest) Answer {
	n.mu.Lock()
	ask := n.confirm
	n.mu.Unlock()
	if ask == nil {
		j.finish("denied", "This needs approval in the mac-lan window, and no mac-lan window is available to ask. Nothing was sent.")
		return Deny
	}
	withdrawn := make(chan struct{})
	defer close(withdrawn) // closes a prompt that is still open when this returns
	request.Withdrawn = withdrawn
	answer := make(chan Answer, 1)
	go func() { answer <- ask(request) }()
	timer := time.NewTimer(confirmTimeout)
	defer timer.Stop()
	select {
	case reply := <-answer:
		if reply == AllowAlways && request.Remember == "" {
			reply = AllowOnce
		}
		if reply != AllowOnce && reply != AllowAlways {
			j.finish("denied", "Not approved in mac-lan. Nothing was sent.")
			return Deny
		}
		return reply
	case <-j.cancel:
		j.finish("cancelled", "Cancelled before it was approved.")
	case <-timer.C:
		j.finish("denied", "Nobody approved this in mac-lan within a minute. Nothing was sent.")
	}
	return Deny
}

// ExecRequest asks the paired computer to run a shell command.
type ExecRequest struct {
	Device      string
	Command     string
	Cwd         string
	Shell       string
	Timeout     int // seconds; 0 uses the default
	Interactive bool
	Origin      string
}

// Exec starts a command on the paired computer and returns its job at once.
func (n *Node) Exec(r ExecRequest) (*Job, error) {
	if strings.TrimSpace(r.Command) == "" || !validText(r.Command, 32<<10) || !validText(r.Cwd, 4096) || r.Timeout < 0 {
		return nil, errors.New("enter a command to run")
	}
	l, err := n.target(r.Device)
	if err != nil {
		return nil, err
	}
	peer, _ := l.peerInfo()
	reason := NeedsConfirmation(r.Command)
	// A person may have chosen not to be asked again about this exact command
	// in this folder; that lasts until mac-lan restarts.
	memory := peer.Platform + "\x00" + r.Shell + "\x00" + r.Cwd + "\x00" + r.Command
	n.mu.Lock()
	ask := reason != "" && !n.remembered[memory]
	n.mu.Unlock()
	state := "starting"
	if ask {
		state = "awaiting-confirmation"
	}
	j := n.newJob("exec", r.Command, r.Cwd, r.Origin, state)
	go func() {
		defer n.changed()
		if ask {
			detail := r.Command
			if r.Cwd != "" {
				detail += "\n\nin " + r.Cwd
			}
			reply := n.approve(j, ConfirmRequest{Title: "Run this on " + peer.Name + "?", Detail: detail, Reason: "This command " + reason + ".", Origin: r.Origin, Target: peer.Name,
				Remember: "Don't ask again for this exact command in this folder until mac-lan restarts"})
			if reply == Deny {
				return
			}
			if reply == AllowAlways {
				n.mu.Lock()
				if len(n.remembered) < 200 {
					n.remembered[memory] = true
				}
				n.mu.Unlock()
				n.log.record("remember", r.Origin, r.Command, "not asked again until restart")
			}
			j.mu.Lock()
			j.state = "starting"
			j.touch()
			j.mu.Unlock()
		}
		n.startRemote(l, j, msg{T: "exec", ID: j.id, Command: r.Command, Cwd: r.Cwd, Shell: r.Shell, Timeout: r.Timeout, Interactive: r.Interactive, Origin: r.Origin, Confirmed: reason != ""})
	}()
	return j, nil
}

// startRemote sends an exec request and moves the job to running or failed.
func (n *Node) startRemote(l *link, j *Job, request msg) {
	if j.cancelled() {
		j.finish("cancelled", "Cancelled before it started.")
		return
	}
	reply, err := l.call(request, 30*time.Second)
	if err != nil {
		j.finish("failed", "Could not reach the other computer: "+err.Error())
		return
	}
	if !reply.OK {
		j.finish("failed", reply.Err)
		return
	}
	j.mu.Lock()
	if !finished(j.state) {
		j.state = "running"
		j.touch()
	}
	j.mu.Unlock()
	if j.cancelled() {
		_ = l.send(msg{T: "cancel", ID: j.id})
	}
}

// Status returns a job with its output from offset since. With wait > 0 it
// returns as soon as there is new output or the job ends, or after wait.
func (n *Node) Status(id string, since int64, wait time.Duration) (JobView, error) {
	j := n.job(id)
	if j == nil {
		return JobView{}, errors.New("unknown execution id")
	}
	if since < 0 {
		since = 0
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		j.mu.Lock()
		ready := finished(j.state) || j.out.next > since
		changed := j.changed
		j.mu.Unlock()
		if ready || wait <= 0 {
			return j.view(since), nil
		}
		select {
		case <-changed:
		case <-deadline.C:
			return j.view(since), nil
		}
	}
}

// WaitDone blocks until the job ends or wait passes, then returns it whole.
func (n *Node) WaitDone(id string, wait time.Duration) (JobView, error) {
	j := n.job(id)
	if j == nil {
		return JobView{}, errors.New("unknown execution id")
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		j.mu.Lock()
		done := finished(j.state)
		changed := j.changed
		j.mu.Unlock()
		if done {
			return j.view(0), nil
		}
		select {
		case <-changed:
		case <-deadline.C:
			return j.view(0), nil
		}
	}
}

// Cancel stops a job; a running command is ended on the other computer.
func (n *Node) Cancel(id string) error {
	j := n.job(id)
	if j == nil {
		return errors.New("unknown execution id")
	}
	j.once.Do(func() { close(j.cancel) })
	j.mu.Lock()
	running := j.state == "running" && j.kind != "transfer"
	j.mu.Unlock()
	if running {
		if l := n.current(); l != nil {
			return l.send(msg{T: "cancel", ID: id})
		}
		j.finish("cancelled", "Cancelled while disconnected; the other computer ends the command when it is not reattached.")
	}
	return nil
}

// Input sends text to the standard input of an interactive command.
func (n *Node) Input(id, data string) error {
	j := n.job(id)
	l := n.current()
	if j == nil || l == nil || !validText(data, 64<<10) {
		return errors.New("that command is not running")
	}
	return l.send(msg{T: "stdin", ID: id, D: data})
}

// Resolve asks the other computer for the absolute form of a folder path; the
// terminal uses it to follow "cd".
func (n *Node) Resolve(path, cwd string) (string, error) {
	l, err := n.target("")
	if err != nil {
		return "", err
	}
	reply, err := l.call(msg{T: "resolve", Path: path, Cwd: cwd}, 15*time.Second)
	if err != nil {
		return "", err
	}
	if !reply.OK {
		return "", errors.New(reply.Err)
	}
	return reply.Path, nil
}

// Jobs lists recent jobs, newest first, without their output.
func (n *Node) Jobs(limit int) []JobView {
	n.mu.Lock()
	ids := append([]string(nil), n.order...)
	n.mu.Unlock()
	var out []JobView
	for i := len(ids) - 1; i >= 0 && len(out) < limit; i-- {
		if j := n.job(ids[i]); j != nil {
			v := j.view(1 << 62)
			v.Output, v.Errors = "", ""
			out = append(out, v)
		}
	}
	return out
}

// output and exit arrive from the other computer for a job this one started.
func (n *Node) output(m msg) {
	j := n.job(m.ID)
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if m.Off < j.remote || finished(j.state) {
		return // replayed after a reconnect
	}
	j.remote = m.Off + int64(len(m.D))
	stream := "o"
	if m.S == "e" {
		stream = "e"
	}
	j.out.add(stream, m.D)
	j.touch()
}

func (n *Node) exited(m msg) {
	j := n.job(m.ID)
	if j == nil {
		return
	}
	state := m.State
	if !finished(state) {
		state = "failed"
	}
	j.mu.Lock()
	j.exit = m.Exit
	j.mu.Unlock()
	j.finish(state, m.Err)
	n.changed()
}

// linkLost marks running commands as waiting for a reconnect. If the link does
// not return in time, the other computer has ended them and they are lost.
func (n *Node) linkLost() {
	n.mu.Lock()
	jobs := make([]*Job, 0, len(n.jobs))
	for _, j := range n.jobs {
		jobs = append(jobs, j)
	}
	n.mu.Unlock()
	for _, j := range jobs {
		j.mu.Lock()
		waiting := j.state == "running" && j.kind != "transfer" && !j.detached
		if waiting {
			j.detached = true
			j.touch()
		}
		j.mu.Unlock()
		if !waiting {
			continue
		}
		go func(j *Job) {
			timer := time.NewTimer(detachGrace + 15*time.Second)
			defer timer.Stop()
			for {
				j.mu.Lock()
				still, changed := j.detached && !finished(j.state), j.changed
				j.mu.Unlock()
				if !still {
					return
				}
				select {
				case <-changed:
				case <-timer.C:
					j.finish("lost", "The connection did not return in time. The other computer stops a command nobody is attached to.")
					n.changed()
					return
				}
			}
		}(j)
	}
}

// reattach asks the other computer to resume output for commands that were
// running when the previous connection dropped.
func (n *Node) reattach(l *link) {
	n.mu.Lock()
	jobs := make([]*Job, 0, len(n.jobs))
	for _, j := range n.jobs {
		jobs = append(jobs, j)
	}
	n.mu.Unlock()
	for _, j := range jobs {
		j.mu.Lock()
		resume := j.detached && !finished(j.state)
		off := j.remote
		if resume {
			j.detached = false
			j.touch()
		}
		j.mu.Unlock()
		if resume {
			_ = l.send(msg{T: "attach", ID: j.id, Off: off})
		}
	}
}

// Attach runs the link on an established, already authenticated WebSocket until
// it closes. A newer connection replaces an older one.
func (n *Node) Attach(c *websocket.Conn) { n.attach(c, false) }

func (n *Node) attach(c *websocket.Conn, standby bool) {
	if old := n.current(); old != nil {
		// Finish the old connection completely first, so commands that were
		// attached to it are picked up again by the new one.
		old.close()
		n.dropped(old)
	}
	l := newLink(n, c)
	l.standby = standby
	n.mu.Lock()
	n.link = l
	n.mu.Unlock()
	_ = l.send(n.hello())
	n.reattach(l)
	n.changed()
	l.run()
	n.dropped(l)
}

// dropped records, once per connection, that it is gone.
func (n *Node) dropped(l *link) {
	l.dropOnce.Do(func() {
		n.mu.Lock()
		if n.link == l {
			n.link = nil
		}
		n.mu.Unlock()
		l.abandon()
		n.procs.detachAll(time.Now())
		n.linkLost()
		n.changed()
	})
}
