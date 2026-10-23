package main

import (
	"encoding/json"
	"testing"
)

func TestHeldStateRejectsArbitraryInput(t *testing.T) {
	good := []byte("{\"type\":\"held\",\"keys\":[55,48],\"buttons\":[0],\"x\":10,\"y\":20}")
	h, ok := parseHeld(good)
	if !ok || len(h.Keys) != 2 || len(h.Buttons) != 1 {
		t.Fatal("valid held-state lost")
	}
	for _, s := range []string{
		"{\"type\":\"other\",\"keys\":[55]}",
		"{\"type\":\"held\",\"keys\":[999]}",
		"{\"type\":\"held\",\"buttons\":[3]}",
		"{\"type\":\"held\",\"keys\":[55,55]}",
	} {
		if _, ok := parseHeld([]byte(s)); ok {
			t.Fatal("bad recovery state accepted", s)
		}
	}
	b, _ := json.Marshal(h)
	if len(b) > 4096 {
		t.Fatal("recovery bound")
	}
}
