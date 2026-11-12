package remote

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// The local protocol is one JSON object per line. A request carries "rid" and
// "op"; its answer repeats "rid" with "ok" and either a payload or "error".
// Lines with "ev" are events sent only to mac-lan's own window.
//
// Two kinds of client exist. mac-lan's own window talks over a private pipe and
// may change settings and answer approval prompts. Everything else (the MCP
// server, the command-line shell) connects to a loopback port with a token read
// from a private per-user file, may only use the link, and only while the user
// has switched "Allow local tools and AI agents" on.

type request struct {
	RID         json.RawMessage `json:"rid"`
	Op          string          `json:"op"`
	Device      string          `json:"device"`
	Command     string          `json:"command"`
	Cwd         string          `json:"cwd"`
	Shell       string          `json:"shell"`
	Timeout     int             `json:"timeout"`
	Wait        int             `json:"wait"`
	Interactive bool            `json:"interactive"`
	ID          string          `json:"id"`
	Since       int64           `json:"since"`
	Data        string          `json:"data"`
	Direction   string          `json:"direction"`
	Local       string          `json:"local"`
	Remote      string          `json:"remote"`
	Overwrite   bool            `json:"overwrite"`
	Source      string          `json:"source"`
	Destination string          `json:"destination"`
	Path        string          `json:"path"`
	Accept      *bool           `json:"accept"`
	Agents      *bool           `json:"agents"`
	Allow       bool            `json:"allow"`
	Address     string          `json:"address"`
	Port        int             `json:"port"`
	Fingerprint string          `json:"fingerprint"`
	Token       string          `json:"token"`
	Ticket      string          `json:"ticket"`
	Content     string          `json:"content"`
	Old         string          `json:"old"`
	New         string          `json:"new"`
	All         bool            `json:"all"`
	Width       int             `json:"width"`
	Always      bool            `json:"always"`
}

// Server exposes a Node to local clients.
type Server struct {
	node *Node

	mu       sync.Mutex
	ui       func(map[string]any) // event writer of mac-lan's own window
	prompts  map[string]chan Answer
	tickets  map[string]time.Time
	token    string
	listener net.Listener
	dialStop context.CancelFunc
	standby  context.CancelFunc
	touched  time.Time
	dir      string
}

// Idle is how long ago a local client last asked for anything.
func (s *Server) Idle() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.touched)
}

// Current reports whether dir's endpoint file still names this process; a
// newer mac-lan on this computer replaces it.
func (s *Server) Current(dir string) bool {
	data, err := os.ReadFile(endpointPath(dir))
	var e endpoint
	return err == nil && json.Unmarshal(data, &e) == nil && e.PID == os.Getpid()
}

func NewServer(n *Node) *Server {
	return &Server{node: n, prompts: map[string]chan Answer{}, tickets: map[string]time.Time{}, touched: time.Now()}
}

// State is what mac-lan's window shows about the link.
func (s *Server) State() map[string]any {
	settings := s.node.Settings()
	trusted := settings.Trusted
	if trusted == nil {
		trusted = []string{}
	}
	return map[string]any{"devices": s.node.Devices(), "busy": s.node.Busy(), "accept": settings.Accept, "agents": settings.Agents, "trusted": trusted}
}

// ServeUI serves mac-lan's own window over its private pipe until it closes.
// Approval prompts are sent to it as events and answered with the confirm op.
func (s *Server) ServeUI(r io.Reader, w io.Writer) {
	var wmu sync.Mutex
	write := func(v map[string]any) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = json.NewEncoder(w).Encode(v)
	}
	s.mu.Lock()
	s.ui = write
	s.mu.Unlock()
	s.node.SetConfirm(s.askUI)
	s.node.OnChange(func() {
		state := s.State()
		state["ev"] = "state"
		write(state)
	})
	s.serve(r, write, true, "user")
	s.node.SetConfirm(nil)
	s.node.OnChange(nil)
}

func (s *Server) askUI(r ConfirmRequest) Answer {
	s.mu.Lock()
	write := s.ui
	id := newID()
	answer := make(chan Answer, 1)
	s.prompts[id] = answer
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.prompts, id)
		s.mu.Unlock()
	}()
	if write == nil {
		return Deny
	}
	write(map[string]any{"ev": "confirm", "id": id, "title": r.Title, "detail": r.Detail, "reason": r.Reason, "origin": r.Origin, "target": r.Target, "remember": r.Remember})
	select {
	case reply := <-answer:
		return reply
	case <-r.Withdrawn: // cancelled or timed out: the window closes its prompt
	case <-time.After(confirmTimeout + 5*time.Second):
	}
	write(map[string]any{"ev": "confirm-expired", "id": id})
	return Deny
}

