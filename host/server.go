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
	"math"
	"math/big"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const maxFrame = 4 << 20

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
}

func (s start) valid(windows []window) bool {
	if s.Type != "start" || s.FPS < 5 || s.FPS > 30 || s.Width < 640 || s.Width > 1920 || s.Quality < 35 || s.Quality > 85 {
		return false
	}
	if s.Codec != "" && s.Codec != "h264" && s.Codec != "jpeg" || s.Bitrate != 0 && (s.Bitrate < 2 || s.Bitrate > 20) {
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
}

func (e input) valid() bool {
	switch e.Type {
	case "lease", "release":
		return e.Generation < 9007199254740992
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
	token        string
	peerAllowed  func(string) bool
	slot         chan struct{}
	catalog      func() ([]window, error)
	consent      func(start) bool
	pair         func(string, string) bool
	prompts      promptLimit
	connectionMu sync.Mutex
	active       *websocket.Conn
	activeDone   chan struct{}
	stopped      bool
	stream       func(func([]byte), func(string), <-chan input) error
	// selected is only accessed while slot is held.
	selected start
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !s.peerAllowed(peer) || r.URL.Path != "/session" && r.URL.Path != "/" || r.Method != "GET" {
		http.Error(w, "LAN session endpoint only", 403)
		return
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser origins are not allowed", 403)
		return
	}
	select {
	case s.slot <- struct{}{}:
		defer func() { <-s.slot }()
	default:
		http.Error(w, "one controller at a time", 409)
		return
	}
	up := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.connectionMu.Lock()
	if s.stopped {
		s.connectionMu.Unlock()
		c.Close()
		return
	}
	doneConnection := make(chan struct{})
	s.active = c
	s.activeDone = doneConnection
	s.connectionMu.Unlock()
	defer func() {
		c.Close()
		s.connectionMu.Lock()
		s.active = nil
		s.activeDone = nil
		close(doneConnection)
		s.connectionMu.Unlock()
	}()
	c.SetReadLimit(4096)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth struct{ Type, Token, Code string }
	if c.ReadJSON(&auth) != nil {
		return
	}
	authenticated := false
	if auth.Type == "auth" && len(auth.Token) == 64 && len(s.token) == 64 {
		authenticated = subtle.ConstantTimeCompare([]byte(auth.Token), []byte(s.token)) == 1
	} else if auth.Type == "pair" && s.pair != nil && validPairNumber(auth.Code) && s.prompts.allow(time.Now()) {
		authenticated = s.pair(peer, auth.Code)
	}
	if !authenticated {
		diagnostics.Emit("mac", "pairing-denied", "not-approved", nil)
		return
	}
	diagnostics.Emit("mac", "pairing-approved", "ok", nil)
	windows, err := s.catalog()
	if err != nil {
		_ = c.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
		return
	}
	if err = c.WriteJSON(map[string]any{"type": "catalog", "windows": windows}); err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Minute))
	var request start
	if c.ReadJSON(&request) != nil || !request.valid(windows) {
		return
	}
	if !s.consent(request) {
		diagnostics.Emit("mac", "stream-denied", "not-approved", nil)
		_ = c.WriteJSON(map[string]string{"type": "error", "message": "Mac user did not consent"})
		return
	}
	diagnostics.Emit("mac", "stream-approved", "ok", map[string]int{"width": request.Width, "fps": request.FPS})
	s.selected = request
	if err = c.WriteJSON(map[string]string{"type": "ready"}); err != nil {
		return
	}
	events := make(chan input, 32)
	frames := make(chan []byte, 1)
	status := make(chan string, 4)
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done); c.Close() }) }
	defer stop()
	go func() {
		defer stop()
		for {
			select {
			case <-done:
				return
			case b := <-frames:
				_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if c.WriteMessage(websocket.BinaryMessage, b) != nil {
					return
				}
			case v := <-status:
				_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if c.WriteMessage(websocket.TextMessage, []byte(v)) != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer stop()
		defer close(events)
		for {
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			var e input
			if c.ReadJSON(&e) != nil || !e.valid() {
				return
			}
			if e.Type == "lease" {
				b, _ := json.Marshal(map[string]any{"type": "pong", "stamp": e.Stamp})
				select {
				case status <- string(b):
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
				return
			}
		}
	}()
	send := func(b []byte) {
		if len(b) == 0 || len(b) > maxFrame {
			stop()
			return
		}
		select {
		case <-frames:
		default:
		}
		select {
		case frames <- b:
		case <-done:
		}
	}
	report := func(line string) {
		if len(line) > 2048 {
			return
		}
		if !json.Valid([]byte(line)) {
			b, _ := json.Marshal(map[string]string{"type": "status", "message": line})
			line = string(b)
		}
		select {
		case status <- line:
		case <-done:
		default:
			stop()
		}
	}
	err = s.stream(send, report, events)
	if err != nil && !errors.Is(err, net.ErrClosed) {
		report("Capture stopped: " + err.Error())
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
}
