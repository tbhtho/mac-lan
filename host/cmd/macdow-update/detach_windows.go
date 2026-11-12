//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// startDetached runs this program again with args, outliving the caller and
// showing no window.
func startDetached(args ...string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, args...)
	const detachedProcess, newProcessGroup, noWindow = 0x00000008, 0x00000200, 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess | newProcessGroup | noWindow}
	if err = cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
