package main

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Native messages stay on the authenticated LAN session. Only fixed labels and
// an explicitly parsed known-framework integer may enter remote diagnostics.
type captureFailure struct {
	reason string
	code   string
	cause  error
}

func (e *captureFailure) Error() string { return e.reason }
func (e *captureFailure) Unwrap() error { return e.cause }

func captureFailureCode(err error) string {
	var failure *captureFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return "native-helper"
}

var encoderUnavailable = regexp.MustCompile(`^Hardware H\.264 unavailable \((-?[0-9]{1,10})\)`)
var encoderPreparation = regexp.MustCompile(`^Hardware encoder preparation failed: (-?[0-9]{1,10})(?:$|[^0-9])`)
var setupNSError = regexp.MustCompile(`^Capture setup failed: Error Domain=(com\.apple\.ScreenCaptureKit\.SCStreamErrorDomain|NSOSStatusErrorDomain) Code=(-?[0-9]{1,10})(?:$| )`)

func nativeFailureCode(message string) string {
	if match := setupNSError.FindStringSubmatch(message); len(match) > 2 {
		if code, err := strconv.ParseInt(match[2], 10, 32); err == nil {
			prefix := "sck-setup."
			if match[1] == "NSOSStatusErrorDomain" {
				prefix = "osstatus-setup."
			}
			return prefix + strconv.FormatInt(code, 10)
		}
	}
	if match := encoderUnavailable.FindStringSubmatch(message); len(match) > 1 {
		return "h264-unavailable." + match[1]
	}
	if match := encoderPreparation.FindStringSubmatch(message); len(match) > 1 {
		return "h264-prepare." + match[1]
	}
	for _, item := range []struct{ prefix, code string }{
		{"Hardware H.264 unavailable", "h264-unavailable"},
		{"Hardware encoder preparation failed", "h264-prepare"},
		{"Hardware encoder not active", "h264-not-hardware"},
		{"Capture setup failed:", "capture-setup"},
		{"Capture stopped:", "capture-stopped"},
		{"Grant Screen Recording", "screen-permission"},
		{"Main Mac display unavailable", "display-unavailable"},
		{"Selected window closed", "window-unavailable"},
		{"Selected window has no controllable owner", "window-owner"},
		{"Selected Mac window resized", "window-resized"},
		{"Capture dimensions exceed resource limit", "capture-dimensions"},
		{"Viewer transport pipe closed", "viewer-pipe"},
		{"Native input mailbox overflow/EOF", "input-pipe"},
		{"Viewer heartbeat expired", "viewer-heartbeat"},
		{"Usage: MacDowCapture", "helper-arguments"},
	} {
		if strings.HasPrefix(message, item.prefix) {
			return item.code
		}
	}
	return ""
}

func nativeFailureStatus(line []byte) *captureFailure {
	if len(line) > 2048 {
		return nil
	}
	var status struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Paused  bool   `json:"paused"`
	}
	if json.Unmarshal(line, &status) != nil || status.Type != "status" || !status.Paused {
		return nil
	}
	code := nativeFailureCode(status.Message)
	if code == "" {
		return nil // Control pauses and recoverable encoder warnings are not exits.
	}
	message := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, status.Message)
	if len(message) > 1024 {
		message = strings.ToValidUTF8(message[:1024], "") + "..."
	}
	return &captureFailure{reason: strings.TrimSpace(message), code: code}
}
