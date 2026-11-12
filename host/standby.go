package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tbhtho/MAC-DOW/host/internal/remote"
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
)

// The standby link is a small service this Mac's user can switch on. launchd
// runs this program a second time, apart from the mac-lan app, so it keeps
// serving remote commands to the paired PC while the app is restarting, being
// updated, not sharing, or failing to open after a bad update. It runs from the
// installed app, which the macOS firewall and privacy settings already know;
// a private copy of the last version that worked is used only when the
// installed program is missing. It listens on its own
// port with its own certificate and token, which the PC learns over the normal
// link, and it obeys the same "run commands on this Mac" switch.

const standbyLabel = "com.tbhtho.mac-lan.standby"

func standbyBinary(dir string) string { return filepath.Join(remote.StandbyDir(dir), "MacDowHost") }

func standbyPlist() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", standbyLabel+".plist"), nil
}

func standbyPlistText(program, fallback string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + standbyLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/sh</string><string>-c</string>
		<string>[ -x "$0" ] &amp;&amp; exec "$0" --link-service; exec "$1" --link-service</string>
		<string>` + html.EscapeString(program) + `</string><string>` + html.EscapeString(fallback) + `</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>ThrottleInterval</key><integer>10</integer>
	<key>ProcessType</key><string>Background</string>
</dict>
</plist>
`
}

func launchTarget() string { return "gui/" + strconv.Itoa(os.Getuid()) + "/" + standbyLabel }

func fileHash(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	sum := sha256.New()
	if _, err = io.Copy(sum, file); err != nil {
		return ""
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// copyProgram places this program's file at destination atomically.
func copyProgram(destination string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	source, err := os.Open(executable)
	if err != nil {
		return err
	}
	defer source.Close()
	if err = os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".program-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err = io.Copy(temp, source); err == nil {
		err = temp.Chmod(0o755)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, destination)
}

func standbyInstalled(dir string) bool {
	plist, err := standbyPlist()
	if err != nil {
		return false
	}
	_, planned := os.Stat(plist)
	_, copied := os.Stat(standbyBinary(dir))
	return planned == nil && copied == nil
}

var launchPID = regexp.MustCompile(`(?m)^\s*pid = (\d+)`)

// standbyPID is the running service's process id, or 0.
func standbyPID() int {
	out, err := exec.Command("/bin/launchctl", "print", launchTarget()).Output()
	if err != nil {
		return 0
	}
	if found := launchPID.FindSubmatch(out); found != nil {
		pid, _ := strconv.Atoi(string(found[1]))
		return pid
	}
	return 0
}

// setStandby installs and starts the service, or stops and removes it.
func setStandby(dir string, on bool) error {
	plist, err := standbyPlist()
	if err != nil {
		return err
	}
	_ = exec.Command("/bin/launchctl", "bootout", launchTarget()).Run() // not loaded is fine
	if !on {
		_ = os.Remove(plist)
		return os.RemoveAll(remote.StandbyDir(dir))
	}
	program, err := os.Executable()
	if err != nil {
		return err
	}
	if err = copyProgram(standbyBinary(dir)); err != nil {
		return errors.New("could not copy the program: " + err.Error())
	}
	if err = os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		return err
	}
	if err = os.WriteFile(plist, []byte(standbyPlistText(program, standbyBinary(dir))), 0o644); err != nil {
		return err
	}
	// A service that was just removed can take a moment to leave launchd.
	var out []byte
	for attempt := 0; attempt < 5; attempt++ {
		if out, err = exec.Command("/bin/launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), plist).CombinedOutput(); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("launchd did not start the service: " + strings.TrimSpace(string(out)))
}

// refreshStandby runs once this version of the app has worked for a while: it
// restarts the service so that it runs this version too, and renews the
// fallback copy. It leaves a service that is running a command alone.
func refreshStandby(dir string) {
	time.Sleep(10 * time.Minute)
	executable, err := os.Executable()
	if err != nil || !standbyInstalled(dir) {
		return
	}
	if mine := fileHash(executable); mine == "" || mine == fileHash(standbyBinary(dir)) {
		return
	}
	if pid := standbyPID(); pid > 0 {
		if out, _ := exec.Command("/usr/bin/pgrep", "-P", strconv.Itoa(pid)).Output(); len(bytes.TrimSpace(out)) > 0 {
			return // busy; the next launch of mac-lan tries again
		}
	}
	if copyProgram(standbyBinary(dir)) == nil {
		_ = exec.Command("/bin/launchctl", "kickstart", "-k", launchTarget()).Run()
	}
}

// runLinkService is the service itself: it serves remote commands until launchd stops it.
func runLinkService() int {
	dir, err := remote.StateDir()
	if err != nil {
		return 1
	}
	folder := remote.StandbyDir(dir)
	if err = os.MkdirAll(folder, 0o700); err != nil {
		return 1
	}
	cert, fingerprint, err := persistentIdentity(folder)
	if err != nil {
		return 1
	}
	offer, _ := remote.ReadStandbyOffer(dir)
	if len(offer.Token) != 64 {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return 1
		}
		offer.Token = hex.EncodeToString(secret)
	}
	offer.Fingerprint = fingerprint
	// Keep the same port across restarts so the PC finds the service again.
	var listener net.Listener
	if offer.Port >= 1024 {
		listener, _ = net.Listen("tcp4", ":"+strconv.Itoa(offer.Port))
	}
	if listener == nil {
		if listener, err = net.Listen("tcp4", ":0"); err != nil {
			return 1
		}
	}
	offer.Port = listener.Addr().(*net.TCPAddr).Port
	if err = remote.WriteStandbyOffer(dir, offer); err != nil {
		return 1
	}
	node := remote.New(dir, computerName(), updater.BuildConfig().Version)
	defer node.Close()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}},
		Handler:   http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { node.ServeStandby(w, r, offer.Token) })}
	if err = server.ServeTLS(listener, "", ""); err != nil && err != http.ErrServerClosed {
		return 1
	}
	return 0
}

// standbyCommand handles "--standby on|off|status".
func standbyCommand(dir string, args []string) int {
	if len(args) != 1 || args[0] != "on" && args[0] != "off" && args[0] != "status" {
		fmt.Fprintln(os.Stderr, "Use: --standby on|off|status")
		return 2
	}
	if args[0] == "status" {
		offer, listening := remote.ReadStandbyOffer(dir)
		switch pid := standbyPID(); {
		case !standbyInstalled(dir):
			fmt.Println("The standby link is off.")
		case pid == 0 || !listening:
			fmt.Println("The standby link is installed but not running.")
		default:
			fmt.Printf("The standby link is running (process %d, port %d).\n", pid, offer.Port)
		}
		return 0
	}
	if err := setStandby(dir, args[0] == "on"); err != nil {
		fmt.Fprintln(os.Stderr, "The standby link could not be changed: "+err.Error())
		return 1
	}
	if args[0] == "on" {
		fmt.Println("The standby link is on. The paired PC learns it the next time mac-lan connects, and can then run commands here while the mac-lan app restarts or updates.")
	} else {
		fmt.Println("The standby link is off and removed.")
	}
	return 0
}
