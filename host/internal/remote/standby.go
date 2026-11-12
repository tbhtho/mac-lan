package remote

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// The standby link keeps commands working while mac-lan itself is restarting,
// updating or not sharing. On the Mac a small service, installed only when its
// user asks, runs from its own copy of the program under launchd and serves the
// same remote-command protocol on its own port with its own certificate and
// token. The PC learns those over the normal authenticated link and falls back
// to them whenever that link is down. The Mac's "run commands here" switch
// governs the standby link exactly as it does the normal one.

// StandbyOffer is what the Mac's standby service listens with.
type StandbyOffer struct {
	Port        int    `json:"port"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint"`
}

// StandbyDir is the service's private folder inside mac-lan's.
func StandbyDir(dir string) string { return filepath.Join(dir, "standby") }

func offerPath(dir string) string { return filepath.Join(StandbyDir(dir), "standby.json") }

func validOffer(o StandbyOffer) bool {
	pin, err := hex.DecodeString(o.Fingerprint)
	return err == nil && len(pin) == 32 && len(o.Token) == 64 && validText(o.Token, 64) && o.Port >= 1024 && o.Port <= 65535
}

// ReadStandbyOffer returns the running service's details, if one is set up.
func ReadStandbyOffer(dir string) (StandbyOffer, bool) {
	var offer StandbyOffer
	if dir == "" {
		return offer, false
	}
	data, err := os.ReadFile(offerPath(dir))
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &offer) != nil {
		return offer, false
	}
	return offer, validOffer(offer)
}

// WriteStandbyOffer records the service's details for the Mac's own mac-lan.
func WriteStandbyOffer(dir string, offer StandbyOffer) error {
	if !validOffer(offer) {
		return errors.New("invalid standby details")
	}
	data, _ := json.Marshal(offer)
	return writePrivate(offerPath(dir), data)
}

// standbyRoute is what the PC remembers about the Mac's standby service.
type standbyRoute struct {
	Addresses   []string `json:"addresses"`
	Name        string   `json:"name"`
	Port        int      `json:"port"`
	Fingerprint string   `json:"fingerprint"`
	Token       string   `json:"token"`
}

func routePath(dir string) string { return filepath.Join(dir, "remote-standby.json") }

func readRoute(dir string) (standbyRoute, bool) {
	var route standbyRoute
	if dir == "" {
		return route, false
	}
	data, err := os.ReadFile(routePath(dir))
	if err != nil || len(data) > 8192 || json.Unmarshal(data, &route) != nil {
		return route, false
	}
	return route, validOffer(StandbyOffer{Port: route.Port, Token: route.Token, Fingerprint: route.Fingerprint})
}

// ForgetStandby removes what this computer remembers about the Mac's standby
// service, for example when the Mac is forgotten.
func ForgetStandby(dir string) {
	if dir != "" {
		_ = os.Remove(routePath(dir))
	}
}

func localName() string {
	name, err := os.Hostname()
	if err != nil || name == "" || !validText(name, 200) {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(name), ".local") {
		name += ".local"
	}
	return name
}

func (l *link) handleStandby(m msg) {
	if l.disabled(m) {
		return
	}
	offer, ok := ReadStandbyOffer(l.node.dir)
	if !ok {
		l.reply(m, msg{Code: "none", Err: "no standby link is set up on " + l.node.name})
		return
	}
	l.node.log.record("standby", m.Origin, "", "details sent to the paired computer")
	l.reply(m, msg{OK: true, Width: offer.Port, Sha: offer.Fingerprint, D: offer.Token, Name: localName()})
}

