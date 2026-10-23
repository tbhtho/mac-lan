//go:build !windows

package updater

import (
	"context"
	"errors"
	"syscall"
	"time"
)

func waitProcess(ctx context.Context, pid int) error {
	if pid < 1 {
		return errors.New("invalid parent PID")
	}
	for {
		e := syscall.Kill(pid, 0)
		if e == syscall.ESRCH {
			return nil
		}
		if e != nil {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
