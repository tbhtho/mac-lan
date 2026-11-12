//go:build !windows

package remote

import (
	"context"
	"errors"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
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

var systemOnce sync.Once
var systemText string

// systemName is the operating system as a person would name it.
func systemName() string {
	systemOnce.Do(func() {
		systemText = runtime.GOOS
		if runtime.GOOS == "darwin" {
			systemText = "macOS"
			if out, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output(); err == nil && len(out) < 32 {
				systemText += " " + strings.TrimSpace(string(out))
			}
		}
	})
	return systemText
}

// captureScreen returns a JPEG of the main display, at most width pixels wide.
func captureScreen(width int) ([]byte, int, int, error) {
	if runtime.GOOS != "darwin" {
		return nil, 0, 0, errors.New("screenshots are not available on this computer")
	}
	folder, err := os.MkdirTemp("", "mac-lan-shot-*")
	if err != nil {
		return nil, 0, 0, errors.New("cannot create a temporary file for the picture")
	}
	defer os.RemoveAll(folder)
	file := filepath.Join(folder, "screen.jpg")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	const denied = "The Mac could not take a picture of its screen. Allow Screen Recording for mac-lan in System Settings > Privacy & Security, and make sure the Mac is awake and unlocked."
	if exec.CommandContext(ctx, "/usr/sbin/screencapture", "-x", "-t", "jpg", file).Run() != nil {
		return nil, 0, 0, errors.New(denied)
	}
	size := func() (int, int) {
		source, err := os.Open(file)
		if err != nil {
			return 0, 0
		}
		defer source.Close()
		config, err := jpeg.DecodeConfig(source)
		if err != nil {
			return 0, 0
		}
		return config.Width, config.Height
	}
	w, h := size()
	if w == 0 {
		return nil, 0, 0, errors.New(denied)
	}
	if w > width {
		if exec.CommandContext(ctx, "/usr/bin/sips", "--resampleWidth", strconv.Itoa(width), file).Run() == nil {
			w, h = size()
		}
	}
	data, err := os.ReadFile(file)
	if err != nil || w == 0 {
		return nil, 0, 0, errors.New(denied)
	}
	return data, w, h, nil
}

// keepAwake stops a Mac from going to sleep for being idle while the command
// with this process id runs, so a long build is not cut off. The helper ends by
// itself when the command does. Closing the lid still sleeps the Mac.
func keepAwake(pid int) {
	if runtime.GOOS != "darwin" {
		return
	}
	awake := exec.Command("/usr/bin/caffeinate", "-i", "-w", strconv.Itoa(pid))
	if awake.Start() == nil {
		go func() { _ = awake.Wait() }()
	}
}

func defaultShellName() string {
	if runtime.GOOS == "darwin" {
		return "zsh"
	}
	return "sh"
}
