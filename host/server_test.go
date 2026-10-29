package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func connectFixture(t *testing.T, s *server) *websocket.Conn {
	t.Helper()
	ts := httptest.NewTLSServer(s)
	t.Cleanup(ts.Close)
	roots := x509.NewCertPool()
	roots.AddCert(ts.Certificate())
	d := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: roots}}
	c, _, err := d.Dial("wss"+strings.TrimPrefix(ts.URL, "https"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); s.stopActive() })
	if err := c.WriteJSON(map[string]string{"type": "auth", "token": s.token}); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var catalog struct {
		Type   string
		MaxFPS int `json:"maxFps"`
	}
	if err := c.ReadJSON(&catalog); err != nil || catalog.Type != "catalog" || catalog.MaxFPS != 60 {
		t.Fatalf("catalog: %+v, %v", catalog, err)
	}
	return c
}

func TestPairedIdleReleaseAndLeaseDoNotEndSessionOrStartCapture(t *testing.T) {
	consentCalled := make(chan start, 1)
	allow := make(chan bool, 1)
	streamCalled := make(chan struct{}, 1)
	s := &server{token: strings.Repeat("c", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
		catalog: func() ([]window, error) { return nil, nil },
		consent: func(r start) bool { consentCalled <- r; return <-allow },
		stream: func(send func([]byte), _ func(string), events <-chan input) error {
			streamCalled <- struct{}{}
			send([]byte{1, 2, 3})
			for range events {
			}
			return nil
		},
	}
	c := connectFixture(t, s)
	for _, idle := range []input{{Type: "release"}, {Type: "lease", Active: true, View: true, Stamp: 10, Generation: 1}, {Type: "release", Generation: 1}} {
		if err := c.WriteJSON(idle); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-consentCalled:
		t.Fatal("idle safety messages requested viewing consent")
	case <-streamCalled:
		t.Fatal("idle safety messages started capture")
	default:
	}
	request := start{Type: "start", FPS: 60, Width: 1280, Quality: 70, Codec: "h264", Bitrate: 8}
	if err := c.WriteJSON(request); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-consentCalled:
		if got != request {
			t.Fatalf("view consent request: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("safety release before start disconnected the paired session")
	}
	select {
	case <-streamCalled:
		t.Fatal("capture started before local viewing approval")
	default:
	}
	allow <- true
	for {
		kind, frame, err := c.ReadMessage()
		if err != nil {
			t.Fatal("approved stream", err)
		}
		if kind == websocket.BinaryMessage {
			if !bytes.Equal(frame, []byte{1, 2, 3}) {
				t.Fatal("wrong fixture frame")
			}
			break
		}
	}
}

func TestPairedIdleInputCannotRequestConsentOrStartCapture(t *testing.T) {
	for _, invalid := range []input{{Type: "key", Code: 55, Down: true}, {Type: "release", Generation: 9007199254740992}} {
		t.Run(invalid.Type, func(t *testing.T) {
			s := &server{token: strings.Repeat("d", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
				catalog: func() ([]window, error) { return nil, nil },
				consent: func(start) bool { t.Error("unrequested consent"); return true },
				stream:  func(func([]byte), func(string), <-chan input) error { t.Error("unapproved capture"); return nil },
			}
			c := connectFixture(t, s)
			if err := c.WriteJSON(invalid); err != nil {
				t.Fatal(err)
			}
			for {
				_, _, err := c.ReadMessage()
				if err != nil {
					break
				}
			}
		})
	}
}

func TestCaptureFailureReachesClientBeforeConnectionEnds(t *testing.T) {
	s := &server{token: strings.Repeat("e", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
		catalog: func() ([]window, error) { return nil, nil },
		consent: func(start) bool { return true },
		stream: func(func([]byte), func(string), <-chan input) error {
			return errors.New("native helper fixture failure")
		},
	}
	c := connectFixture(t, s)
	if err := c.WriteJSON(start{Type: "start", FPS: 60, Width: 1280, Quality: 70}); err != nil {
		t.Fatal(err)
	}
	for {
		var message struct{ Type, Message string }
		if err := c.ReadJSON(&message); err != nil {
			t.Fatal("capture error was lost before disconnect", err)
		}
		if message.Type == "error" && strings.Contains(message.Message, "native helper fixture failure") {
			break
		}
	}
}

func TestNativeStatusBurstDoesNotDisconnectApprovedCapture(t *testing.T) {
	s := &server{token: strings.Repeat("f", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
		catalog: func() ([]window, error) { return nil, nil },
		consent: func(start) bool { return true },
		stream: func(send func([]byte), status func(string), events <-chan input) error {
			for i := 0; i < 1000; i++ {
				status(`{"type":"status","paused":false,"message":"Encoder property fixture unsupported"}`)
			}
			send([]byte{1, 2, 3})
			for range events {
			}
			return nil
		},
	}
	c := connectFixture(t, s)
	if err := c.WriteJSON(start{Type: "start", FPS: 60, Width: 1280, Quality: 70}); err != nil {
		t.Fatal(err)
	}
	for {
		kind, _, err := c.ReadMessage()
		if err != nil {
			t.Fatal("native status burst disconnected the approved capture", err)
		}
		if kind == websocket.BinaryMessage {
			return
		}
	}
}

func TestInputPressureDoesNotEndApprovedCapture(t *testing.T) {
	streaming := make(chan struct{})
	s := &server{token: strings.Repeat("2", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
		catalog: func() ([]window, error) { return nil, nil },
		consent: func(start) bool { return true },
		stream: func(send func([]byte), status func(string), events <-chan input) error {
			send([]byte{1, 2, 3})
			close(streaming)
			// A stalled native helper must not turn viewer input pressure into a
			// disconnect; the session keeps video and control state alive.
			time.Sleep(250 * time.Millisecond)
			for e := range events {
				if e.Type == "release" {
					send([]byte{9})
					// Keep the session up while the client reads its proof frame.
					for range events {
					}
					return nil
				}
			}
			return nil
		},
	}
	c := connectFixture(t, s)
	if err := c.WriteJSON(start{Type: "start", FPS: 60, Width: 1280, Quality: 70}); err != nil {
		t.Fatal(err)
	}
	<-streaming
	for i := 0; i < 200; i++ {
		if err := c.WriteJSON(input{Type: "move", X: 0.5, Y: 0.5}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(350 * time.Millisecond)
	if err := c.WriteJSON(input{Type: "release"}); err != nil {
		t.Fatalf("input pressure closed the approved session: %v", err)
	}
	for {
		kind, frame, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("approved capture ended after input pressure: %v", err)
		}
		if kind == websocket.BinaryMessage && bytes.Equal(frame, []byte{9}) {
			return
		}
	}
}

func TestInputPressureReleasesHeldEdgesBeforeRenewedLease(t *testing.T) {
	for _, scenario := range []struct {
		kind       string
		rounds     int
		focusLoss  bool
		disconnect bool
	}{
		{"key", 1, false, false}, {"button", 1, false, false}, {"key", 3, false, false}, {"button", 3, false, false}, {"key", 3, true, false}, {"button", 3, true, false}, {"key", 3, true, true}, {"button", 3, true, true},
		{"pending-keyframe", 3, false, false},
	} {
		kind := scenario.kind
		preserveKeyframe := kind == "pending-keyframe"
		if preserveKeyframe {
			kind = "key"
		}
		t.Run(scenario.kind+"-rounds-"+strconv.Itoa(scenario.rounds)+"-focus-loss-"+strconv.FormatBool(scenario.focusLoss)+"-disconnect-"+strconv.FormatBool(scenario.disconnect), func(t *testing.T) {
			held := make(chan struct{})
			resume := make(chan struct{})
			observed := make(chan []input, 1)
			s := &server{token: strings.Repeat("2", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
				catalog: func() ([]window, error) { return nil, nil }, consent: func(start) bool { return true },
				stream: func(send func([]byte), _ func(string), events <-chan input) error {
					first, ok := <-events
					if !ok || first.Type != kind || !first.Down {
						return errors.New("missing fixture press")
					}
					close(held)
					<-resume
					var batch []input
					for e := range events {
						batch = append(batch, e)
						if e.Type == "lease" {
							observed <- batch
							send([]byte{9})
							for range events {
							}
							return nil
						}
					}
					return nil
				},
			}
			c := connectFixture(t, s)
			// Cleanup unblocks the fixture even if an assertion below fails.
			t.Cleanup(func() {
				select {
				case <-resume:
				default:
					close(resume)
				}
			})
			for _, e := range []any{start{Type: "start", FPS: 60, Width: 1280, Quality: 70}, input{Type: kind, Code: 55, Button: 0, Down: true}} {
				if err := c.WriteJSON(e); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-held:
			case <-time.After(time.Second):
				t.Fatal("fixture did not hold input")
			}
			// A competing controller cannot own another producer or receive this
			// session's recovery release while its consumer is stalled.
			rejected := httptest.NewRecorder()
			s.ServeHTTP(rejected, httptest.NewRequest("GET", "https://fixture/", nil))
			if rejected.Code != 409 {
				t.Fatalf("second controller was not rejected: %d", rejected.Code)
			}
			for round := 0; round < scenario.rounds; round++ {
				if preserveKeyframe {
					for i := 0; i < 3; i++ {
						if err := c.WriteJSON(input{Type: "keyframe"}); err != nil {
							t.Fatal(err)
						}
					}
				}
				for i := 0; i < 32; i++ {
					if err := c.WriteJSON(input{Type: "move", X: 0.5, Y: 0.5}); err != nil {
						t.Fatal(err)
					}
				}
				edge := input{Type: kind, Code: 55, Button: 0, Down: false}
				if scenario.focusLoss {
					edge = input{Type: "release", Reason: "foreground-lost"}
				}
				if err := c.WriteJSON(edge); err != nil {
					t.Fatal(err)
				}
				if err := c.WriteJSON(input{Type: "lease", Active: true, View: true, Generation: 1, Stamp: int64(round + 1)}); err != nil {
					t.Fatal(err)
				}
				// Receiving this pong proves the reader passed the up's timeout. No
				// scheduler-dependent sleep is needed to hold the native consumer.
				for {
					var message map[string]any
					if err := c.ReadJSON(&message); err != nil {
						t.Fatal(err)
					}
					if message["type"] == "pong" {
						if message["stamp"] != float64(round+1) {
							t.Fatal("out-of-order lease acknowledgement")
						}
						break
					}
				}
			}
			s.connectionMu.Lock()
			connectionDone := s.activeDone
			s.connectionMu.Unlock()
			if scenario.disconnect {
				c.Close()
			}
			close(resume)
			select {
			case batch := <-observed:
				var types []string
				for _, event := range batch {
					types = append(types, event.Type)
				}
				want := "release,lease"
				if preserveKeyframe {
					want = "release,keyframe,lease"
				}
				if strings.Join(types, ",") != want {
					t.Fatalf("overflow must discard stale input and release before renewed lease, got %v", batch)
				}
			case <-time.After(time.Second):
				t.Fatal("native input did not resume")
			}
			if scenario.disconnect {
				select {
				case <-connectionDone:
				case <-time.After(time.Second):
					t.Fatal("disconnect during recovery stranded the session")
				}
				select {
				case s.slot <- struct{}{}:
					<-s.slot
				case <-time.After(time.Second):
					t.Fatal("disconnected session retained controller ownership")
				}
				return
			}
			kind, frame, err := c.ReadMessage()
			if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(frame, []byte{9}) {
				t.Fatalf("video did not survive input pressure: %v", err)
			}
		})
	}
}

func TestPrivateLANOnly(t *testing.T) {
	for _, s := range []string{"10.1.2.3", "172.16.1.1", "192.168.1.5"} {
		if !privateIP(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"0.0.0.0", "127.0.0.1", "8.8.8.8", "localhost", "::1", "172.32.0.1", "192.168.1.1:443"} {
		if privateIP(s) {
			t.Fatal(s)
		}
	}
}
func TestConsentReportsRequestedCodec(t *testing.T) {
	for _, test := range []struct {
		request start
		want    string
	}{
		{start{}, "hardware H.264 / 8 Mbit/s"},
		{start{Codec: "h264", Bitrate: 12}, "hardware H.264 / 12 Mbit/s"},
		{start{Codec: "jpeg", Quality: 70}, "JPEG quality 70"},
	} {
		if got := streamDescription(test.request); got != test.want {
			t.Fatalf("consent: got %q, want %q", got, test.want)
		}
	}
}
func TestFreshTLSIdentity(t *testing.T) {
	a, fp, err := identity()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := identity()
	sum := sha256.Sum256(a.Certificate[0])
	if fp != hex.EncodeToString(sum[:]) {
		t.Fatal("fingerprint")
	}
	if bytes.Equal(a.Certificate[0], b.Certificate[0]) {
		t.Fatal("reused identity")
	}
	cert, err := x509.ParseCertificate(a.Certificate[0])
	if err != nil || cert.NotAfter.Before(time.Now()) {
		t.Fatal("invalid cert")
	}
}
func TestInputValidation(t *testing.T) {
	for _, m := range []input{{Type: "lease", Active: true}, {Type: "release"}, {Type: "key", Code: 55, Down: true}, {Type: "move", X: .5, Y: .5}, {Type: "button", Button: 0, Down: true}, {Type: "scroll", Delta: -120}} {
		if !m.valid() {
			t.Fatalf("rejected %+v", m)
		}
	}
	for _, m := range []input{{Type: "shell"}, {Type: "key", Code: 65536}, {Type: "move", X: 1.5}, {Type: "button", Button: 3}, {Type: "scroll", Delta: 100000}} {
		if m.valid() {
			t.Fatalf("accepted %+v", m)
		}
	}
}
func TestAuthenticatedConsentAndFrames(t *testing.T) {
	consent := 0
	s := &server{token: strings.Repeat("a", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
		catalog: func() ([]window, error) { return []window{{ID: 42, Title: "Editor", App: "Example"}}, nil },
		consent: func(start) bool { consent++; return true },
		stream: func(send func([]byte), status func(string), events <-chan input) error {
			send([]byte{0xff, 0xd8, 0xff, 0xd9})
			for e := range events {
				if e.Type == "release" {
					break
				}
			}
			return nil
		},
	}
	ts := httptest.NewTLSServer(s)
	defer ts.Close()
	d := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()}}
	d.TLSClientConfig.RootCAs.AddCert(ts.Certificate())
	connect := func() *websocket.Conn {
		c, _, err := d.Dial("wss"+strings.TrimPrefix(ts.URL, "https"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := connect()
	c.WriteJSON(map[string]string{"type": "auth", "token": "wrong"})
	_, _, err := c.ReadMessage()
	c.Close()
	if err == nil || consent != 0 {
		t.Fatal("unauthenticated access")
	}
	c = connect()
	defer c.Close()
	c.WriteJSON(map[string]string{"type": "auth", "token": s.token})
	_, catalog, err := c.ReadMessage()
	if err != nil || !bytes.Contains(catalog, []byte("Editor")) {
		t.Fatal("catalog", err)
	}
	c.WriteJSON(start{Type: "start", Window: 42, FPS: 60, Width: 1280, Quality: 70})
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	gotFrame := false
	for i := 0; i < 4; i++ {
		kind, data, err := c.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.BinaryMessage {
			gotFrame = bytes.Equal(data, []byte{0xff, 0xd8, 0xff, 0xd9})
			break
		}
	}
	if !gotFrame || consent != 1 {
		t.Fatal("missing authenticated consented frame")
	}
	c.WriteJSON(input{Type: "release"})
}
func TestNoBrowserOrigins(t *testing.T) {
	s := &server{token: "x", peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1)}
	ts := httptest.NewServer(s)
	defer ts.Close()
	d := websocket.Dialer{}
	h := mapHeader("https://evil.example")
	c, resp, err := d.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), h)
	if c != nil {
		c.Close()
	}
	if err == nil || resp.StatusCode != 403 {
		t.Fatal("origin accepted")
	}
}
func mapHeader(origin string) map[string][]string { return map[string][]string{"Origin": {origin}} }
func TestStartBoundsAndClosedWindow(t *testing.T) {
	ws := []window{{ID: 42}}
	for _, s := range []start{{Type: "start", FPS: 60, Width: 1280, Quality: 70, Codec: "h264"}, {Type: "start", FPS: 60, Width: 1280, Quality: 70, Codec: "jpeg"}, {Type: "start", FPS: 60, Width: 1600, Quality: 70}, {Type: "start", Window: 42, FPS: 60, Width: 800, Quality: 40}} {
		if !s.valid(ws) {
			t.Fatal(s)
		}
	}
	for _, s := range []start{{Type: "start", Window: 99, FPS: 60, Width: 1600, Quality: 70}, {Type: "start", FPS: 61, Width: 1600, Quality: 70}, {Type: "start", FPS: 120, Width: 1600, Quality: 70}, {Type: "start", FPS: 60, Width: 8000, Quality: 70}} {
		if s.valid(ws) {
			b, _ := json.Marshal(s)
			t.Fatal(string(b))
		}
	}
}

func TestSub60RequestsNeverAskConsentOrStartCapture(t *testing.T) {
	for _, fps := range []int{0, 5, 10, 15, 30, 59} {
		t.Run(strconv.Itoa(fps)+"-fps", func(t *testing.T) {
			s := &server{token: strings.Repeat("g", 64), peerAllowed: func(string) bool { return true }, slot: make(chan struct{}, 1),
				catalog: func() ([]window, error) { return nil, nil },
				consent: func(start) bool { t.Error("sub60 request asked for consent"); return true },
				stream: func(func([]byte), func(string), <-chan input) error {
					t.Error("sub60 request started capture")
					return nil
				},
			}
			c := connectFixture(t, s)
			if err := c.WriteJSON(start{Type: "start", FPS: fps, Width: 1280, Quality: 70, Codec: "h264"}); err != nil {
				t.Fatal(err)
			}
			var response struct{ Type, Message string }
			if err := c.ReadJSON(&response); err != nil || response.Type != "error" || !strings.Contains(response.Message, "60 fps") {
				t.Fatalf("sub60 request must give an actionable 60fps error: %+v, %v", response, err)
			}
		})
	}
}
