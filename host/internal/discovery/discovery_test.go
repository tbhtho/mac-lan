package discovery

import (
	"github.com/libp2p/zeroconf/v2"
	"net"
	"strings"
	"testing"
	"time"
)

func TestUntrustedRecordsFailClosed(t *testing.T) {
	good := &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: "My Mac"}, Port: 4433, Text: []string{"v=2", "fp=" + strings.Repeat("a", 64)}, AddrIPv4: []net.IP{net.ParseIP("192.168.1.7")}, Expiry: time.Now().Add(time.Minute)}
	got := normalize([]*zeroconf.ServiceEntry{good}, time.Now())
	if len(got) != 1 || got[0].Address != "192.168.1.7" {
		t.Fatal(got)
	}
	for _, change := range []string{"public", "expired", "fp", "version", "duplicate", "control", "port"} {
		b := *good
		b.Text = append([]string{}, good.Text...)
		switch change {
		case "public":
			b.AddrIPv4 = []net.IP{net.ParseIP("8.8.8.8")}
		case "expired":
			b.Expiry = time.Now().Add(-time.Second)
		case "fp":
			b.Text = []string{"v=2", "fp=bad"}
		case "version":
			b.Text = []string{"v=1", good.Text[1]}
		case "duplicate":
			b.Text = append(b.Text, "fp="+strings.Repeat("b", 64))
		case "control":
			b.Instance = "Mac\nforged"
		case "port":
			b.Port = 80
		}
		if len(normalize([]*zeroconf.ServiceEntry{&b}, time.Now())) != 0 {
			t.Fatal("accepted", change)
		}
	}
	if len(normalize([]*zeroconf.ServiceEntry{good, good}, time.Now())) != 1 {
		t.Fatal("not deduplicated")
	}
	conflict := *good
	conflict.Text = []string{"v=2", "fp=" + strings.Repeat("b", 64)}
	if len(normalize([]*zeroconf.ServiceEntry{good, &conflict}, time.Now())) != 0 {
		t.Fatal("ambiguous endpoint accepted")
	}
}
