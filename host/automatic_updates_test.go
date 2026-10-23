package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAutomaticUpdateOfferAndWorkerAreExplicitAndIdle(t *testing.T) {
	for _, allow := range []bool{false, true} {
		s := &server{}
		prompted, scheduled := 0, 0
		accepted, err := offerWhenIdle(context.Background(), s, func() bool { prompted++; return allow }, func() error { scheduled++; return nil })
		if err != nil || accepted != allow || prompted != 1 || scheduled != map[bool]int{false: 0, true: 1}[allow] {
			t.Fatalf("restart choice mismatch: %v %v %d %d", accepted, err, prompted, scheduled)
		}
	}
	s := &server{active: &websocket.Conn{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	accepted, err := offerWhenIdle(ctx, s, func() bool { t.Error("active pairing was interrupted by an update offer"); return true }, func() error { t.Error("active pairing was interrupted by an updater"); return nil })
	if accepted || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("active connection not protected: %v %v", accepted, err)
	}
}

func TestAutomaticUpdateWorkerFailureDoesNotRequestShutdown(t *testing.T) {
	s := &server{}
	accepted, err := offerWhenIdle(context.Background(), s, func() bool { return true }, func() error {
		if !s.updating {
			t.Error("idle host was not reserved during verification")
		}
		return errors.New("synthetic verification failure")
	})
	if accepted || err == nil {
		t.Fatal("unverified update requested host shutdown")
	}
	if s.updating {
		t.Fatal("failed update left the host unavailable for pairing")
	}
}

func TestAutomaticUpdateNewPairingDuringPromptStaysConnected(t *testing.T) {
	s := &server{}
	accepted, err := offerWhenIdle(context.Background(), s, func() bool {
		s.connectionMu.Lock()
		s.active = &websocket.Conn{}
		s.connectionMu.Unlock()
		return true
	}, func() error { t.Error("update interrupted a pairing that arrived during the dialog"); return nil })
	if accepted || err == nil || s.updating {
		t.Fatal("idle reservation missed a new pairing")
	}
}
