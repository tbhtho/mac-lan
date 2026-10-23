package discovery

import (
	"context"
	"errors"
	"fmt"
	"github.com/libp2p/zeroconf/v2"
	"net"
	"sort"
	"strings"
	"time"
	"unicode"
)

type Host struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	Fingerprint string `json:"fingerprint"`
}

func private(ip net.IP) bool {
	b := ip.To4()
	return b != nil && (b[0] == 10 || b[0] == 172 && b[1] >= 16 && b[1] <= 31 || b[0] == 192 && b[1] == 168)
}
func normalize(entries []*zeroconf.ServiceEntry, now time.Time) []Host {
	found := map[string]Host{}
	conflict := map[string]bool{}
	for _, e := range entries {
		if e == nil || len(e.Instance) == 0 || len(e.Instance) > 63 || e.Port < 1024 || e.Port > 65535 || (!e.Expiry.IsZero() && !e.Expiry.After(now)) || len(e.Text) > 8 || len(e.AddrIPv4) > 16 {
			continue
		}
		bad := false
		for _, r := range e.Instance {
			if unicode.IsControl(r) {
				bad = true
			}
		}
		txt := map[string]string{}
		for _, line := range e.Text {
			parts := strings.SplitN(line, "=", 2)
			if len(line) > 255 || len(parts) != 2 {
				bad = true
				break
			}
			if _, ok := txt[parts[0]]; ok {
				bad = true
			}
			txt[parts[0]] = parts[1]
		}
		fp := strings.ToLower(txt["fp"])
		if bad || txt["v"] != "2" || len(fp) != 64 || strings.Trim(fp, "0123456789abcdef") != "" {
			continue
		}
		for _, ip := range e.AddrIPv4 {
			if !private(ip) {
				continue
			}
			if selected := txt["ip"]; selected != "" && selected != ip.String() {
				continue
			}
			address := ip.String()
			key := net.JoinHostPort(address, strconvPort(e.Port))
			if previous, ok := found[key]; ok && previous.Fingerprint != fp {
				conflict[key] = true
				delete(found, key)
			}
			if !conflict[key] && len(found) < 32 {
				found[key] = Host{e.Instance, address, e.Port, fp}
			}
		}
	}
	out := make([]Host, 0, len(found))
	for _, h := range found {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Address < out[j].Address
	})
	return out
}
func strconvPort(port int) string {
	// net.JoinHostPort requires decimal; no hostnames or URLs are accepted here.
	return fmt.Sprint(port)
}
func Browse(ctx context.Context) ([]Host, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var eligible []net.Interface
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			ip, _, er := net.ParseCIDR(address.String())
			if er == nil && private(ip) {
				eligible = append(eligible, iface)
				break
			}
		}
	}
	if len(eligible) == 0 {
		return nil, errors.New("No private LAN connection. Connect Windows to the same Wi-Fi or Ethernet as your Mac")
	}
	entries := make(chan *zeroconf.ServiceEntry, 32)
	result := make(chan error, 1)
	go func() {
		result <- zeroconf.Browse(ctx, "_macdow._tcp", "local.", entries, zeroconf.SelectIPTraffic(zeroconf.IPv4), zeroconf.SelectIfaces(eligible))
	}()
	records := make([]*zeroconf.ServiceEntry, 0, 64)
	for {
		select {
		case e, ok := <-entries:
			if !ok {
				entries = nil
			} else if len(records) < 256 {
				records = append(records, e)
			}
		case err := <-result:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				return nil, errors.New("Local discovery is unavailable. Keep MAC-DOW open on the Mac, check that both devices use the same network, then try again")
			}
			// Drain already received records, bounded and nonblocking.
			for entries != nil {
				select {
				case e, ok := <-entries:
					if !ok {
						entries = nil
					} else if len(records) < 256 {
						records = append(records, e)
					}
				default:
					entries = nil
				}
			}
			return normalize(records, time.Now()), nil
		}
	}
}
