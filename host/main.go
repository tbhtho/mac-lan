package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--updater" {
		os.Exit(updater.Run(os.Args[2:], os.Stdout))
	}
	addr := flag.String("listen", "auto", "automatic LAN selection, or a specific private IPv4:port")
	gui := flag.Bool("gui", false, "inherited-pipe UI mode; no Terminal consent")
	interfaces := flag.Bool("interfaces", false, "list eligible LAN interfaces; no listener/capture")
	check := flag.Bool("check", false, "check existing permissions without requesting them")
	self := flag.Bool("self-test", false, "non-capturing TLS self-test")
	updates := flag.Bool("updates", false, "interactive updates only")
	updateCheck := flag.Bool("update-check", false, "read-only release check")
	noUpdateCheck := flag.Bool("no-update-check", false, "disable startup release check")
	flag.Parse()
	if *interfaces {
		all, err := localInterfaces()
		if err != nil {
			fatal(err)
		}
		json.NewEncoder(os.Stdout).Encode(all)
		return
	}
	if *updates {
		os.Exit(updater.Console(os.Stdin, os.Stdout))
	}
	if *updateCheck {
		os.Exit(updater.Run([]string{"check"}, os.Stdout))
	}
	if *self {
		_, _, err := identity()
		if err != nil {
			fatal(err)
		}
		fmt.Println("MAC-DOW TLS identity self-test passed; capture/input not started")
		return
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		fatal(errors.New("companion requires Apple Silicon macOS 14+"))
	}
	exe, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	helper := filepath.Join(filepath.Dir(exe), "MacDowCapture")
	if *check {
		cmd := exec.Command(helper, "--check")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err = cmd.Run(); err != nil {
			fatal(err)
		}
		return
	}
	if err = hostSession(helper, *addr, *gui, *noUpdateCheck); err != nil {
		fatal(err)
	}
}
func hostSession(helper, address string, gui, noUpdates bool) error {
	all, err := localInterfaces()
	if err != nil {
		return err
	}
	address, err = chooseListen(address, all)
	if err != nil {
		return err
	}
	cmd := exec.Command(helper, "--check")
	if !gui {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err = cmd.Run(); err != nil {
		return errors.New("Screen Recording is not available to the capture helper. Open MAC-DOW Host, choose Screen Recording, grant access yourself and restart the app")
	}
	cert, fp, err := identity()
	if err != nil {
		return err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return err
	}
	token := hex.EncodeToString(secret)
	s := &server{token: token, peerAllowed: privateIP, slot: make(chan struct{}, 1)}
	s.catalog = func() ([]window, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, helper, "--list").Output()
		if err != nil {
			return nil, errors.New("Cannot list Mac windows. Check the host app's Screen Recording permission")
		}
		var windows []window
		err = json.Unmarshal(out, &windows)
		return windows, err
	}
	var broker *uiBroker
	var scanner *bufio.Scanner
	if gui {
		broker = newUIBroker(os.Stdin, os.Stdout)
	} else {
		scanner = bufio.NewScanner(os.Stdin)
	}
	s.consent = func(r start) bool {
		scope := "Full desktop"
		if r.Window != 0 {
			scope = fmt.Sprintf("Only selected window %d; menus and child dialogs excluded", r.Window)
		}
		if gui {
			return broker.ask("stream", map[string]any{"scope": scope, "width": r.Width, "fps": r.FPS, "codec": streamDescription(r)}, 45*time.Second)
		}
		fmt.Printf("\nWindows requests %s, %d px / %d fps / %s.\nAllow viewing and explicitly armed keyboard/mouse for this session? Type ALLOW: ", scope, r.Width, r.FPS, streamDescription(r))
		return scanner.Scan() && scanner.Text() == "ALLOW"
	}
	if gui {
		s.pair = func(peer, code string) bool {
			return broker.ask("pair", map[string]any{"peer": peer, "code": code}, 45*time.Second)
		}
	}
	s.stream = func(send func([]byte), status func(string), events <-chan input) error {
		if gui {
			broker.emit(map[string]any{"type": "state", "message": "Sharing active. Windows control remains explicit."})
		}
		err := runCapture(helper, s.selected, send, status, events)
		if gui {
			broker.emit(map[string]any{"type": "state", "message": "Session ended. Waiting for a new approved connection."})
		}
		return err
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return fmt.Errorf("Cannot start sharing on this network: %w", err)
	}
	defer listener.Close()
	actual := listener.Addr().(*net.TCPAddr)
	if gui {
		broker.emit(map[string]any{"type": "ready", "address": actual.IP.String(), "port": actual.Port, "fingerprint": fp, "token": token})
	} else {
		fmt.Printf("\nMAC-DOW host - Ctrl+C stops sharing.\nAddress: %s\nSHA-256 fingerprint: %s\nTemporary token: %s\nNo capture until pairing + ALLOW.\n", listener.Addr(), fp, token)
		if !noUpdates {
			go updater.StartupCheck(os.Stdout)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if gui {
		go func() {
			select {
			case <-broker.ctx.Done():
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}}
	stopped := make(chan struct{})
	go func() { <-ctx.Done(); listener.Close(); s.stopActive(); close(stopped) }()
	err = srv.Serve(tls.NewListener(listener, srv.TLSConfig))
	cancel()
	<-stopped
	if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
func streamDescription(r start) string {
	if r.Codec == "jpeg" {
		return fmt.Sprintf("JPEG quality %d", r.Quality)
	}
	bitrate := r.Bitrate
	if bitrate == 0 {
		bitrate = 8
	}
	return fmt.Sprintf("hardware H.264 / %d Mbit/s", bitrate)
}
func runCapture(helper string, r start, send func([]byte), status func(string), events <-chan input) error {
	codec := r.Codec
	if codec == "" {
		codec = "h264"
	}
	bitrate := r.Bitrate
	if bitrate == 0 {
		bitrate = 8
	}
	cmd := exec.Command(helper, "--stream", strconv.FormatUint(uint64(r.Window), 10), strconv.Itoa(r.Width), strconv.Itoa(r.FPS), strconv.Itoa(r.Quality), codec, strconv.Itoa(bitrate))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	var held heldState
	var heldMu sync.Mutex
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if h, ok := parseHeld(sc.Bytes()); ok {
				heldMu.Lock()
				held = h
				heldMu.Unlock()
			} else {
				status(sc.Text())
			}
		}
	}()
	go func() {
		defer stdin.Close()
		enc := json.NewEncoder(stdin)
		for {
			select {
			case e, ok := <-events:
				if !ok {
					_ = enc.Encode(input{Type: "release"})
					return
				}
				if enc.Encode(e) != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()
	var frameErr error
	for {
		var n uint32
		if err = binary.Read(stdout, binary.BigEndian, &n); err != nil {
			frameErr = err
			break
		}
		if n == 0 || n > maxFrame {
			frameErr = errors.New("invalid capture frame size")
			break
		}
		b := make([]byte, n)
		if _, err = io.ReadFull(stdout, b); err != nil {
			frameErr = err
			break
		}
		send(b)
	}
	_ = stdin.Close()
	_ = cmd.Process.Kill()
	<-stderrDone
	waitErr := cmd.Wait()
	heldMu.Lock()
	snapshot := held
	heldMu.Unlock()
	if recoveryErr := recoverHeld(helper, snapshot); recoveryErr != nil {
		status("Native recovery could not confirm release; stop control and release modifiers on Mac: " + recoveryErr.Error())
	}
	if waitErr != nil {
		return waitErr
	}
	if errors.Is(frameErr, io.EOF) {
		return nil
	}
	return frameErr
}