func (s *Server) serve(r io.Reader, write func(map[string]any), privileged bool, origin string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 12<<20) // a write request carries a whole file
	var running sync.WaitGroup
	for scanner.Scan() {
		var q request
		if json.Unmarshal(scanner.Bytes(), &q) != nil || q.Op == "" {
			write(map[string]any{"ok": false, "error": "invalid request"})
			continue
		}
		running.Add(1)
		go func() {
			defer running.Done()
			out := map[string]any{"ok": true}
			if err := s.dispatch(q, privileged, origin, out); err != nil {
				out = map[string]any{"ok": false, "error": err.Error()}
			}
			if len(q.RID) > 0 {
				out["rid"] = q.RID
			}
			write(out)
		}()
	}
	running.Wait()
}

var errPrivileged = errors.New("only the mac-lan window can do this")

func (s *Server) dispatch(q request, privileged bool, origin string, out map[string]any) error {
	if !privileged && origin == "agent" && !s.node.Settings().Agents {
		return errors.New("access for local tools and AI agents is switched off in mac-lan on this computer. The user can turn it on in mac-lan's Terminal window (Windows) or Remote Commands menu (Mac)")
	}
	s.mu.Lock()
	s.touched = time.Now()
	s.mu.Unlock()
	wait := time.Duration(q.Wait) * time.Second
	if wait < 0 {
		wait = 0
	}
	if wait > 300*time.Second {
		wait = 300 * time.Second
	}
	switch q.Op {
	case "devices":
		out["devices"] = s.node.Devices()
	case "exec":
		j, err := s.node.Exec(ExecRequest{Device: q.Device, Command: q.Command, Cwd: q.Cwd, Shell: q.Shell, Timeout: q.Timeout, Interactive: q.Interactive, Origin: origin})
		if err != nil {
			return err
		}
		return s.result(j, wait, out)
	case "transfer":
		j, err := s.node.Transfer(TransferRequest{Device: q.Device, Direction: q.Direction, Local: q.Local, Remote: q.Remote, Overwrite: q.Overwrite, Origin: origin})
		if err != nil {
			return err
		}
		return s.result(j, wait, out)
	case "deploy":
		j, err := s.node.Deploy(DeployRequest{Device: q.Device, Source: q.Source, Destination: q.Destination, Command: q.Command, Shell: q.Shell, Timeout: q.Timeout, Origin: origin})
		if err != nil {
			return err
		}
		return s.result(j, wait, out)
	case "status":
		view, err := s.node.Status(q.ID, q.Since, wait)
		if err != nil {
			return err
		}
		out["job"] = view
	case "wait":
		view, err := s.node.WaitDone(q.ID, wait)
		if err != nil {
			return err
		}
		out["job"] = view
	case "cancel":
		return s.node.Cancel(q.ID)
	case "stdin":
		return s.node.Input(q.ID, q.Data)
	case "resolve":
		path, err := s.node.Resolve(q.Path, q.Cwd)
		if err != nil {
			return err
		}
		out["path"] = path
	case "read":
		data, err := s.node.ReadFile(q.Device, q.Path, origin)
		if err != nil {
			return err
		}
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return errors.New("that is not a text file (" + sizeText(int64(len(data))) + "). Copy it with transfer_file instead")
		}
		out["content"], out["size"] = string(data), len(data)
	case "write":
		j, err := s.node.WriteFile(q.Device, q.Path, []byte(q.Content), q.Overwrite, origin, "")
		if err != nil {
			return err
		}
		return s.result(j, wait, out)
	case "edit":
		j, count, err := s.node.EditFile(q.Device, q.Path, q.Old, q.New, q.All, origin)
		if err != nil {
			return err
		}
		out["replaced"] = count
		return s.result(j, wait, out)
	case "dir":
		path, entries, more, err := s.node.ListDir(q.Device, q.Path, origin)
		if err != nil {
			return err
		}
		out["path"], out["entries"], out["more"] = path, entries, more
	case "shot":
		data, width, height, err := s.node.Screenshot(q.Device, q.Width, origin)
		if err != nil {
			return err
		}
		out["image"], out["width"], out["height"] = base64.StdEncoding.EncodeToString(data), width, height
	case "trust", "untrust":
		if !privileged {
			return errPrivileged
		}
		var err error
		if q.Op == "trust" {
			err = s.node.Trust(q.Path)
		} else {
			err = s.node.Untrust(q.Path)
		}
		if err != nil {
			return err
		}
		for k, v := range s.State() {
			out[k] = v
		}
	case "jobs":
		out["jobs"] = s.node.Jobs(50)
	case "state":
		for k, v := range s.State() {
			out[k] = v
		}
	case "set":
		if !privileged {
			return errPrivileged
		}
		next := s.node.Settings()
		if q.Accept != nil {
			next.Accept = *q.Accept
		}
		if q.Agents != nil {
			next.Agents = *q.Agents
		}
		if err := s.node.SetSettings(next); err != nil {
			return errors.New("could not save the setting")
		}
		for k, v := range s.State() {
			out[k] = v
		}
	case "confirm":
		if !privileged {
			return errPrivileged
		}
		s.mu.Lock()
		answer := s.prompts[q.ID]
		s.mu.Unlock()
		if answer != nil {
			reply := Deny
			if q.Allow {
				reply = AllowOnce
				if q.Always {
					reply = AllowAlways
				}
			}
			select {
			case answer <- reply:
			default:
			}
		}
	case "link":
		if !privileged {
			return errPrivileged
		}
		return s.Link(q.Address, q.Port, q.Fingerprint, q.Token)
	case "unlink":
		if !privileged {
			return errPrivileged
		}
		s.Unlink()
	case "forget-standby":
		// The Mac was forgotten on this computer: drop its standby details too.
		if !privileged {
			return errPrivileged
		}
		s.mu.Lock()
		dir := s.dir
		s.mu.Unlock()
		ForgetStandby(dir)
		if l := s.node.current(); l != nil && l.standby {
			l.close()
		}
	case "ticket":
		if !privileged {
			return errPrivileged
		}
		out["ticket"] = s.Ticket()
	default:
		return errors.New("unknown request")
	}
	return nil
}

