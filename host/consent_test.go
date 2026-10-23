package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNativeConsentFailsClosed(t *testing.T) {
	for _, reply := range []string{"Deny", "", "Allow extra", "gave up:true"} {
		if approveDialog(context.Background(), "a prompt", func(context.Context, string) ([]byte, error) { return []byte(reply), nil }) {
			t.Fatal("accepted", reply)
		}
	}
	if approveDialog(context.Background(), "prompt", func(context.Context, string) ([]byte, error) { return []byte("Allow"), errors.New("cancelled") }) {
		t.Fatal("failed dialog approved")
	}
	if !approveDialog(context.Background(), "prompt", func(_ context.Context, message string) ([]byte, error) {
		if message != "prompt" {
			t.Fatal(message)
		}
		return []byte("Allow\n"), nil
	}) {
		t.Fatal("explicit Allow rejected")
	}
	if !strings.Contains(consentScript, "item 1 of argv") || !strings.Contains(consentScript, "gave up") {
		t.Fatal("dialog prompt must be data with an expiry")
	}
}
