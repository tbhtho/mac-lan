package main

import (
	"context"
	"errors"
	"testing"

	"github.com/gorilla/websocket"
)

func TestAutomaticUpdateSchedulesWhileSharing(t *testing.T) {
	s := &server{active: &websocket.Conn{}}
	accepted, err := scheduleAutomaticUpdate(context.Background(), s, func() error {
		if !s.updating || s.active == nil {
			t.Error("not reserved with sharing intact")
		}
		return nil
	})
	if err != nil || !accepted || !s.updating {
		t.Fatal(accepted, err)
	}
}
func TestAutomaticUpdateWorkerFailureCanRetry(t *testing.T) {
	s := &server{}
	accepted, err := scheduleAutomaticUpdate(context.Background(), s, func() error { return errors.New("temporary verification failure") })
	if accepted || err == nil || s.updating {
		t.Fatal("failure requested shutdown or blocked pairing")
	}
	accepted, err = scheduleAutomaticUpdate(context.Background(), s, func() error { return nil })
	if !accepted || err != nil {
		t.Fatal("cannot retry", err)
	}
}
func TestAutomaticUpdateDoesNotScheduleAfterStopOrCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx context.Context
		s   *server
	}{{ctx, &server{}}, {context.Background(), &server{stopped: true}}, {context.Background(), &server{updating: true}}} {
		accepted, err := scheduleAutomaticUpdate(tc.ctx, tc.s, func() error { t.Error("worker after stop"); return nil })
		if accepted || err == nil {
			t.Fatal("unavailable host accepted update")
		}
	}
}