// learnStandby asks the Mac, once per connection, whether it runs a standby
// service and remembers how to reach it.
func (l *link) learnStandby() {
	n := l.node
	if n.dir == "" {
		return
	}
	reply, err := l.call(msg{T: "standby"}, 20*time.Second)
	if err != nil {
		return // an older mac-lan does not answer
	}
	if !reply.OK {
		if reply.Code == "none" {
			ForgetStandby(n.dir)
		}
		return
	}
	route := standbyRoute{Name: reply.Name, Port: reply.Width, Fingerprint: reply.Sha, Token: reply.D}
	if !validOffer(StandbyOffer{Port: route.Port, Token: route.Token, Fingerprint: route.Fingerprint}) || !validText(route.Name, 255) {
		return
	}
	host, _, err := net.SplitHostPort(l.c.RemoteAddr().String())
	if err != nil || !privateIPv4(host) {
		return
	}
	route.Addresses = []string{host}
	if old, ok := readRoute(n.dir); ok && old.Fingerprint == route.Fingerprint {
		for _, address := range old.Addresses {
			if address != host && privateIPv4(address) && len(route.Addresses) < 3 {
				route.Addresses = append(route.Addresses, address)
			}
		}
	}
	data, _ := json.Marshal(route)
	_ = writePrivate(routePath(n.dir), data)
}

// dialRemote opens the remote-command WebSocket of a pinned listener.
func dialRemote(ctx context.Context, address string, port int, pin []byte, token string, handshake time.Duration) (*websocket.Conn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: handshake, Proxy: nil, ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, // the pinned fingerprint below is the check
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 {
					return errors.New("no certificate")
				}
				sum := sha256.Sum256(raw[0])
				if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
					return errors.New("this is not the paired Mac")
				}
				return nil
			}}}
	conn, _, err := dialer.DialContext(ctx, "wss://"+net.JoinHostPort(address, strconv.Itoa(port))+"/remote", http.Header{"Authorization": {"Bearer " + token}})
	return conn, err
}

// candidates are the addresses to try: the ones the Mac was last seen at, then
// whatever its Bonjour name resolves to now.
func (r standbyRoute) candidates(ctx context.Context) []string {
	var out []string
	seen := map[string]bool{}
	add := func(address string) {
		if privateIPv4(address) && !seen[address] {
			seen[address] = true
			out = append(out, address)
		}
	}
	for _, address := range r.Addresses {
		add(address)
	}
	if r.Name != "" {
		lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
		if found, err := net.DefaultResolver.LookupIP(lookup, "ip4", r.Name); err == nil {
			for _, ip := range found {
				add(ip.String())
			}
		}
		cancel()
	}
	return out
}

// standbyLoop connects through the Mac's standby service whenever there is no
// link and nothing is waiting to be picked up again on the normal one.
func (s *Server) standbyLoop(ctx context.Context, dir string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
		if s.node.current() != nil || s.node.awaitingReattach() {
			continue
		}
		route, ok := readRoute(dir)
		if !ok {
			continue
		}
		pin, _ := hex.DecodeString(route.Fingerprint)
		for _, address := range route.candidates(ctx) {
			conn, err := dialRemote(ctx, address, route.Port, pin, route.Token, 4*time.Second)
			if err != nil {
				continue
			}
			if s.node.current() != nil { // the normal link returned meanwhile
				_ = conn.Close()
				break
			}
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			s.node.attach(conn, true)
			stop()
			break
		}
	}
}

// awaitingReattach reports commands that were running when the normal link
// dropped and may still be picked up when it returns.
func (n *Node) awaitingReattach() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, j := range n.jobs {
		j.mu.Lock()
		waiting := j.detached && !finished(j.state)
		j.mu.Unlock()
		if waiting {
			return true
		}
	}
	return false
}

// standbyBusy reports work in progress over the standby link; the normal link
// does not take over until it is done, because the two are served by different
// processes on the Mac and a command cannot move between them.
func (n *Node) standbyBusy() bool {
	l := n.current()
	if l == nil || !l.standby {
		return false
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

// ServeStandby authorizes and serves one connection to the Mac's standby
// service: the caller must hold the service's token and be on a private address.
func (n *Node) ServeStandby(w http.ResponseWriter, r *http.Request, token string) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err != nil || !privateIPv4(host) || r.Method != http.MethodGet || r.URL.Path != "/remote" ||
		len(given) != len(token) || len(token) != 64 || subtle.ConstantTimeCompare([]byte(given), []byte(token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	n.Accept(w, r)
}

// HasStandby reports whether this computer remembers a standby link to try.
func HasStandby(dir string) bool {
	_, ok := readRoute(dir)
	return ok
}
