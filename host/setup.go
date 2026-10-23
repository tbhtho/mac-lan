package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type networkCandidate struct {
	Name, Address string
	Up, Loopback  bool
}
type localInterface struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

func selectInterfaces(all []networkCandidate) []localInterface {
	result := []localInterface{}
	seen := map[string]bool{}
	for _, c := range all {
		virtual := false
		for _, prefix := range []string{"utun", "tun", "tap", "docker", "veth", "vmnet", "vbox", "lo", "tailscale", "wg"} {
			if strings.HasPrefix(strings.ToLower(c.Name), prefix) {
				virtual = true
			}
		}
		if !c.Up || c.Loopback || virtual || !privateIP(c.Address) || seen[c.Address] {
			continue
		}
		seen[c.Address] = true
		result = append(result, localInterface{c.Name, c.Address})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Address < result[j].Address
	})
	return result
}
func localInterfaces() ([]localInterface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var all []networkCandidate
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil {
				continue
			}
			all = append(all, networkCandidate{iface.Name, ip.String(), iface.Flags&net.FlagUp != 0, iface.Flags&net.FlagLoopback != 0})
		}
	}
	return selectInterfaces(all), nil
}
func chooseListen(request string, interfaces []localInterface) (string, error) {
	if request == "" || request == "auto" {
		if len(interfaces) == 0 {
			return "", errors.New("No private LAN connection found. Connect this Mac to the same Wi-Fi or Ethernet network as Windows, then Refresh")
		}
		return net.JoinHostPort(interfaces[0].Address, "0"), nil
	}
	ip, port, err := net.SplitHostPort(request)
	p, e := strconv.Atoi(port)
	if err != nil || e != nil || !privateIP(ip) || (p != 0 && p < 1024) || p > 65535 || p < 0 {
		return "", errors.New("Choose an available private LAN interface; wildcard/public addresses and privileged ports are refused")
	}
	return request, nil
}

type uiResponse struct {
	Type, ID string
	Allow    bool
}
type uiBroker struct {
	ctx      context.Context
	cancel   context.CancelFunc
	writer   io.Writer
	writeMu  sync.Mutex
	mu       sync.Mutex
	pending  string
	response chan bool
}

func newUIBroker(in io.Reader, out io.Writer) *uiBroker {
	ctx, cancel := context.WithCancel(context.Background())
	b := &uiBroker{ctx: ctx, cancel: cancel, writer: out}
	go func() {
		defer cancel()
		if closer, ok := in.(io.Closer); ok {
			defer closer.Close()
		}
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 1024), 8192)
		for scanner.Scan() {
			var r uiResponse
			if json.Unmarshal(scanner.Bytes(), &r) != nil {
				return
			}
			if r.Type == "stop" {
				return
			}
			if r.Type != "consent" {
				continue
			}
			b.mu.Lock()
			if r.ID == b.pending && b.response != nil {
				select {
				case b.response <- r.Allow:
				default:
				}
				b.pending = ""
			}
			b.mu.Unlock()
		}
	}()
	return b
}
func (b *uiBroker) emit(value map[string]any) {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if json.NewEncoder(b.writer).Encode(value) != nil {
		b.cancel()
	}
}
func (b *uiBroker) ask(kind string, fields map[string]any, timeout time.Duration) bool {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return false
	}
	id := hex.EncodeToString(random)
	response := make(chan bool, 1)
	b.mu.Lock()
	if b.pending != "" {
		b.mu.Unlock()
		return false
	}
	b.pending = id
	b.response = response
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if b.response == response {
			b.pending = ""
			b.response = nil
		}
		b.mu.Unlock()
	}()
	event := map[string]any{"type": "consent", "kind": kind, "id": id, "timeoutSeconds": int(timeout.Seconds())}
	for k, v := range fields {
		if k != "id" && k != "type" && k != "kind" {
			event[k] = v
		}
	}
	b.emit(event)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case approved := <-response:
		select {
		case <-b.ctx.Done():
			return false
		default:
			return approved
		}
	case <-timer.C:
		return false
	case <-b.ctx.Done():
		return false
	}
}

type promptLimit struct {
	mu    sync.Mutex
	start time.Time
	count int
}

func (l *promptLimit) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.start.IsZero() || now.Sub(l.start) >= time.Minute {
		l.start = now
		l.count = 0
	}
	if l.count >= 3 {
		return false
	}
	l.count++
	return true
}
func validPairNumber(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
