//go:build !windows

package remote

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// shellCommand runs the command in a login shell, so tools installed for the
// user (Homebrew, version managers) are on PATH as they are in Terminal.
func shellCommand(shell, command string) (*exec.Cmd, error) {
	path := ""
	switch shell {
	case "":
		path = os.Getenv("SHELL")
		if path == "" {
			path = "/bin/sh"
			if runtime.GOOS == "darwin" {
				path = "/bin/zsh"
			}
		}
	case "zsh", "bash", "sh":
		path = "/bin/" + shell
	default:
		return nil, errors.New("shell must be zsh, bash or sh on this computer")
	}
	cmd := exec.Command(path, "-lc", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd, nil
}

// killTree ends the command's whole process group.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	go func() {
		time.Sleep(3 * time.Second)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}()
}

func defaultShellName() string {
	if runtime.GOOS == "darwin" {
		return "zsh"
	}
	return "sh"
}
