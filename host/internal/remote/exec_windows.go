//go:build windows

package remote

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

const (
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
)

// shellCommand builds the Windows command. cmd.exe needs its command line passed
// verbatim: Go's argument quoting would escape the user's own quotes.
func shellCommand(shell, command string) (*exec.Cmd, error) {
	attributes := &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createNewProcessGroup}
	switch shell {
	case "", "cmd":
		path := os.Getenv("ComSpec")
		if path == "" {
			path = "cmd.exe"
		}
		cmd := exec.Command(path)
		// Code page 65001 makes built-in commands and most tools print UTF-8.
		attributes.CmdLine = `cmd.exe /d /s /c "chcp 65001>nul & ` + command + `"`
		cmd.SysProcAttr = attributes
		return cmd, nil
	case "powershell", "pwsh":
		cmd := exec.Command(shell+".exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
			"[Console]::OutputEncoding=[Text.Encoding]::UTF8; "+command)
		cmd.SysProcAttr = attributes
		return cmd, nil
	}
	return nil, errors.New("shell must be cmd, powershell or pwsh on Windows")
}

// killTree ends the command and everything it started.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if kill.Run() != nil {
		_ = cmd.Process.Kill()
	}
}

func defaultShellName() string { return "cmd" }
