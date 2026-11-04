package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func keepAlivePair(t *testing.T, answer bool) *websocket.Conn {
	t.Helper()
	server := make(chan *websocket.Conn, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		server <- c
	}))
	t.Cleanup(h.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	if !answer {
		client.SetPingHandler(func(string) error { return nil })
	}
	go func() {
		for {
			if _, _, err := client.ReadMessage(); err != nil {
				return
			}
		}
	}()
	return <-server
}

func readsUntilClosed(c *websocket.Conn, within time.Duration) bool {
	_ = c.SetReadDeadline(time.Now().Add(within))
	_, _, err := c.ReadMessage()
	return err != nil && !strings.Contains(err.Error(), "timeout")
}

func TestKeepAliveClosesSilentPeer(t *testing.T) {
	c := keepAlivePair(t, false)
	defer keepAlive(c, 20*time.Millisecond, 100*time.Millisecond)()
	if !readsUntilClosed(c, 2*time.Second) {
		t.Fatal("a peer that never answers pings must be disconnected")
	}
}

func TestKeepAliveKeepsResponsivePeer(t *testing.T) {
	c := keepAlivePair(t, true)
	defer keepAlive(c, 20*time.Millisecond, 100*time.Millisecond)()
	if readsUntilClosed(c, 500*time.Millisecond) {
		t.Fatal("a peer answering pings must stay connected")
	}
}
