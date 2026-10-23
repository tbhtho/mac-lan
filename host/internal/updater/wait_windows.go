//go:build windows

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
	kernel := syscall.NewLazyDLL("kernel32.dll")
	open := kernel.NewProc("OpenProcess")
	wait := kernel.NewProc("WaitForSingleObject")
	close := kernel.NewProc("CloseHandle")
	h, _, e := open.Call(0x00100000, 0, uintptr(pid))
	if h == 0 {
		if e == syscall.Errno(87) {
			return nil
		}
		return errors.New("cannot wait for application exit")
	}
	defer close.Call(h)
	for {
		r, _, _ := wait.Call(h, 250)
		if r == 0 {
			return nil
		}
		if r != 258 {
			return errors.New("process wait failed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}
