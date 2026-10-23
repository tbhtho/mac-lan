package updater

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func launch(root, goos string) error {
	name := "MAC-DOW.exe"
	var args []string
	if goos == "darwin" {
		name = "MacDowHost"
		args = []string{"--updates"}
	}
	c := exec.Command(filepath.Join(root, name), args...)
	quietCommand(c)
	if goos == "darwin" {
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
	}
	return c.Start()
}
func WaitParent(pid int) error {
	if pid == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return waitProcess(ctx, pid)
}
