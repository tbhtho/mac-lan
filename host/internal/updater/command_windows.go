//go:build windows

package updater

import (
	"os/exec"
	"syscall"
)

func quietCommand(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
