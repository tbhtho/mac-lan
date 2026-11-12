//go:build windows

package remote

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
)

// shellCommand builds the Windows command. cmd.exe needs its command line passed
// verbatim: Go's argument quoting would escape the user's own quotes.
func shellCommand(shell, command string) (*exec.Cmd, error) {
	attributes := &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow | createNewProcessGroup}
	switch shell {
	case "", "cmd":
		path := os.Getenv("ComSpec")
		if path == "" {
			path = "cmd.exe"
		}
		cmd := exec.Command(path)
		// Code page 65001 makes built-in commands and most tools print UTF-8.
		attributes.CmdLine = `cmd.exe /d /s /c "chcp 65001>nul & ` + command + `"`
		cmd.SysProcAttr = attributes
		return cmd, nil
	case "powershell", "pwsh":
		cmd := exec.Command(shell+".exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
			"[Console]::OutputEncoding=[Text.Encoding]::UTF8; "+command)
		cmd.SysProcAttr = attributes
		return cmd, nil
	}
	return nil, errors.New("shell must be cmd, powershell or pwsh on Windows")
}

// killTree ends the command and everything it started.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if kill.Run() != nil {
		_ = cmd.Process.Kill()
	}
}

func defaultShellName() string { return "cmd" }

// keepAwake is only needed on a Mac, which sleeps when idle by default.
func keepAwake(int) {}

// systemName is the operating system as a person would name it.
func systemName() string {
	version := windows.RtlGetVersion()
	name := "Windows 10"
	if version.MajorVersion == 10 && version.BuildNumber >= 22000 {
		name = "Windows 11"
	} else if version.MajorVersion != 10 {
		name = "Windows " + strconv.Itoa(int(version.MajorVersion)) + "." + strconv.Itoa(int(version.MinorVersion))
	}
	return name + " (build " + strconv.Itoa(int(version.BuildNumber)) + ")"
}

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	gdi32                      = windows.NewLazySystemDLL("gdi32.dll")
	procSetProcessDPIAware     = user32.NewProc("SetProcessDPIAware")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	dpiOnce                    sync.Once
)

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	Used          uint32
	Important     uint32
}

// captureScreen returns a JPEG of the primary display, at most width pixels wide.
func captureScreen(width int) ([]byte, int, int, error) {
	const failed = "Windows could not take a picture of its screen. Make sure the PC is unlocked and mac-lan runs in your desktop session."
	// Without this, a scaled display reports a smaller size and the picture is cropped.
	dpiOnce.Do(func() { _, _, _ = procSetProcessDPIAware.Call() })
	w, _, _ := procGetSystemMetrics.Call(0)
	h, _, _ := procGetSystemMetrics.Call(1)
	if w < 16 || h < 16 || w > 16384 || h > 16384 {
		return nil, 0, 0, errors.New(failed)
	}
	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return nil, 0, 0, errors.New(failed)
	}
	defer procReleaseDC.Call(0, screen)
	memory, _, _ := procCreateCompatibleDC.Call(screen)
	if memory == 0 {
		return nil, 0, 0, errors.New(failed)
	}
	defer procDeleteDC.Call(memory)
	bitmap, _, _ := procCreateCompatibleBitmap.Call(screen, w, h)
	if bitmap == 0 {
		return nil, 0, 0, errors.New(failed)
	}
	defer procDeleteObject.Call(bitmap)
	previous, _, _ := procSelectObject.Call(memory, bitmap)
	const sourceCopy, captureLayered = 0x00CC0020, 0x40000000
	copied, _, _ := procBitBlt.Call(memory, 0, 0, w, h, screen, 0, 0, sourceCopy|captureLayered)
	procSelectObject.Call(memory, previous)
	if copied == 0 {
		return nil, 0, 0, errors.New(failed)
	}
	header := bitmapInfoHeader{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	picture := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	lines, _, _ := procGetDIBits.Call(memory, bitmap, 0, h, uintptr(unsafe.Pointer(&picture.Pix[0])), uintptr(unsafe.Pointer(&header)), 0)
	if lines == 0 {
		return nil, 0, 0, errors.New(failed)
	}
	for i := 0; i < len(picture.Pix); i += 4 { // BGRA to RGBA
		picture.Pix[i], picture.Pix[i+2], picture.Pix[i+3] = picture.Pix[i+2], picture.Pix[i], 255
	}
	picture = shrink(picture, width)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, picture, &jpeg.Options{Quality: 82}); err != nil {
		return nil, 0, 0, errors.New(failed)
	}
	return out.Bytes(), picture.Bounds().Dx(), picture.Bounds().Dy(), nil
}
