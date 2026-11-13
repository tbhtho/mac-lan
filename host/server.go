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
	"github.com/tbhtho/MAC-DOW/host/internal/diagnostics"
	"github.com/tbhtho/MAC-DOW/host/internal/remote"
	"math"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const maxFrame = 4 << 20

var errInvalidStart = errors.New("Choose a Mac desktop or window at 60 fps. Update both mac-lan apps if needed")

type window struct {
	ID    uint32 `json:"id"`
	Title string `json:"title"`
	App   string `json:"app"`
}
type start struct {
	Type    string `json:"type"`
	Window  uint32 `json:"window"`
	FPS     int    `json:"fps"`
	Width   int    `json:"width"`
	Quality int    `json:"quality"`
	Codec   string `json:"codec"`
	Bitrate int    `json:"bitrate"`
	Audio   bool   `json:"audio"`
	// Display asks for a Mac desktop shaped like the Windows view (full desktop only).
	Display *displaySize `json:"display,omitempty"`
}

// displaySize is a desktop size in points; see the capture helper's fit mode.
type displaySize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (d *displaySize) valid() bool {
	return d.Width >= 1024 && d.Width <= 3840 && d.Height >= 600 && d.Height <= 2160 && d.Width%2 == 0 && d.Height%2 == 0
}

func (s start) valid(windows []window) bool {
	if s.Type != "start" || s.FPS != 60 || s.Width < 640 || s.Width > 1920 || s.Quality < 35 || s.Quality > 85 {
		return false
	}
	if s.Codec != "" && s.Codec != "h264" && s.Codec != "jpeg" || s.Bitrate != 0 && (s.Bitrate < 2 || s.Bitrate > 20) {
		return false
	}
	if s.Display != nil && (s.Window != 0 || !s.Display.valid()) {
		return false
	}
	if s.Window == 0 {
		return true
	}
	for _, w := range windows {
		if w.ID == s.Window {
			return true
		}
	}
	return false
}

// Safety releases can be sent whenever the Windows focus or selected tab changes.
// Before viewing approval there is no native process to receive them, so leave the
// paired session idle. In particular, a release must not consume the start request.
func readStart(c *websocket.Conn, windows []window) (start, error) {
	for {
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Minute))
		var raw json.RawMessage
		if err := c.ReadJSON(&raw); err != nil {
			return start{}, err
		}
		var kind struct{ Type string }
		if json.Unmarshal(raw, &kind) != nil {
			return start{}, errInvalidStart
		}
		switch kind.Type {
		case "start":
			var request start
			if json.Unmarshal(raw, &request) != nil || !request.valid(windows) {
				return start{}, errInvalidStart
			}
			return request, nil
		case "release", "lease":
			var idle input
			if json.Unmarshal(raw, &idle) != nil || !idle.valid() {
				return start{}, errInvalidStart
			}
			// An active lease here also grants nothing: capture and input do not
			// exist until a separate start request receives local Mac consent.
		default:
			return start{}, errInvalidStart
		}
	}
}

