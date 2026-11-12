package remote

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// proc is a command running on this computer for the paired one. It outlives a
// dropped connection for detachGrace so a reconnect can pick its output up.
type proc struct {
	id      string
	command string
	mu      sync.Mutex
	out     ring
	state   string // running, succeeded, failed, cancelled, timed-out
	exit    *int
	err     string
	sink    func(msg) error // the connection currently receiving output
	detach  time.Time       // when sink was lost; zero while attached
	ended   time.Time
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stop    chan string // receives the final state when cancelled or timed out
	done    chan struct{}
}

type procTable struct {
	mu    sync.Mutex
	procs map[string]*proc
	quit  chan struct{}
	once  sync.Once
}

func newProcTable() *procTable {
	t := &procTable{procs: map[string]*proc{}, quit: make(chan struct{})}
	go t.reap()
	return t
}

func (t *procTable) get(id string) *proc {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.procs[id]
}

func (t *procTable) running() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, p := range t.procs {
		select {
		case <-p.done:
		default:
			n++
		}
	}
	return n
}

// reap ends commands whose connection never came back and forgets old results.
func (t *procTable) reap() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.quit:
			return
		case now := <-ticker.C:
			t.mu.Lock()
			for id, p := range t.procs {
				p.mu.Lock()
				finished := !p.ended.IsZero()
				abandoned := !finished && !p.detach.IsZero() && now.Sub(p.detach) > detachGrace
				expired := finished && now.Sub(p.ended) > 10*time.Minute
				p.mu.Unlock()
				if abandoned {
					p.end("cancelled")
				}
				if expired {
					delete(t.procs, id)
				}
			}
			t.mu.Unlock()
		}
	}
}

// close ends every running command; used when mac-lan stops.
func (t *procTable) close() {
	t.once.Do(func() { close(t.quit) })
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range t.procs {
		p.end("cancelled")
	}
}

// detachAll is called when the connection drops: commands keep running and
// buffering, without a receiver.
func (t *procTable) detachAll(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range t.procs {
		p.mu.Lock()
		if p.sink != nil || p.detach.IsZero() {
			p.sink = nil
			p.detach = now
		}
		p.mu.Unlock()
	}
}

func (p *proc) end(state string) {
	select {
	case p.stop <- state:
	default:
	}
}

// emit records a piece of output and forwards it to the attached connection.
func (p *proc) emit(stream, data string) {
	if data == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	off := p.out.add(stream, data)
	if p.sink != nil {
		if p.sink(msg{T: "out", ID: p.id, Off: off, S: stream, D: data}) != nil {
			p.sink, p.detach = nil, time.Now()
		}
	}
}

// attach replays output from off and makes sink the live receiver.
func (p *proc) attach(off int64, sink func(msg) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	chunks, _, _ := p.out.since(off)
	for _, c := range chunks {
		if sink(msg{T: "out", ID: p.id, Off: c.off, S: c.stream, D: c.data}) != nil {
			return
		}
	}
	if !p.ended.IsZero() {
		_ = sink(msg{T: "exit", ID: p.id, State: p.state, Exit: p.exit, Err: p.err})
		return
	}
	p.sink, p.detach = sink, time.Time{}
}

// start runs command and returns once it has started or failed to start.
func (t *procTable) start(id, command, cwd, shell string, timeout time.Duration, interactive bool, sink func(msg) error) (*proc, error) {
	if !validID(id) || !validText(command, 32<<10) || command == "" {
		return nil, errors.New("invalid command request")
	}
	t.mu.Lock()
	if _, exists := t.procs[id]; exists {
		t.mu.Unlock()
		return nil, errors.New("this command was already started")
	}
	t.mu.Unlock()
	if t.running() >= maxRunning {
		return nil, errors.New("too many commands are already running on this computer")
	}
	dir, err := resolvePath(cwd, "")
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, errors.New("working folder does not exist: " + dir)
	}
	cmd, err := shellCommand(shell, command)
	if err != nil {
		return nil, err
	}
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	p := &proc{id: id, command: command, state: "running", sink: sink, cmd: cmd, stop: make(chan string, 1), done: make(chan struct{})}
	if interactive {
		if p.stdin, err = cmd.StdinPipe(); err != nil {
			return nil, err
		}
	}
	if err = cmd.Start(); err != nil {
		return nil, errors.New("could not start the command: " + err.Error())
	}
	t.mu.Lock()
	t.procs[id] = p
	t.mu.Unlock()
	keepAwake(cmd.Process.Pid)
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
	var readers sync.WaitGroup
	read := func(stream string, source io.Reader) {
		defer readers.Done()
		var text utf8Stream
		buffer := make([]byte, outputChunk)
		for {
			n, err := source.Read(buffer)
			if n > 0 {
				p.emit(stream, text.text(buffer[:n]))
			}
			if err != nil {
				p.emit(stream, text.flush())
				return
			}
		}
	}
	readers.Add(2)
	go read("o", stdout)
	go read("e", stderr)
	waited := make(chan error, 1)
	go func() { readers.Wait(); waited <- cmd.Wait() }()
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		state, forced := "", false
		var waitErr error
		select {
		case waitErr = <-waited:
		case state = <-p.stop:
			forced = true
		case <-timer.C:
			state, forced = "timed-out", true
		}
		code := -1
		if forced {
			killTree(cmd)
			select {
			case waitErr = <-waited:
				code = cmd.ProcessState.ExitCode()
			case <-time.After(10 * time.Second):
				// A grandchild still holds the output pipes; report the end anyway.
			}
		} else {
			code = cmd.ProcessState.ExitCode()
		}
		p.mu.Lock()
		switch {
		case forced:
			p.state = state
		case waitErr == nil:
			p.state = "succeeded"
		default:
			p.state = "failed"
			var exit *exec.ExitError
			if !errors.As(waitErr, &exit) {
				p.err = waitErr.Error()
			}
		}
		p.exit, p.ended = &code, time.Now()
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
		if p.sink != nil {
			_ = p.sink(msg{T: "exit", ID: p.id, State: p.state, Exit: p.exit, Err: p.err})
		}
		p.mu.Unlock()
		close(p.done)
	}()
	return p, nil
}

func (p *proc) write(data string) {
	p.mu.Lock()
	stdin := p.stdin
	p.mu.Unlock()
	if stdin != nil {
		_, _ = io.WriteString(stdin, data)
	}
}
