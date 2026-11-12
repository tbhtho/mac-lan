package main

import (
	"os"
	"strings"
	"testing"
)

func TestStandbyServiceDefinition(t *testing.T) {
	text := standbyPlistText("/Applications/mac-lan.app/Contents/Helpers/MacDowHost", "/Users/a&b/Library/Application Support/MAC-DOW/standby/MacDowHost")
	for _, want := range []string{"<string>" + standbyLabel + "</string>", "<string>/Applications/mac-lan.app/Contents/Helpers/MacDowHost</string>", "/Users/a&amp;b/Library/Application Support/MAC-DOW/standby/MacDowHost", "--link-service", "<key>KeepAlive</key><true/>", "<key>RunAtLoad</key><true/>"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the launchd definition lacks %q", want)
		}
	}
	if strings.Contains(text, "a&b") {
		t.Fatal("paths must be escaped")
	}
	// Written out on request so it can be checked with plutil on a Mac.
	if out := os.Getenv("MACLAN_PLIST_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if code := standbyCommand(t.TempDir(), []string{"maybe"}); code != 2 {
		t.Fatal("an unknown standby argument must be refused")
	}
}
