package main

import (
	"context"
	"encoding/json"
	"github.com/tbhtho/MAC-DOW/host/internal/discovery"
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
	"os"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "discover" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hosts, err := discovery.Browse(ctx)
		message := "Choose your Mac, then compare its identity code. No automatic trust or capture."
		if err != nil {
			message = err.Error()
		} else if len(hosts) == 0 {
			message = "No Mac found. Open MAC-DOW Host on the Mac and click Start sharing. Both devices need the same LAN. Advanced connection remains available; no firewall rules are changed."
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": err == nil, "message": message, "hosts": hosts})
		return
	}
	os.Exit(updater.Run(os.Args[1:], os.Stdout))
}
