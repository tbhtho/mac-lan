package main

import (
	"testing"
	"time"
)

func TestResumeTokenIsPeerBoundSingleUseAndExpires(t *testing.T) {
	s := &server{}
	now := time.Now()
	if _, err := s.issueResume("192.168.1.20", now); err != nil {
		t.Fatal(err)
	}
	s.resumes = nil
	token, _ := s.issueResume("192.168.1.20", now)
	if s.redeemResume("192.168.1.20", token, now) {
		t.Fatal("a peer that was never approved must not resume")
	}
	s.approve("192.168.1.20")
	token, _ = s.issueResume("192.168.1.20", now)
	if s.redeemResume("192.168.1.21", token, now) {
		t.Fatal("a token must not work from another address")
	}
	if s.redeemResume("192.168.1.20", token[:63]+"0", now) && token[63] != '0' {
		t.Fatal("a wrong token must be rejected")
	}
	if !s.redeemResume("192.168.1.20", token, now) {
		t.Fatal("the issued token must resume once")
	}
	if s.redeemResume("192.168.1.20", token, now) {
		t.Fatal("a token must be single-use")
	}
	token, _ = s.issueResume("192.168.1.20", now)
	if s.redeemResume("192.168.1.20", token, now.Add(resumeLifetime+time.Second)) {
		t.Fatal("an expired token must be rejected")
	}
}

func TestSamePeerTakesOverStaleSlot(t *testing.T) {
	s := &server{slot: make(chan struct{}, 1)}
	stale := keepAlivePair(t, true)
	done := make(chan struct{})
	s.slot <- struct{}{}
	s.active, s.activeDone, s.activePeer = stale, done, "192.168.1.20"
	go func() {
		for {
			if _, _, err := stale.ReadMessage(); err != nil {
				<-s.slot
				close(done)
				return
			}
		}
	}()
	if s.tryTakeSlot() || s.activeFrom("192.168.1.30") || s.takeOverSlot("192.168.1.30") {
		t.Fatal("another PC must not take over an active session")
	}
	if !s.activeFrom("192.168.1.20") || !s.takeOverSlot("192.168.1.20") {
		t.Fatal("the same PC must replace its own stale connection")
	}
}
