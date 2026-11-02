package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tbhtho/MAC-DOW/host/internal/diagnostics"
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
)

// Reserve new pairings during verification and worker creation. Existing sharing
// stays alive until scheduling succeeds and the caller requests graceful shutdown.
func scheduleAutomaticUpdate(ctx context.Context, s *server, schedule func() error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.connectionMu.Lock()
	if s.stopped || s.updating {
		s.connectionMu.Unlock()
		return false, errors.New("host is stopping or already updating")
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
	updater.StartBackgroundUpdates(ctx, install, func(stage updater.Stage) error {
		scheduled, err := scheduleAutomaticUpdate(ctx, s, func() error {
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
		fmt.Fprintln(os.Stderr, "Automatic update unavailable; sharing continues and the next poll will retry:", err)
	})
}
