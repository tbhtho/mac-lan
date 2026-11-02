package updater

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestBackgroundUpdatesPollCadenceAndRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Unix(0, 0)
	start := now
	var checks []time.Duration
	applies, errorsSeen := 0, 0
	runBackgroundUpdates(ctx, func(context.Context) (Stage, error) {
		checks = append(checks, now.Sub(start))
		if len(checks) < 8 {
			return Stage{}, ErrNoUpdate
		}
		return Stage{Plan: Plan{Tag: "v0.2.0"}}, nil
	}, func(Stage) error {
		applies++
		if applies == 1 {
			return errors.New("temporary failure")
		}
		return nil
	}, func(error) { errorsSeen++ }, func() time.Time { return now }, func(_ context.Context, d time.Duration) bool { now = now.Add(d); return true })
	expected := []time.Duration{0, 5 * time.Second, 10 * time.Second, 15 * time.Second, 20 * time.Second, 25 * time.Second, 30 * time.Second, 60 * time.Second, 90 * time.Second}
	if !reflect.DeepEqual(checks, expected) || applies != 2 || errorsSeen != 1 {
		t.Fatalf("checks=%v applies=%d errors=%d", checks, applies, errorsSeen)
	}
}

func TestBackgroundUpdatesLongChecksDoNotCatchUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Unix(0, 0)
	checks := 0
	runBackgroundUpdates(ctx, func(context.Context) (Stage, error) {
		checks++
		now = now.Add(40 * time.Second)
		return Stage{}, errors.New("network unavailable")
	}, nil, nil, func() time.Time { return now }, func(_ context.Context, d time.Duration) bool {
		if d != 30*time.Second {
			t.Errorf("delay=%v", d)
		}
		return false
	})
	if checks != 1 {
		t.Fatal(checks)
	}
}

func TestBackgroundUpdatesCancellationSkipsApply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runBackgroundUpdates(ctx, func(context.Context) (Stage, error) { cancel(); return Stage{}, nil }, func(Stage) error { t.Error("apply after cancellation"); return nil }, nil, time.Now, func(context.Context, time.Duration) bool { t.Error("wait after cancellation"); return false })
}
