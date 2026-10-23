package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os/exec"
	"time"
)

type heldState struct {
	Type    string  `json:"type"`
	Keys    []int   `json:"keys"`
	Buttons []int   `json:"buttons"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
}

func parseHeld(b []byte) (heldState, bool) {
	var h heldState
	if len(b) > 2048 || json.Unmarshal(b, &h) != nil || h.Type != "held" || len(h.Keys) > 127 || len(h.Buttons) > 3 || math.IsNaN(h.X) || math.IsNaN(h.Y) || math.IsInf(h.X, 0) || math.IsInf(h.Y, 0) {
		return h, false
	}
	seen := map[int]bool{}
	for _, k := range h.Keys {
		if k < 0 || k > 126 || seen[k] {
			return h, false
		}
		seen[k] = true
	}
	seen = map[int]bool{}
	for _, k := range h.Buttons {
		if k < 0 || k > 2 || seen[k] {
			return h, false
		}
		seen[k] = true
	}
	return h, true
}
func recoverHeld(helper string, h heldState) error {
	if len(h.Keys) == 0 && len(h.Buttons) == 0 {
		return nil
	}
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, helper, "--release-safe")
	cmd.Stdin = bytes.NewReader(append(b, '\n'))
	return cmd.Run()
}
