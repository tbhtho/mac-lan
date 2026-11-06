package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAutomaticUpdateSchedulesWhileSharingWithoutIdleGate(t *testing.T) {
	s := &server{active: &websocket.Conn{}}
	accepted, err := scheduleAutomaticUpdate(context.Background(), s, nil, func() error {
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
	accepted, err := scheduleAutomaticUpdate(context.Background(), s, newSharingIdle(), func() error { return errors.New("temporary verification failure") })
	if accepted || err == nil || s.updating {
		t.Fatal("failure requested shutdown or blocked pairing")
	}
	accepted, err = scheduleAutomaticUpdate(context.Background(), s, newSharingIdle(), func() error { return nil })
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
		accepted, err := scheduleAutomaticUpdate(tc.ctx, tc.s, newSharingIdle(), func() error { t.Error("worker after stop"); return nil })
		if accepted || err == nil {
			t.Fatal("unavailable host accepted update")
		}
	}
}

func fakeIdle(start time.Time) (*sharingIdle, *time.Time) {
	now := start
	return &sharingIdle{now: func() time.Time { return now }}, &now
}

func TestAutomaticUpdateDefersWhileWindowsIsStreaming(t *testing.T) {
	idle, now := fakeIdle(time.Unix(1000, 0))
	s := &server{active: &websocket.Conn{}, streaming: true}
	accepted, err := scheduleAutomaticUpdate(context.Background(), s, idle, func() error { t.Error("worker while streaming"); return nil })
	if accepted || !errors.Is(err, errUpdateDeferred) || s.updating {
		t.Fatal("streaming session was interrupted", accepted, err)
	}
	// Stop viewing (a remembered PC may stay connected for files), then still wait for the grace period.
	s.streaming = false
	*now = now.Add(automaticUpdateIdleGrace - time.Second)
	if accepted, err = scheduleAutomaticUpdate(context.Background(), s, idle, func() error { t.Error("worker inside grace period"); return nil }); accepted || !errors.Is(err, errUpdateDeferred) {
		t.Fatal("installed before the idle grace period", err)
	}
	*now = now.Add(2 * time.Second)
	if accepted, err = scheduleAutomaticUpdate(context.Background(), s, idle, func() error { return nil }); !accepted || err != nil {
		t.Fatal("idle host did not install", err)
	}
}

func TestAutomaticUpdateDefersDuringFileTransfer(t *testing.T) {
	idle, _ := fakeIdle(time.Unix(1000, 0))
	for _, receiver := range []*transferReceiver{{busy: true}, {outgoing: &outgoingFile{}}} {
		s := &server{transfers: receiver}
		if accepted, err := scheduleAutomaticUpdate(context.Background(), s, idle, func() error { t.Error("worker during transfer"); return nil }); accepted || !errors.Is(err, errUpdateDeferred) {
			t.Fatal("transfer was interrupted", err)
		}
		if !s.sharingBusy() {
			t.Fatal("transfer not reported busy")
		}
	}
}

func TestSharingIdleNeverBusyIsIdle(t *testing.T) {
	idle, now := fakeIdle(time.Unix(1000, 0))
	if !idle.observe(false) {
		t.Fatal("unused host must be idle")
	}
	if idle.observe(true) {
		t.Fatal("busy host reported idle")
	}
	*now = now.Add(automaticUpdateIdleGrace)
	if !idle.observe(false) {
		t.Fatal("grace period elapsed but not idle")
	}
	if (&server{}).sharingBusy() {
		t.Fatal("empty server reported busy")
	}
}

func TestWatchSharingActivityReportsInitialStateAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reports := make(chan bool, 4)
	done := make(chan struct{})
	go func() {
		watchSharingActivity(ctx, &server{}, nil, func(b bool) { reports <- b })
		close(done)
	}()
	if busy := <-reports; busy {
		t.Fatal("idle server reported busy")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not stop")
	}
}
