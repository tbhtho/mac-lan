package updater

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func launch(root, goos string) error {
	program, args := launchCommand(root, goos)
	c := exec.Command(program, args...)
	quietCommand(c)
	return c.Start()
}
func launchCommand(root, goos string) (string, []string) {
	if goos == "darwin" && isApp(root) {
		return "/usr/bin/open", []string{root}
	}
	if goos == "windows" {
		if i, err := os.Stat(root); err == nil && i.Mode().IsRegular() {
			return root, nil
		}
	}
	name := "MAC-DOW.exe"
	var args []string
	if goos == "darwin" {
		name = "MacDowHost"
		// The host starts a fresh listener and still requires fresh pairing,
		// video consent, and an input lease after an update.
	}
	return filepath.Join(root, name), args
}
func WaitParent(pid int) error {
	if pid == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return waitProcess(ctx, pid)
}
