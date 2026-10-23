package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tbhtho/MAC-DOW/host/internal/diagnostics"
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
)

// The public Go launcher and host carry the same verified native UI source.
var NativeMenuSourceGzip string
var NativeMenuSourceSHA256 string
var NativeMenuInfoBase64 string

func appRootForExecutable(executable string) string {
	if filepath.Base(executable) != "mac-lan" || filepath.Base(filepath.Dir(executable)) != "MacOS" {
		return ""
	}
	contents := filepath.Dir(filepath.Dir(executable))
	root := filepath.Dir(contents)
	if filepath.Base(contents) != "Contents" || !strings.HasSuffix(root, ".app") {
		return ""
	}
	return root
}

func readMenuMarker(path string) (updater.Marker, error) {
	var marker updater.Marker
	input, _, err := openNativeFile(path, 4096)
	if err != nil {
		return marker, err
	}
	defer input.Close()
	decoder := json.NewDecoder(io.LimitReader(input, 4097))
	if decoder.Decode(&marker) != nil || decoder.Decode(new(any)) != io.EOF || marker.Product != "MAC-DOW" || marker.RepositoryID != 1430459188 || marker.OS != "darwin" || marker.Arch != "arm64" || len(marker.Commit) != 40 {
		return marker, errors.New("Mac installation identity is invalid")
	}
	if _, err = updater.ParseVersion(marker.Version); err != nil {
		return marker, err
	}
	return marker, nil
}

func menuDirectory(path string) error {
	path = filepath.Clean(path)
	parent := filepath.Dir(path)
	if parent != path {
		if err := menuDirectory(parent); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err = os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Mac app directory contains a link or non-directory")
	}
	return nil
}

