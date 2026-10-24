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
	"github.com/tbhtho/MAC-DOW/host/internal/diagnostics"
	"github.com/tbhtho/MAC-DOW/host/internal/discovery"
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
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" && len(os.Args) == 1 {
		if err := launchMacMenu(); err != nil {
			showMenuFailure(err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--diagnose" {
		event, code := "manual-report", "ok"
		if len(os.Args) > 2 {
			event = os.Args[2]
		}
		if len(os.Args) > 3 {
			code = os.Args[3]
		}
		json.NewEncoder(os.Stdout).Encode(diagnostics.Record("mac", event, code, nil))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--updater" {
		if runtime.GOOS == "darwin" {
			if err := diagnostics.InitMacDestination(); err != nil {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "status": "blocked", "message": "Cannot preserve private Mac diagnostic settings before updating. No app files were replaced."})
				os.Exit(1)
			}
		}
		os.Exit(updater.Run(os.Args[2:], os.Stdout))
	}
	addr := flag.String("listen", "auto", "automatic LAN selection, or a specific private IPv4:port")
	gui := flag.Bool("gui", false, "inherited-pipe UI mode; no Terminal consent")
	terminalConsent := flag.Bool("terminal-consent", false, "use Terminal approvals instead of Mac Allow dialogs")
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
		if runtime.GOOS == "darwin" {
			if err := diagnostics.InitMacDestination(); err != nil {
				fmt.Fprintln(os.Stderr, "Cannot preserve private Mac diagnostic settings before updating. No app files were replaced.")
				os.Exit(1)
			}
		}
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
		fmt.Println("mac-lan TLS identity self-test passed; capture/input not started")
		return
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		fatal(errors.New("companion requires Apple Silicon macOS 14+"))
	}
	if err := diagnostics.InitMacDestination(); err != nil {
		fmt.Fprintln(os.Stderr, "Private Mac diagnostic settings could not be retained. Automatic updates are disabled until that is resolved; sharing can continue.")
		*noUpdateCheck = true
	}
	exe, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	helper := filepath.Join(filepath.Dir(exe), "MacDowCapture")
	helper, captureErr := resolveCaptureHelper(context.Background(), helper)
	if *check {
		if captureErr != nil {
			fatal(captureErr)
		}
		cmd := exec.Command(helper, "--check")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err = cmd.Run(); err != nil {
			fatal(err)
		}
		return
	}
	if captureErr != nil {
		fmt.Fprintln(os.Stderr, captureErr)
		fmt.Fprintln(os.Stderr, "Video setup is unavailable. Pairing and file transfers can still be used; see the Mac diagnostic report for the build stage.")
	}
	if err = hostSession(helper, *addr, *gui, *noUpdateCheck, *terminalConsent, captureErr); err != nil {
		fatal(err)
	}
}
func hostSession(helper, address string, gui, noUpdates, terminalConsent bool, captureErr error) error {
	diagnostics.Emit("mac", "startup", "ok", nil)
	all, err := localInterfaces()
	if err != nil {
		return err
	}
	address, err = chooseListen(address, all)
	if err != nil {
		return err
	}
	videoError := ""
	if captureErr != nil {
		videoError = "Native video setup failed. Check the Mac diagnostic report, then restart mac-lan after fixing the build. File transfers are available."
	} else {
		cmd := exec.Command(helper, "--check")
		if !gui {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}
		if err = cmd.Run(); err != nil {
			diagnostics.Emit("mac", "permissions-missing", "screen", nil)
			videoError = "Screen Recording is unavailable. Grant mac-lan Screen Recording access in System Settings and restart it. File transfers are available."
		} else {
			diagnostics.Emit("mac", "permissions-ok", "ok", map[string]int{"screenPermission": 1})
		}
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
	userHome, err := os.UserHomeDir()
	if err != nil {
		return errors.New("Cannot locate the Mac Downloads folder")
	}
	s.transfers = newTransferReceiver(filepath.Join(userHome, "Downloads", "mac-lan"))
	s.clipboard = macClipboard()
	s.videoError = videoError
	s.catalog = func() ([]window, error) {
		if videoError != "" {
			return []window{}, nil
		}
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
		if !terminalConsent {
			fmt.Println("Windows requested viewing. Choose Allow in the Mac dialog to start video.")
			return nativeConsent(fmt.Sprintf("Allow Windows to view %s?\n\n%d px / %d fps / %s\n\nKeyboard and mouse stay off until you click the Mac view to activate control on Windows.", scope, r.Width, r.FPS, streamDescription(r)))
		}
		fmt.Printf("\nWindows requests %s, %d px / %d fps / %s.\nAllow viewing and explicitly armed keyboard/mouse for this session? Type ALLOW: ", scope, r.Width, r.FPS, streamDescription(r))
		return scanner.Scan() && scanner.Text() == "ALLOW"
	}
	if gui {
		s.pair = func(peer, code string) bool {
			return broker.ask("pair", map[string]any{"peer": peer, "code": code, "message": "Allowing lets this PC send files to Downloads/mac-lan and view this screen until sharing stops. Keyboard and mouse stay off until you click the Mac view on Windows."}, 45*time.Second)
		}
	} else {
		s.pair = func(peer, code string) bool {
			fmt.Printf("Windows pairing request: %s. Compare this number on Windows.\n", code)
			if !terminalConsent {
				return nativeConsent("Pair with your Windows PC?\n\nCompare this number on Windows: " + code + "\n\nAllowing lets this PC send files to Downloads/mac-lan and view this screen until sharing stops. Only allow the request you started.")
			}
			fmt.Print("Type ALLOW to approve this pairing request: ")
			return scanner.Scan() && scanner.Text() == "ALLOW"
		}
	}
	s.stream = func(send func([]byte), status func(string), events <-chan input) error {
		codec := s.selected.Codec
		if codec == "" {
			codec = "h264"
		}
		diagnostics.Emit("mac", "stream-started", codec, map[string]int{"width": s.selected.Width, "fps": s.selected.FPS})
		if gui {
			broker.emit(map[string]any{"type": "state", "message": "Sharing active. Windows control remains explicit."})
		}
		err := runCapture(helper, s.selected, send, status, events)
		if err != nil {
			diagnostics.Emit("mac", "capture-failed", captureFailureCode(err), nil)
		} else {
			diagnostics.Emit("mac", "stream-ended", "ok", nil)
		}
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
	diagnostics.Emit("mac", "listener-ready", "ok", nil)
	if !gui {
		if stopAdvert, err := discovery.Advertise(actual.IP.String(), actual.Port, fp); err == nil {
			diagnostics.Emit("mac", "advertise-ready", "ok", nil)
			defer stopAdvert()
			fmt.Println("MacBook is discoverable. In Windows, choose Find my Mac.")
		} else {
			diagnostics.Emit("mac", "advertise-failed", "mdns", nil)
			fmt.Println("Local discovery unavailable. Use the connection details below.")
		}
	}
	if gui {
		broker.emit(map[string]any{"type": "ready", "address": actual.IP.String(), "port": actual.Port, "fingerprint": fp, "token": token})
	} else {
		fmt.Printf("\nmac-lan is ready - waiting for Windows. Ctrl+C stops sharing.\nMac identity: %s\n\nAdvanced connection details (only if discovery is unavailable):\nAddress: %s\nSHA-256 fingerprint: %s\nTemporary token: %s\n\nConnect from Windows, then choose Allow on this Mac to begin viewing. Video has not started yet.\n", strings.ToUpper(fp[:32]), listener.Addr(), fp, token)
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
	if !gui && !noUpdates {
		startHostUpdates(ctx, cancel, s)
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
func fatal(err error) {
	diagnostics.Record("mac", "fatal", "host-startup", nil)
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
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
	arguments := []string{"--stream", strconv.FormatUint(uint64(r.Window), 10), strconv.Itoa(r.Width), strconv.Itoa(r.FPS), strconv.Itoa(r.Quality), codec, strconv.Itoa(bitrate)}
	if r.Audio {
		arguments = append(arguments, "audio")
	}
	cmd := exec.Command(helper, arguments...)
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
		return &captureFailure{reason: "Cannot start Mac capture helper: " + err.Error(), code: "helper-start", cause: err}
	}
	done := make(chan struct{})
	defer close(done)
	var held heldState
	var heldMu sync.Mutex
	stderrDone := make(chan struct{})
	var nativeFailure *captureFailure // Written by stderr reader; read after stderrDone.
	go func() {
		defer close(stderrDone)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if failure := nativeFailureStatus(sc.Bytes()); failure != nil && nativeFailure == nil {
				nativeFailure = failure
			}
			if state, ok := nativeControlStatus(sc.Bytes()); ok {
				diagnostics.Emit("mac", "control-state", state.Code, map[string]int{"controlPermission": boolCount(state.Permission), "controlActive": boolCount(state.Active)})
			}
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
	// stdout closes as a helper exits, but its stderr and process teardown can
	// finish slightly later. Let that clean exit preserve its native reason and
	// exit status instead of racing it with an unconditional Kill.
	waited := make(chan error, 1)
	go func() { <-stderrDone; waited <- cmd.Wait() }()
	grace := time.NewTimer(time.Second)
	var waitErr error
	select {
	case waitErr = <-waited:
		grace.Stop()
	case <-grace.C:
		_ = cmd.Process.Kill()
		waitErr = <-waited
	}
	heldMu.Lock()
	snapshot := held
	heldMu.Unlock()
	if recoveryErr := recoverHeld(helper, snapshot); recoveryErr != nil {
		status("Native recovery could not confirm release; stop control and release modifiers on Mac: " + recoveryErr.Error())
	}
	if nativeFailure != nil {
		nativeFailure.cause = waitErr
		return nativeFailure
	}
	if waitErr != nil {
		return &captureFailure{reason: waitErr.Error(), code: "helper-exit", cause: waitErr}
	}
	if errors.Is(frameErr, io.EOF) {
		return nil
	}
	return frameErr
}
