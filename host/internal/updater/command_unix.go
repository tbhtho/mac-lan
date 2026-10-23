//go:build !windows

package updater

import (
	"os/exec"
	"syscall"
)

func quietCommand(c *exec.Cmd)    {}
func detachedCommand(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
