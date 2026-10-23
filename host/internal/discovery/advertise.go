package discovery

import (
	"errors"
	"github.com/libp2p/zeroconf/v2"
	"net"
	"strings"
)

type advert struct {
	Port int
	Text []string
	Host string
}

func advertisement(address string, port int, fingerprint string) (advert, error) {
	if !private(net.ParseIP(address)) || port < 1024 || port > 65535 || len(fingerprint) != 64 || strings.Trim(fingerprint, "0123456789abcdef") != "" {
		return advert{}, errors.New("invalid LAN advertisement")
	}
	return advert{port, []string{"v=2", "fp=" + fingerprint, "ip=" + address}, "macdow-" + fingerprint[:12] + ".local."}, nil
}

// CLI hosts publish too; UI publication is no longer required for Find my Mac.
func Advertise(address string, port int, fingerprint string) (func(), error) {
	record, err := advertisement(address, port, fingerprint)
	if err != nil {
		return nil, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var selected []net.Interface
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, a := range addresses {
			ip, _, err := net.ParseCIDR(a.String())
			if err == nil && ip.String() == address {
				selected = append(selected, iface)
				break
			}
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("selected LAN interface disappeared")
	}
	server, err := zeroconf.RegisterProxy("MacBook", "_macdow._tcp", "local.", record.Port, record.Host, []string{address}, record.Text, selected)
	if err != nil {
		return nil, err
	}
	return server.Shutdown, nil
}