func copyMenuFile(source, destination string, mode os.FileMode) error {
	for current := source; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Mac source path contains a link or missing file")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	input, _, err := openNativeFile(source, 128<<20)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, io.LimitReader(input, 128<<20))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// A legacy updater may replace only its own preview folder. Its next launch
// installs a separate app without moving/removing that folder or its neighbors.
func installMenuApp(preview, destination string) (string, error) {
	marker, err := readMenuMarker(filepath.Join(preview, "MAC-DOW-INSTALL.json"))
	if err != nil || marker.Layout != "commandline-preview" || marker.Version != updater.BuildConfig().Version {
		return "", errors.New("Only the matching Mac preview can install its menu-bar companion")
	}
	if filepath.Ext(destination) != ".app" || !filepath.IsAbs(destination) {
		return "", errors.New("Mac app destination must be an absolute .app path")
	}
	if err = menuDirectory(filepath.Dir(destination)); err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(destination); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("Existing Mac app destination is unsafe")
		}
		previous, readErr := readMenuMarker(filepath.Join(destination, "Contents", "Resources", "MAC-DOW-INSTALL.json"))
		if readErr != nil || previous.Layout != "app-bundle" || previous.Version != marker.Version || previous.Commit != marker.Commit {
			return "", errors.New("An existing app was preserved; open it or install the new app from the release")
		}
		if err = updater.ValidateMacAppBundle(destination, runtime.GOOS != "windows"); err != nil {
			return "", err
		}
		return destination, nil
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	temp, err := os.MkdirTemp(filepath.Dir(destination), ".mac-lan-install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp) // Only this invocation's temporary directory.
	staged := filepath.Join(temp, "mac-lan.app")
	resources := filepath.Join(staged, "Contents", "Resources")
	for _, dir := range []string{resources, filepath.Join(staged, "Contents", "MacOS"), filepath.Join(staged, "Contents", "Helpers"), filepath.Join(resources, "licenses")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
	}
	for _, name := range []string{"Contents/MacOS/mac-lan", "Contents/Helpers/MacDowHost"} {
		if err = copyMenuFile(filepath.Join(preview, "MacDowHost"), filepath.Join(staged, filepath.FromSlash(name)), 0700); err != nil {
			return "", err
		}
	}
	for _, name := range []string{"COMMIT.txt", "BUILD-PROVENANCE.json", "README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"} {
		if err = copyMenuFile(filepath.Join(preview, name), filepath.Join(resources, name), 0600); err != nil {
			return "", err
		}
	}
	// Do not copy user files or arbitrary subdirectories from an old extraction.
	for _, name := range []string{"zeroconf-LICENSE.txt", "webview2-NOTICE.txt", "webview2-LICENSE.txt", "miekg-dns-LICENSE.txt", "gorilla-websocket.txt", "golang-x-LICENSE.txt", "go-LICENSE.txt"} {
		source := filepath.Join(preview, "licenses", name)
		if _, err = os.Lstat(source); os.IsNotExist(err) {
			continue
		}
		if err = copyMenuFile(source, filepath.Join(resources, "licenses", name), 0600); err != nil {
			return "", err
		}
	}
	plist := menuPlistTemplate
	if NativeMenuInfoBase64 != "" {
		decoded, err := base64.StdEncoding.Strict().DecodeString(NativeMenuInfoBase64)
		if err != nil || len(decoded) > 16384 {
			return "", errors.New("Embedded app identity is invalid")
		}
		plist = string(decoded)
	}
	plist = strings.ReplaceAll(plist, "@NUMERIC_VERSION@", strings.Split(marker.Version, "-")[0])
	if err = os.WriteFile(filepath.Join(staged, "Contents", "Info.plist"), []byte(plist), 0600); err != nil {
		return "", err
	}
	marker.Layout = "app-bundle"
	data, _ := json.Marshal(marker)
	if err = os.WriteFile(filepath.Join(resources, "MAC-DOW-INSTALL.json"), data, 0600); err != nil {
		return "", err
	}
	if err = updater.ValidateMacAppBundle(staged, runtime.GOOS != "windows"); err != nil {
		return "", err
	}
	if err = os.Rename(staged, destination); err != nil {
		return "", err
	}
	return destination, nil
}

// Keep the entry process alive while AppKit runs so LaunchServices associates
// permission requests with the app, and updater workers can wait for its PID.
func launchMacMenu() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	bundle := appRootForExecutable(executable)
	if bundle == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		bundle, err = installMenuApp(filepath.Dir(executable), filepath.Join(home, "Applications", "mac-lan.app"))
		if err != nil {
			return err
		}
		return exec.Command("/usr/bin/open", bundle).Run()
	}
	if err = updater.ValidateMacAppBundle(bundle, true); err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	if err = diagnostics.InitMacDestination(); err != nil {
		return err
	}
	builder := nativeBuilder{encoded: NativeMenuSourceGzip, sourceSHA: NativeMenuSourceSHA256, cacheBase: cache, goos: "darwin", goarch: "arm64", menu: true, run: runNativeCommand, emit: func(event, code string, counts map[string]int) {
		event = strings.Replace(event, "native-build-", "menu-build-", 1)
		diagnostics.Emit("mac", event, code, counts)
	}}
	menu, err := builder.resolve(context.Background(), "")
	if err != nil {
		return fmt.Errorf("Cannot prepare the mac-lan menu using existing Apple tools: %w", err)
	}
	child := exec.Command(menu, "--app-bundle", bundle, "--launcher-pid", strconv.Itoa(os.Getpid()))
	child.Stdin = nil
	// Native stderr has no pairing data. Leave stdout disconnected from Terminal.
	child.Stderr = os.Stderr
	if err = child.Start(); err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		return err
	case sig := <-signals:
		_ = child.Process.Signal(sig)
		select {
		case err := <-done:
			return err
		case <-time.After(8 * time.Second):
			_ = child.Process.Kill()
			return <-done
		}
	}
}

func showMenuFailure(err error) {
	diagnostics.Record("mac", "fatal", "launcher", nil)
	text := "mac-lan could not start its menu-bar companion. Existing Apple command-line tools are required. No developer tools or OS settings were changed.\n\n" + err.Error()
	if len(text) > 3000 {
		text = text[:3000]
	}
	// argv carries text as data; no shell or AppleScript string interpolation.
	script := `on run argv
display alert "mac-lan could not start" message (item 1 of argv) as critical
end run`
	_ = exec.Command("/usr/bin/osascript", "-e", script, "--", text).Run()
}

// Source-built fixtures can create the same stable LaunchServices identity.
const menuPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>mac-lan</string><key>CFBundleIdentifier</key><string>com.tbhtho.macdow.host</string>
<key>CFBundleName</key><string>mac-lan</string><key>CFBundleDisplayName</key><string>mac-lan</string><key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>@NUMERIC_VERSION@</string><key>CFBundleShortVersionString</key><string>@NUMERIC_VERSION@</string>
<key>LSMinimumSystemVersion</key><string>14.0</string><key>LSUIElement</key><true/><key>NSHighResolutionCapable</key><true/>
<key>NSLocalNetworkUsageDescription</key><string>Connect Windows and this Mac on your local network for screen mirroring.</string>
<key>NSBonjourServices</key><array><string>_macdow._tcp</string></array>
<key>NSScreenCaptureUsageDescription</key><string>Share only the screen or window you approve.</string>
</dict></plist>`
