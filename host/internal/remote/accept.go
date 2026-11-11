package remote

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// Accept upgrades an already authorized request on the Mac's pinned listener to
// the remote-command link and serves it until it closes. The caller must have
// verified the paired peer and its bearer token first.
func (n *Node) Accept(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10, HandshakeTimeout: 5 * time.Second,
		CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	n.Attach(conn)
}

// Drop closes the current link, for example when the pairing it belongs to ends.
func (n *Node) Drop() {
	if l := n.current(); l != nil {
		l.close()
	}
}

// Connected reports whether the other computer is linked right now.
func (n *Node) Connected() bool { return n.current() != nil }
