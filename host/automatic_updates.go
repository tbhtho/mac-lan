package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tbhtho/MAC-DOW/host/internal/diagnostics"
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
)

// automaticUpdateIdleGrace is how long sharing must stay idle (no connected
// Windows session, no file transfer) before an automatic update may restart
// the host. Manual updates are not delayed.
const automaticUpdateIdleGrace = 60 * time.Second

// errUpdateDeferred keeps a verified update waiting while Windows is using the
// Mac. The background poll retries, so it installs at the first idle moment.
var errUpdateDeferred = errors.New("update ready; installs when Windows disconnects")

// sharingIdle remembers when sharing was last in use. It is sampled by a
// ticker and at scheduling time, so a short session between polls still
// restarts the grace period.
type sharingIdle struct {
	mu       sync.Mutex
	lastBusy time.Time
	now      func() time.Time
}

func newSharingIdle() *sharingIdle { return &sharingIdle{now: time.Now} }

// observe records the current state and reports whether sharing has been idle
// for at least the grace period. A host that was never busy is idle.
func (i *sharingIdle) observe(busy bool) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	now := i.now()
	if busy {
		i.lastBusy = now
		return false
	}
	return i.lastBusy.IsZero() || now.Sub(i.lastBusy) >= automaticUpdateIdleGrace
}

// inUse reports an incoming transfer or an offered/downloading outgoing file.
func (t *transferReceiver) inUse() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.busy || t.outgoing != nil
}

// sharingBusy reports a connected Windows session or a file transfer.
func (s *server) sharingBusy() bool {
	if s.transfers.inUse() {
		return true
	}
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	return s.streaming
}

// watchSharingActivity samples sharing state every few seconds until ctx ends.
// report, when set, is called with the first state and on each change.
func watchSharingActivity(ctx context.Context, s *server, idle *sharingIdle, report func(bool)) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	last, first := false, true
	for {
		busy := s.sharingBusy()
		if idle != nil {
			idle.observe(busy)
		}
		if report != nil && (first || busy != last) {
			report(busy)
		}
		last, first = busy, false
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Reserve new pairings during verification and worker creation. An automatic
// update is deferred while Windows is connected or a transfer runs; once idle,
// existing sharing stays alive until scheduling succeeds and the caller
// requests graceful shutdown.
func scheduleAutomaticUpdate(ctx context.Context, s *server, idle *sharingIdle, schedule func() error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	transferring := s.transfers.inUse()
	s.connectionMu.Lock()
	if s.stopped || s.updating {
		s.connectionMu.Unlock()
		return false, errors.New("host is stopping or already updating")
	}
	// Checked under connectionMu so no session can start between this check
	// and the updating reservation below.
	if idle != nil && !idle.observe(transferring || s.streaming) {
		s.connectionMu.Unlock()
		return false, errUpdateDeferred
	}
	s.updating = true
	s.connectionMu.Unlock()
	if err := schedule(); err != nil {
		s.connectionMu.Lock()
		s.updating = false
		s.connectionMu.Unlock()
		return false, err
	}
	return true, nil
}

func startHostUpdates(ctx context.Context, stop context.CancelFunc, s *server) {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	install := filepath.Dir(executable)
	// Drop old stages, backups and partial downloads of earlier updates.
	if local, err := updater.LocalEngine(); err == nil {
		local.Cleanup(install)
	}
	idle := newSharingIdle()
	go watchSharingActivity(ctx, s, idle, nil)
	deferredShown := false // callbacks run on the single polling goroutine
	updater.StartBackgroundUpdates(ctx, install, func(stage updater.Stage) error {
		scheduled, err := scheduleAutomaticUpdate(ctx, s, idle, func() error {
			// Public updates omit the private endpoint; retain it locally first.
			if err := diagnostics.InitMacDestination(); err != nil {
				return err
			}
			e, err := updater.NewEngine()
			if err != nil {
				return err
			}
			applyCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			_, err = e.ScheduleApply(applyCtx, stage.Directory, install, os.Getpid(), true)
			return err
		})
		if err != nil {
			return err
		}
		if scheduled {
			fmt.Fprintln(os.Stdout, "Restarting to install the verified update...")
			stop()
		}
		return nil
	}, func(err error) {
		if errors.Is(err, errUpdateDeferred) {
			if !deferredShown {
				deferredShown = true
				fmt.Fprintln(os.Stdout, "Update ready - installs after Windows disconnects and sharing has been idle for a minute.")
			}
			return
		}
		fmt.Fprintln(os.Stderr, "Automatic update unavailable; sharing continues and the next poll will retry:", err)
	})
}