type input struct {
	Type       string  `json:"type"`
	Active     bool    `json:"active"`
	View       bool    `json:"view"`
	Code       int     `json:"code"`
	Down       bool    `json:"down"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Button     int     `json:"button"`
	Delta      int     `json:"delta"`
	Stamp      int64   `json:"stamp"`
	Generation uint64  `json:"generation"`
	Reason     string  `json:"reason,omitempty"`
}

func (s *server) approve(peer string) {
	s.approvedMu.Lock()
	defer s.approvedMu.Unlock()
	if s.approved == nil {
		s.approved = map[string]bool{}
	}
	s.approved[peer] = true
}
func (s *server) viewApproved(peer string) bool {
	s.approvedMu.Lock()
	defer s.approvedMu.Unlock()
	return s.approved[peer]
}

func (e input) valid() bool {
	if e.Reason != "" && (e.Type != "release" || !diagnostics.ValidControlReleaseReason(e.Reason)) {
		return false
	}
	switch e.Type {
	case "lease", "release":
		return e.Generation < 9007199254740992
	case "keyframe":
		return true
	case "key":
		return e.Code >= 0 && e.Code <= 126
	case "move":
		return !math.IsNaN(e.X) && !math.IsNaN(e.Y) && e.X >= 0 && e.X < 1 && e.Y >= 0 && e.Y < 1
	case "button":
		return e.Button >= 0 && e.Button <= 2
	case "scroll":
		return e.Delta >= -2400 && e.Delta <= 2400
	}
	return false
}
func isCursorStatus(line string) bool {
	var probe struct{ Type string }
	return json.Unmarshal([]byte(line), &probe) == nil && probe.Type == "cursor"
}
func isCaptureAlive(line string) bool {
	return len(line) < 64 && strings.Contains(line, `"captureAlive"`)
}
func privateIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	b := ip.To4()
	return b != nil && (b[0] == 10 || b[0] == 172 && b[1] >= 16 && b[1] <= 31 || b[0] == 192 && b[1] == 168)
}
func identity() (tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "MAC-DOW ephemeral pairing"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(12 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(sum[:]), nil
}

type server struct {
	token       string
	peerAllowed func(string) bool
	slot        chan struct{}
	catalog     func() ([]window, error)
	consent     func(start) bool
	// Peers approved during this sharing session; pairing approval also allows viewing.
	approvedMu   sync.Mutex
	approved     map[string]bool
	pair         func(string, string) bool
	prompts      promptLimit
	connectionMu sync.Mutex
	active       *websocket.Conn
	activeDone   chan struct{}
	activePeer   string
	// streaming is true while video or control runs; an idle remembered PC that
	// stays connected for files and clipboard does not hold back updates.
	streaming bool
	// In-memory reconnect credentials per approved peer, valid only while this
	// sharing process runs. They let the same Windows PC return after a drop
	// without a new physical approval, within the scope the user already granted.
	resumes map[string]resumeCredential
	// PCs approved on this Mac in earlier sessions (nil when unavailable).
	trust      *trustStore
	stopped    bool
	updating   bool
	stream     func(func([]byte), func(string), <-chan input) error
	transfers  *transferReceiver
	clipboard  *clipboard
	// remote runs commands and file requests for the paired PC once this Mac's
	// user has switched that on; nil when unavailable.
	remote *remote.Node
	videoError string
	// selected is only accessed while slot is held.
	selected start
	// keyframes carries the send queue's requests for a keyframe to the capture helper of the
	// current stream; like selected it is only accessed while slot is held.
	keyframes chan struct{}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/transfer/outgoing") && s.transfers != nil {
		s.transfers.serveOutgoing(w, r, s.peerAllowed)
		return
	}
	if r.URL.Path == "/transfer" && s.transfers != nil {
		s.transfers.serveHTTP(w, r, s.peerAllowed)
		return
	}
	if r.URL.Path == "/remote" && s.transfers != nil && s.remote != nil {
		s.serveRemote(w, r)
		return
	}
	if r.URL.Path == "/clipboard" && s.transfers != nil && s.clipboard != nil {
		s.serveClipboard(w, r)
		return
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !s.peerAllowed(peer) || r.URL.Path != "/session" && r.URL.Path != "/" || r.Method != "GET" {
		http.Error(w, "LAN session endpoint only", 403)
		return
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser origins are not allowed", 403)
		return
	}
	held := s.tryTakeSlot()
	if !held && !s.activeFrom(peer) {
		http.Error(w, "one controller at a time", 409)
		return
	}
	up := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: videoWriteBuffer, HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		if held {
			<-s.slot
		}
		return
	}
	c.SetReadLimit(4096)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth struct{ Type, Token, Code, Device, Name string }
	if c.ReadJSON(&auth) != nil {
		c.Close()
		if held {
			<-s.slot
		}
		return
	}
	authenticated, freshPairing := false, false
	if !held {
		// The same PC is replacing a connection the Mac still holds (a drop or sleep
		// not yet detected). Only an instantly verifiable credential may close it;
		// an unauthenticated request from that address cannot end a live session.
		if auth.Type == "resume" && len(auth.Token) == 64 && s.redeemResume(peer, auth.Token, time.Now()) {
			authenticated = s.takeOverSlot(peer)
		} else if auth.Type == "auth" && len(auth.Token) == 64 && len(s.token) == 64 && subtle.ConstantTimeCompare([]byte(auth.Token), []byte(s.token)) == 1 {
			authenticated = s.takeOverSlot(peer)
		} else if auth.Type == "trusted" && s.trust != nil && s.trust.verify(auth.Device, auth.Token, time.Now()) {
			authenticated = s.takeOverSlot(peer)
			if authenticated {
				s.approve(peer)
			}
		}
		if !authenticated {
			c.Close()
			return
		}
	}
	defer func() { <-s.slot }()
	s.connectionMu.Lock()
	if s.stopped || s.updating {
		s.connectionMu.Unlock()
		c.Close()
		return
	}
	doneConnection := make(chan struct{})
	s.active = c
	s.activeDone = doneConnection
	s.activePeer = peer
	s.connectionMu.Unlock()
	defer func() {
		c.Close()
		s.connectionMu.Lock()
		s.active = nil
		s.activeDone = nil
		s.activePeer = ""
		close(doneConnection)
		s.connectionMu.Unlock()
	}()
	if authenticated {
		// Already verified while taking over the slot.
	} else if auth.Type == "auth" && len(auth.Token) == 64 && len(s.token) == 64 {
		authenticated = subtle.ConstantTimeCompare([]byte(auth.Token), []byte(s.token)) == 1
	} else if auth.Type == "resume" && len(auth.Token) == 64 {
		authenticated = s.redeemResume(peer, auth.Token, time.Now())
	} else if auth.Type == "trusted" && s.trust != nil && s.trust.verify(auth.Device, auth.Token, time.Now()) {
		// A PC the user approved on this Mac before; approval covers viewing as well.
		authenticated = true
		s.approve(peer)
	} else if auth.Type == "pair" && s.pair != nil && validPairNumber(auth.Code) && s.prompts.allow(time.Now()) {
		authenticated = s.pair(peer, auth.Code)
		if authenticated {
			s.approve(peer)
			freshPairing = true
		}
	}
	if !authenticated {
		if auth.Type == "trusted" {
			// Lets Windows forget a credential this Mac no longer knows, instead of
			// guessing from an ordinary dropped connection.
			_ = c.WriteJSON(map[string]string{"type": "error", "code": "unknown-device", "message": "This Mac no longer remembers this PC."})
		}
		diagnostics.Emit("mac", "pairing-denied", "not-approved", nil)
		return
	}
	diagnostics.Emit("mac", "pairing-approved", "ok", nil)
	transferToken := ""
	if s.transfers != nil {
		var revoke func()
		transferToken, revoke, err = s.transfers.grant(peer)
		if err != nil {
			return
		}
		defer revoke()
		if s.remote != nil {
			// The remote-command link belongs to this pairing and ends with it. A
			// command that is still running waits briefly for the PC to reconnect.
			defer s.remote.Drop()
		}
	}
	windows, err := s.catalog()
	videoError := s.videoError
	if err != nil {
		if s.transfers == nil {
			_ = c.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
			return
		}
		windows = nil
		videoError = "Mac video is unavailable. File transfers are ready. Check the Mac's Screen Recording permission and restart mac-lan."
	}
	if windows == nil {
		windows = []window{}
	}
	// matchDisplay: this host can give the full desktop the shape Windows asks for in "display".
	catalog := map[string]any{"type": "catalog", "maxFps": 60, "windows": windows, "keyframes": true, "matchDisplay": true}
	if s.viewApproved(peer) {
		if resume, er := s.issueResume(peer, time.Now()); er == nil {
			catalog["resumeToken"] = resume
		}
	}
	// A PC the user just approved is remembered, so it can reconnect after either
	// app restarts. Older Windows builds ignore this field.
	if freshPairing && s.trust != nil && auth.Name != "" {
		if id, secret, er := s.trust.remember(auth.Name, time.Now()); er == nil {
			catalog["trustedDevice"] = map[string]string{"id": id, "token": secret}
		}
	}
	if transferToken != "" {
		catalog["transferToken"] = transferToken
		if s.remote != nil {
			// Windows may open /remote with the same token. Older Windows builds ignore this.
			catalog["remote"] = 1
		}
	}
	if videoError != "" {
		catalog["maxFps"] = 0
		catalog["videoError"] = videoError
	}
	if err = c.WriteJSON(catalog); err != nil {
		return
	}
	// Pongs are only processed while this connection is being read; the local
	// viewing-consent prompt can pause reads for up to 45 seconds, so the
	// timeout must outlast it.
	stopPings := keepAlive(c, 10*time.Second, 75*time.Second)
	defer stopPings()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Minute))
	var request start
	for {
		request, err = readStart(c, windows)
		if err != nil || videoError == "" {
			break
		}
		_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err = c.WriteJSON(map[string]any{"type": "status", "paused": true, "message": videoError}); err != nil {
			return
		}
	}
	if err != nil {
		if errors.Is(err, errInvalidStart) {
			diagnostics.Emit("mac", "stream-denied", "invalid-request", nil)
			_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
			_ = c.WriteJSON(map[string]string{"type": "error", "message": errInvalidStart.Error()})
		} else {
			diagnostics.Emit("mac", "stream-ended", "before-view-request", nil)
		}
		return
	}
	diagnostics.Emit("mac", "stream-request", "ok", map[string]int{"width": request.Width, "fps": request.FPS})
	if !s.viewApproved(peer) && !s.consent(request) {
		diagnostics.Emit("mac", "stream-denied", "not-approved", nil)
		_ = c.WriteJSON(map[string]string{"type": "error", "message": "Mac user did not consent"})
		return
	}
	diagnostics.Emit("mac", "stream-approved", "ok", map[string]int{"width": request.Width, "fps": request.FPS})
	s.approve(peer)
	s.selected = request
	if err = c.WriteJSON(map[string]string{"type": "ready"}); err != nil {
		return
	}
	events := make(chan input, 32)
	// Input pressure keeps video alive. Moves may be dropped, but a lost discrete
	// edge must release control: subsequent leases cannot reconstruct a key-up.
	dropped, lastDrop := 0, time.Time{}
	reportDrop := func() {
		dropped++
		if now := time.Now(); now.Sub(lastDrop) >= time.Second {
			lastDrop = now
			count := dropped
			if count > 100000 {
				count = 100000
			}
			diagnostics.Emit("mac", "input-dropped", "queue-full", map[string]int{"dropped": count})
		}
	}
	frames := newVideoQueue()
	keyframes := make(chan struct{}, 1)
	s.keyframes = keyframes
	frames.requestKey = func() {
		select {
		case keyframes <- struct{}{}:
		default:
		}
	}
	// About half a second of Mac audio; when full the oldest chunk is dropped, never video.
	audio := make(chan []byte, 24)
	status := make(chan statusMessage, 4)
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done); c.Close() }) }
	defer stop()
	go func() {
		defer stop()
		writeQueuedVideo(done, frames, status, audio, func(text bool, b []byte) error {
			kind := websocket.BinaryMessage
			if text {
				kind = websocket.TextMessage
			}
			// A short downlink stall must not tear down the session; the native
			// viewer-outage grace and the input read deadline bound real outages.
			_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
			return c.WriteMessage(kind, b)
		})
	}()
	go func() {
		defer stop()
		defer close(events)
		for {
			// A few seconds of Wi-Fi silence must not end capture: the native lease
			// releases control after 850 ms, and the helper stops only after 15 s.
			_ = c.SetReadDeadline(time.Now().Add(20 * time.Second))
			var e input
			if c.ReadJSON(&e) != nil || !e.valid() {
				return
			}
			if e.Type == "release" && e.Reason != "" {
				// A Windows armed-to-released transition supplies only this fixed
				// reason. The Mac owns reporting; no input or arbitrary client text
				// enters remote diagnostics, and legacy empty releases stay silent.
				diagnostics.Emit("mac", "control-release", e.Reason, nil)
			}
			if e.Type == "lease" {
				b, _ := json.Marshal(map[string]any{"type": "pong", "stamp": e.Stamp})
				select {
				case status <- statusMessage{line: string(b)}:
				case <-done:
					return
				default:
				}
			}
			select {
			case events <- e:
			case <-done:
				return
			default:
				if e.Type == "move" || e.Type == "scroll" {
					reportDrop()
					continue
				}
				// Lease and release carry safety state; wait briefly for the native
				// writer rather than losing them with a key still held.
				wait := time.NewTimer(100 * time.Millisecond)
				select {
				case events <- e:
					wait.Stop()
				case <-done:
					wait.Stop()
					return
				case <-wait.C:
					reportDrop()
					// This goroutine is the channel's sole producer. Clear stale
					// queued input and reserve its next delivery for release, before
					// any later lease can renew a key/button whose up was lost.
					requestKeyframe := false
				drain:
					for {
						select {
						case pending := <-events:
							requestKeyframe = requestKeyframe || pending.Type == "keyframe"
						default:
							break drain
						}
					}
					events <- input{Type: "release"}
					if requestKeyframe {
						events <- input{Type: "keyframe"}
					}
				}
			}
		}
	}()
	send := func(b []byte) {
		if len(b) == 0 || len(b) > maxFrame {
			stop()
			return
		}
		select {
		case <-done:
			return
		default:
		}
		if len(b) >= 9 && b[0] == 4 {
			select {
			case audio <- b:
			default:
				select {
				case <-audio:
				default:
				}
				select {
				case audio <- b:
				default:
				}
			}
			return
		}
		frames.push(b, time.Now())
	}
	report := func(line string) {
		// Status text stays short; only the Mac pointer image may be larger.
		if len(line) > 2048 && (len(line) > 60000 || !isCursorStatus(line)) {
			return
		}
		if !json.Valid([]byte(line)) {
			b, _ := json.Marshal(map[string]string{"type": "status", "message": line})
			line = string(b)
		}
		select {
		case status <- statusMessage{line: line}:
			return
		case <-done:
			return
		default:
		}
		// captureAlive is a liveness beat repeated every half second; Windows
		// uses only its recency, so a full queue during a downlink stall drops it
		// instead of counting toward the dead-viewer timeout below.
		if isCaptureAlive(line) {
			return
		}
		// Native startup can emit a short burst of encoder capabilities before
		// the first frame. Backpressure this bounded queue instead of treating
		// routine status output as a dead viewer and killing capture. The wait
		// outlasts the writer's 10-second deadline so a Wi-Fi stall the writer
		// tolerates does not end the session here first.
		deadline := time.NewTimer(12 * time.Second)
		defer deadline.Stop()
		select {
		case status <- statusMessage{line: line}:
		case <-done:
		case <-deadline.C:
			stop()
		}
	}
	s.connectionMu.Lock()
	s.streaming = true
	s.connectionMu.Unlock()
	err = s.stream(send, report, events)
	s.connectionMu.Lock()
	s.streaming = false
	s.connectionMu.Unlock()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		// Do not enqueue the native failure and immediately close the socket:
		// wait for the single writer to send it so Windows can show the reason.
		b, _ := json.Marshal(map[string]string{"type": "error", "message": "Capture stopped: " + err.Error()})
		final := statusMessage{line: string(b), sent: make(chan struct{})}
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		select {
		case status <- final:
			select {
			case <-final.sent:
			case <-done:
			case <-deadline.C:
			}
		case <-done:
		case <-deadline.C:
		}
	}
}

func (s *server) stopActive() {
	s.connectionMu.Lock()
	s.stopped = true
	c, done := s.active, s.activeDone
	s.connectionMu.Unlock()
	if c != nil {
		c.Close()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	if s.transfers != nil {
		s.transfers.stopAndWait()
	}
}

// keepAlive pings an authenticated peer so a sleeping or vanished Windows PC
// is noticed within about a minute instead of holding the single session
// slot until the five-minute idle read deadline. WebSocket clients, including
// every released Windows build, answer pings automatically; pongs are seen by
// whichever goroutine is currently reading the connection.
func keepAlive(c *websocket.Conn, every, timeout time.Duration) func() {
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	c.SetPongHandler(func(string) error {
		last.Store(time.Now().UnixNano())
		return nil
	})
	quit := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
				if time.Since(time.Unix(0, last.Load())) > timeout {
					_ = c.Close()
					return
				}
				_ = c.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(quit) }) }
}

const resumeLifetime = 30 * time.Minute

type resumeCredential struct {
	token   string
	expires time.Time
}

// issueResume mints a fresh single-use reconnect token for an approved peer.
func (s *server) issueResume(peer string, now time.Time) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	s.approvedMu.Lock()
	defer s.approvedMu.Unlock()
	if s.resumes == nil {
		s.resumes = map[string]resumeCredential{}
	}
	s.resumes[peer] = resumeCredential{token, now.Add(resumeLifetime)}
	return token, nil
}

// redeemResume accepts a token only from the peer it was issued to, before it
// expires, and only once; the catalog of the resumed session carries a new one.
func (s *server) redeemResume(peer, token string, now time.Time) bool {
	s.approvedMu.Lock()
	defer s.approvedMu.Unlock()
	c, ok := s.resumes[peer]
	if !ok || !s.approved[peer] || now.After(c.expires) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(c.token), []byte(token)) != 1 {
		return false
	}
	delete(s.resumes, peer)
	return true
}

func (s *server) tryTakeSlot() bool {
	select {
	case s.slot <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *server) activeFrom(peer string) bool {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	return s.active != nil && s.activePeer == peer
}

// takeOverSlot closes the same peer's previous connection and takes the single
// session slot once it is released. A different peer's session is never closed.
func (s *server) takeOverSlot(peer string) bool {
	s.connectionMu.Lock()
	c, done, same := s.active, s.activeDone, s.activePeer == peer
	s.connectionMu.Unlock()
	if c == nil || !same || done == nil {
		return false
	}
	_ = c.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		return false
	}
	select {
	case s.slot <- struct{}{}:
		return true
	case <-time.After(time.Second):
		return false
	}
}
