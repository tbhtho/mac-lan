package discovery

import (
	"strings"
	"testing"
)

func TestAdvertisementContainsOnlyPublicPairingIdentity(t *testing.T) {
	record, err := advertisement("192.168.1.2", 57943, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(record.Text, " ")
	if record.Port != 57943 || !strings.Contains(text, "v=2") || !strings.Contains(text, "ip=192.168.1.2") || !strings.Contains(text, "fp="+strings.Repeat("a", 64)) {
		t.Fatal(record)
	}
	if strings.Contains(strings.ToLower(text), "token") {
		t.Fatal("credential advertised")
	}
	for _, address := range []string{"0.0.0.0", "127.0.0.1", "8.8.8.8", "example.com"} {
		if _, err = advertisement(address, 57943, strings.Repeat("a", 64)); err == nil {
			t.Fatal(address)
		}
	}
	if _, err = advertisement("192.168.1.2", 80, strings.Repeat("a", 64)); err == nil {
		t.Fatal("privileged port")
	}
	if _, err = advertisement("192.168.1.2", 57943, "bad"); err == nil {
		t.Fatal("invalid identity")
	}
}
