package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestAutomaticLANSelection(t *testing.T) {
	c := []networkCandidate{{Name: "utun2", Address: "10.0.0.4", Up: true}, {Name: "en1", Address: "192.168.1.9", Up: true}, {Name: "en0", Address: "192.168.1.7", Up: true}, {Name: "lo0", Address: "127.0.0.1", Up: true, Loopback: true}, {Name: "en2", Address: "8.8.8.8", Up: true}, {Name: "en3", Address: "192.168.1.8", Up: false}}
	got := selectInterfaces(c)
	if len(got) != 2 || got[0].Address != "192.168.1.7" {
		t.Fatal(got)
	}
	a, err := chooseListen("", got)
	if err != nil || a != "192.168.1.7:0" {
		t.Fatal(a, err)
	}
	if _, err = chooseListen("auto", nil); err == nil {
		t.Fatal("no interface accepted")
	}
	for _, v := range []string{"0.0.0.0:4433", "8.8.8.8:4433", "localhost:4433", "192.168.1.7:80"} {
		if _, err = chooseListen(v, got); err == nil {
			t.Fatal(v)
		}
	}
}
func TestGUIConsentIDsAndEOF(t *testing.T) {
	in, writer := io.Pipe()
	out, events := io.Pipe()
	broker := newUIBroker(in, events)
	defer writer.Close()
	defer out.Close()
	event := make(chan map[string]any, 2)
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			var v map[string]any
			json.Unmarshal(s.Bytes(), &v)
			event <- v
		}
	}()
	answer := make(chan bool, 1)
	go func() { answer <- broker.ask("pair", map[string]any{"code": "123456"}, time.Second) }()
	e := <-event
	id := e["id"].(string)
	io.WriteString(writer, "{\"type\":\"consent\",\"id\":\"stale\",\"allow\":true}\n")
	select {
	case <-answer:
		t.Fatal("stale consent accepted")
	case <-time.After(10 * time.Millisecond):
	}
	json.NewEncoder(writer).Encode(map[string]any{"type": "consent", "id": id, "allow": true})
	if !<-answer {
		t.Fatal("matching consent rejected")
	}
	go func() { answer <- broker.ask("stream", nil, time.Second) }()
	<-event
	writer.Close()
	if <-answer {
		t.Fatal("EOF granted consent")
	}
	select {
	case <-broker.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("EOF did not stop host")
	}
}
func TestGUIConsentTimeoutAndMalformed(t *testing.T) {
	in, w := io.Pipe()
	b := newUIBroker(in, io.Discard)
	if b.ask("pair", nil, 5*time.Millisecond) {
		t.Fatal("timeout approved")
	}
	io.WriteString(w, strings.Repeat("x", 9000)+"\n")
	select {
	case <-b.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("oversized UI message not closed")
	}
	w.Close()
}
func TestPairPromptRateLimit(t *testing.T) {
	l := promptLimit{}
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.allow(now) {
			t.Fatal("valid prompt blocked")
		}
	}
	if l.allow(now) {
		t.Fatal("unbounded prompts")
	}
	if !l.allow(now.Add(time.Minute)) {
		t.Fatal("rate did not reset")
	}
}
