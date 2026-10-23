package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tbhtho/MAC-DOW/host/internal/diagnostics"
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
)

const updateOfferScript = `on run argv
 set answer to display dialog (item 1 of argv) with title "mac-lan Update" buttons {"Later", "Restart to update"} default button "Later" cancel button "Later" giving up after 90
 if gave up of answer then return "Later"
 return button returned of answer
end run`

func updateOffer(ctx context.Context, version string) bool {
	child, cancel := context.WithTimeout(ctx, 95*time.Second)
	defer cancel()
	message := "mac-lan " + version + " is downloaded and its publisher signature is verified.\n\nRestart to install it? Your settings stay in place. Reconnect from Windows afterwards; control stays off until you activate it."
	output, err := exec.CommandContext(child, "/usr/bin/osascript", "-e", updateOfferScript, "--", message).Output()
	return err == nil && len(output) < 64 && strings.TrimSpace(string(output)) == "Restart to update"
}

func offerWhenIdle(ctx context.Context, s *server, offer func() bool, schedule func() error) (bool, error) {
	for {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		s.connectionMu.Lock()
		active, stopped := s.active != nil, s.stopped
		s.connectionMu.Unlock()
		if stopped {
			return false, context.Canceled
		}
		if !active {
			break
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if !offer() || ctx.Err() != nil {
		return false, ctx.Err()
	}
	// A connection can arrive while the Restart/Later dialog is visible.
	// Reserve the idle host through preflight/worker creation so that no new
	// pairing is interrupted by a delayed update decision.
	s.connectionMu.Lock()
	if s.active != nil || s.stopped || s.updating {
		s.connectionMu.Unlock()
		return false, errors.New("a connection started while the update offer was open; the update remains downloaded")
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
	updater.StartBackgroundUpdates(ctx, install, func(stage updater.Stage) {
		fmt.Fprintln(os.Stdout, "A verified mac-lan update is downloaded. A Restart to update choice will appear when the connection is idle.")
		accepted, err := offerWhenIdle(ctx, s, func() bool { return updateOffer(ctx, stage.Plan.Version) }, func() error {
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
		if err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "Update restart unavailable; sharing continues:", err)
		}
		if accepted {
			fmt.Fprintln(os.Stdout, "Restarting to install the verified update...")
			stop()
		}
	}, func(err error) {
		fmt.Fprintln(os.Stderr, "Automatic update check unavailable; sharing continues:", err)
	})
}