// result reports a new job, after waiting up to wait for it to finish.
func (s *Server) result(j *Job, wait time.Duration, out map[string]any) error {
	view, err := s.node.WaitDone(j.id, wait)
	if err != nil {
		return err
	}
	out["job"] = view
	return nil
}

// Ticket returns a single-use credential, valid for a minute, that lets a shell
// the user opened from mac-lan's own menu connect without the agent switch.
func (s *Server) Ticket() string {
	ticket := newID() + newID()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for t, expires := range s.tickets {
		if now.After(expires) {
			delete(s.tickets, t)
		}
	}
	s.tickets[ticket] = now.Add(time.Minute)
	return ticket
}

func (s *Server) redeem(ticket string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.tickets[ticket]
	delete(s.tickets, ticket)
	return ok && time.Now().Before(expires)
}

type endpoint struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func endpointPath(dir string) string { return filepath.Join(dir, "remote-endpoint.json") }

// Listen opens the loopback endpoint for local tools and records its port and
// token in dir, readable only by this user.
func (s *Server) Listen(dir string) error {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	token := newID() + newID()
	data, _ := json.Marshal(endpoint{Port: listener.Addr().(*net.TCPAddr).Port, Token: token, PID: os.Getpid()})
	if err = writePrivate(endpointPath(dir), data); err != nil {
		_ = listener.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.token, s.listener, s.standby, s.dir = token, listener, cancel, dir
	s.mu.Unlock()
	go s.standbyLoop(ctx, dir)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go s.serveConn(conn)
		}
	}()
	return nil
}

