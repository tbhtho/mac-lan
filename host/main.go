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
	"time"
)

func main() {
	addr := flag.String("listen", "", "explicit private IPv4:port, e.g. 192.168.1.20:4433")
	check := flag.Bool("check", false, "check existing Mac permissions; never request them")
	self := flag.Bool("self-test", false, "transport-independent non-capturing self-test")
	flag.Parse()
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
	ip, port, err := net.SplitHostPort(*addr)
	p, e := strconv.Atoi(port)
	if err != nil || e != nil || p < 1024 || p > 65535 || !privateIP(ip) {
		fatal(errors.New("--listen requires an explicit RFC1918 IPv4 and port 1024..65535"))
	}
	// No server or capture starts until existing permissions have been checked.
	cmd := exec.Command(helper, "--check")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		fatal(err)
	}
	cert, fp, err := identity()
	if err != nil {
		fatal(err)
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		fatal(err)
	}
	token := hex.EncodeToString(secret)
	s := &server{token: token, peerAllowed: privateIP, slot: make(chan struct{}, 1)}
	s.catalog = func() ([]window, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, helper, "--list").Output()
		if err != nil {
			return nil, errors.New("cannot list windows; check Screen Recording permission")
		}
		var ws []window
		err = json.Unmarshal(out, &ws)
		return ws, err
	}
	scanner := bufio.NewScanner(os.Stdin)
	s.consent = func(r start) bool {
		scope := "FULL DESKTOP"
		if r.Window != 0 {
			scope = fmt.Sprintf("ONLY window ID %d (child windows/dialogs excluded)", r.Window)
		}
		fmt.Printf("\nWindows requests %s, %d px / %d fps / JPEG %d.\nAllow viewing and explicitly armed keyboard/mouse for this session? Type ALLOW: ", scope, r.Width, r.FPS, r.Quality)
		return scanner.Scan() && scanner.Text() == "ALLOW"
	}
	s.stream = func(send func([]byte), status func(string), events <-chan input) error {
		return runCapture(helper, s.selected, send, status, events)
	}
	listener, err := net.Listen("tcp4", *addr)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\nMAC-DOW foreground host — Ctrl+C stops all capture/control.\nAddress: %s\nSHA-256 fingerprint: %s\nTemporary pairing token: %s\nCompare the fingerprint directly on Windows. Do not share token beyond your PC.\nNo capture until authenticated session + ALLOW.\n", *addr, fp, token)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}}
	go func() { <-ctx.Done(); listener.Close(); os.Exit(0) }() // child stdin EOF / lease watchdog independently releases input.
	if err = srv.Serve(tls.NewListener(listener, srv.TLSConfig)); err != nil && !errors.Is(err, net.ErrClosed) {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
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
