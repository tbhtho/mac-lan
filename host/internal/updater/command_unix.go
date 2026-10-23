//go:build !windows

package updater

import "os/exec"

func quietCommand(c *exec.Cmd) {}