// Close stops the loopback endpoint and the connection to the other computer.
func (s *Server) Close(dir string) {
	s.Unlink()
	s.mu.Lock()
	listener, standby := s.listener, s.standby
	s.listener, s.standby = nil, nil
	s.mu.Unlock()
	if standby != nil {
		standby()
	}
	if l := s.node.current(); l != nil {
		l.close()
	}
	if listener != nil {
		_ = listener.Close()
		if dir != "" && s.Current(dir) {
			_ = os.Remove(endpointPath(dir))
		}
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	var wmu sync.Mutex
	write := func(v map[string]any) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		_ = json.NewEncoder(conn).Encode(v)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReaderSize(conn, 64<<10)
	line, err := reader.ReadBytes('\n')
	var hello request
	if err != nil || json.Unmarshal(line, &hello) != nil {
		return
	}
	s.mu.Lock()
	token := s.token
	s.mu.Unlock()
	origin := ""
	switch {
	case hello.Ticket != "" && s.redeem(hello.Ticket):
		origin = "user"
	case len(hello.Token) == len(token) && subtle.ConstantTimeCompare([]byte(hello.Token), []byte(token)) == 1:
		origin = "agent"
	default:
		write(map[string]any{"ok": false, "error": "not authorized"})
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	write(map[string]any{"ok": true, "hello": true})
	s.serve(reader, write, false, origin)
}

func privateIPv4(address string) bool {
	ip := net.ParseIP(address).To4()
	return ip != nil && (ip[0] == 10 || ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 || ip[0] == 192 && ip[1] == 168 || ip[0] == 127)
}

// Link keeps a connection to the Mac's /remote endpoint, authenticated by the
// pinned certificate fingerprint and the current pairing's bearer token. It
// redials after a drop until Unlink or a newer Link replaces it.
func (s *Server) Link(address string, port int, fingerprint, token string) error {
	pin, err := hex.DecodeString(fingerprint)
	if err != nil || len(pin) != 32 || len(token) != 64 || !privateIPv4(address) || port < 1024 || port > 65535 {
		return errors.New("invalid connection details")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	previous := s.dialStop
	s.dialStop = cancel
	s.mu.Unlock()
	if previous != nil {
		previous()
	}
	go func() {
		delay := time.Second
		for ctx.Err() == nil {
			conn, err := dialRemote(ctx, address, port, pin, token, 8*time.Second)
			if err == nil && s.node.standbyBusy() {
				// Work is in progress over the standby link; take over when it is done.
				_ = conn.Close()
				delay = 4 * time.Second
			} else if err == nil {
				stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
				s.node.Attach(conn)
				stop()
				delay = time.Second
			} else if delay < 8*time.Second {
				delay *= 2
			}
			select {
			case <-ctx.Done():
			case <-time.After(delay):
			}
		}
	}()
	return nil
}

func (s *Server) Unlink() {
	s.mu.Lock()
	stop := s.dialStop
	s.dialStop = nil
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// Client is a local tool's connection to mac-lan on this computer.
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	next   int
}

// ErrNotRunning means mac-lan is not open on this computer.
var ErrNotRunning = errors.New("mac-lan is not running on this computer. Open mac-lan, connect the two computers, then try again")

// Dial connects to mac-lan on this computer. ticket is "" for tools that use
// the token in the private endpoint file.
func Dial(dir, ticket string) (*Client, error) {
	data, err := os.ReadFile(endpointPath(dir))
	var e endpoint
	if err != nil || json.Unmarshal(data, &e) != nil || e.Port < 1 || e.Port > 65535 {
		return nil, ErrNotRunning
	}
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(e.Port)), 3*time.Second)
	if err != nil {
		return nil, ErrNotRunning
	}
	c := &Client{conn: conn, reader: bufio.NewReaderSize(conn, 64<<10)}
	hello := map[string]any{"token": e.Token}
	if ticket != "" {
		hello = map[string]any{"ticket": ticket}
	}
	reply, err := c.exchange(hello, 5*time.Second)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if ok, _ := reply["ok"].(bool); !ok {
		_ = conn.Close()
		return nil, errors.New("mac-lan refused this tool; restart mac-lan and try again")
	}
	return c, nil
}

func (c *Client) Close() { _ = c.conn.Close() }

func (c *Client) exchange(v map[string]any, timeout time.Duration) (map[string]any, error) {
	_ = c.conn.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(c.conn).Encode(v); err != nil {
		return nil, ErrNotRunning
	}
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, ErrNotRunning
	}
	var reply map[string]any
	if json.Unmarshal(line, &reply) != nil {
		return nil, errors.New("mac-lan sent an invalid answer")
	}
	return reply, nil
}

// Call sends one request and returns its answer; a refusal is an error.
func (c *Client) Call(v map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	v["rid"] = c.next
	wait, _ := v["wait"].(int)
	reply, err := c.exchange(v, time.Duration(wait+60)*time.Second)
	if err != nil {
		return nil, err
	}
	if ok, _ := reply["ok"].(bool); !ok {
		text, _ := reply["error"].(string)
		if text == "" {
			text = "mac-lan could not do that"
		}
		return nil, errors.New(text)
	}
	return reply, nil
}
