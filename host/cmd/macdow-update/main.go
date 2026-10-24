package main

import (
	"context"
	"encoding/json"
	"github.com/tbhtho/MAC-DOW/host/internal/diagnostics"
	"github.com/tbhtho/MAC-DOW/host/internal/discovery"
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) == 6 && os.Args[1] == "diagnose" {
		count, _ := strconv.Atoi(os.Args[5])
		json.NewEncoder(os.Stdout).Encode(diagnostics.Record(os.Args[2], os.Args[3], os.Args[4], map[string]int{"hosts": count}))
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "discover" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hosts, err := discovery.Browse(ctx)
		message := "Choose your Mac, then compare its identity code. No automatic trust or capture."
		if err != nil {
			message = err.Error()
		} else if len(hosts) == 0 {
			message = "No Mac appeared. Keep mac-lan running on your Mac and use the same Wi-Fi or Ethernet. If you have the previous Mac version, update it: that version could not announce itself to Windows."
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": err == nil, "message": message, "hosts": hosts})
		return
	}
	os.Exit(updater.Run(os.Args[1:], os.Stdout))
}
