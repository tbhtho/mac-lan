package main

import (
	"context"
	"encoding/json"
	"github.com/tbhtho/MAC-DOW/host/internal/diagnostics"
	"github.com/tbhtho/MAC-DOW/host/internal/discovery"
	"github.com/tbhtho/MAC-DOW/host/internal/remote"
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
	if len(os.Args) >= 2 {
		if code, handled := remoteMode(os.Args[1], os.Args[2:]); handled {
			os.Exit(code)
		}
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

// ensureAgent makes sure something on this computer serves local tools: the
// mac-lan app when it is open, otherwise a background copy of this helper that
// can reach the Mac through its standby link. It reports whether one answers.
func ensureAgent(dir string) bool {
	if client, err := remote.Dial(dir, ""); err != remote.ErrNotRunning {
		if err == nil {
			client.Close()
		}
		return err == nil
	}
	if startDetached("remote-agent", "--standalone") != nil {
		return false
	}
	for attempt := 0; attempt < 40; attempt++ {
		time.Sleep(150 * time.Millisecond)
		if client, err := remote.Dial(dir, ""); err == nil {
			client.Close()
			// Give the standby link a moment to connect before the first request.
			for wait := 0; wait < 60 && remote.HasStandby(dir) && !linked(dir); wait++ {
				time.Sleep(150 * time.Millisecond)
			}
			return true
		}
	}
	return false
}

func linked(dir string) bool {
	client, err := remote.Dial(dir, "")
	if err != nil {
		return false
	}
	defer client.Close()
	reply, err := client.Call(map[string]any{"op": "devices"})
	if err != nil {
		return true // refused (agents switched off): waiting will not help
	}
	devices, _ := reply["devices"].([]any)
	for _, value := range devices {
		device, _ := value.(map[string]any)
		this, _ := device["this_device"].(bool)
		connected, _ := device["connected"].(bool)
		if platform, _ := device["platform"].(string); !this && connected && platform != "" {
			return true
		}
	}
	return false
}

// remoteMode runs the remote-command modes of the Windows helper.
//
//	remote-agent  runs for the life of mac-lan.exe, which owns its pipes: it
//	              holds the link to the Mac, runs commands the Mac sends once
//	              the user allows that, and serves local tools on loopback.
//	mcp           stdio MCP server for AI agents; a client of remote-agent.
//	remote-shell  line-based shell on the Mac; a client of remote-agent.
func remoteMode(mode string, args []string) (int, bool) {
	if mode != "remote-agent" && mode != "mcp" && mode != "remote-shell" {
		return 0, false
	}
	dir, err := remote.StateDir()
	if err != nil {
		return 1, true
	}
	version := updater.BuildConfig().Version
	switch mode {
	case "mcp":
		if remote.ServeMCP(os.Stdin, os.Stdout, dir, version, func() bool { return ensureAgent(dir) }) != nil {
			return 1, true
		}
		return 0, true
	case "remote-shell":
		ensureAgent(dir)
		return remote.RunShell(os.Stdin, os.Stdout, dir, "", args), true
	}
	name, _ := os.Hostname()
	if name == "" {
		name = "Windows PC"
	}
	node := remote.New(dir, name, version)
	server := remote.NewServer(node)
	if len(args) == 1 && args[0] == "--standalone" {
		// Started by a tool while mac-lan.exe is not running: there is no window
		// and no session, only the Mac's standby link if it has one. It leaves
		// when mac-lan starts (and takes over the endpoint) or nobody uses it.
		if client, err := remote.Dial(dir, ""); err == nil {
			client.Close()
			return 0, true
		}
		if server.Listen(dir) != nil {
			return 1, true
		}
		for server.Current(dir) && (node.Busy() || server.Idle() < 15*time.Minute) {
			time.Sleep(3 * time.Second)
		}
		server.Close(dir)
		node.Close()
		return 0, true
	}
	_ = server.Listen(dir)
	server.ServeUI(os.Stdin, os.Stdout) // returns when mac-lan.exe closes the pipe
	server.Close(dir)
	node.Close()
	return 0, true
}
