package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const consentScript = `on run argv
 set answer to display dialog (item 1 of argv) with title "mac-lan" buttons {"Deny", "Allow"} default button "Deny" cancel button "Deny" giving up after 40
 if gave up of answer then return "Deny"
 return button returned of answer
end run`

func approveDialog(ctx context.Context, message string, run func(context.Context, string) ([]byte, error)) bool {
	response, err := run(ctx, message)
	return err == nil && len(response) < 32 && strings.TrimSpace(string(response)) == "Allow"
}
func nativeConsent(message string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return approveDialog(ctx, message, func(ctx context.Context, text string) ([]byte, error) {
		return exec.CommandContext(ctx, "/usr/bin/osascript", "-e", consentScript, "--", text).Output()
	})
}
