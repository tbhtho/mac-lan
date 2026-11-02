package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

type pendingAutomaticUpdate struct {
	Install, Current, Tag, Directory string
}

func (e *Engine) pendingPath(install string) (string, error) {
	abs, err := filepath.Abs(install)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(abs))
	return filepath.Join(e.Root, "pending-"+hex.EncodeToString(hash[:16])+".json"), nil
}

// PrepareAutomaticUpdate downloads in the background. Installation remains a
// separate, fresh-verified operation.
func (e *Engine) PrepareAutomaticUpdate(ctx context.Context, install string) (Stage, error) {
	if err := e.installIdentity(install); err != nil {
		return Stage{}, err
	}
	p, err := e.Check(ctx, BuildConfig().DefaultChannel)
	if err != nil {
		return Stage{}, err
	}
	if err = rejectLinks(e.Root, true); err != nil {
		return Stage{}, err
	}
	pendingPath, err := e.pendingPath(install)
	if err != nil {
		return Stage{}, err
	}
	abs, err := filepath.Abs(install)
	if err != nil {
		return Stage{}, err
	}
	var pending pendingAutomaticUpdate
	if info, statErr := os.Lstat(pendingPath); statErr == nil && info.Mode().IsRegular() && readJSON(pendingPath, &pending) == nil && pending.Install == abs && pending.Current == e.Current && pending.Tag == p.Tag {
		if ready, readyErr := e.Preflight(ctx, pending.Directory, install); readyErr == nil && ready.Plan.Tag == p.Tag {
			// CanApply in stage.json is only display state. Trust comes from
			// fresh signature, asset/commit equality and archive digest checks.
			ready.CanApply = true
			return ready, nil
		}
	}
	s, err := e.Stage(ctx, BuildConfig().DefaultChannel)
	if err != nil {
		return s, err
	}
	if !s.CanApply {
		return s, ErrProvenance
	}
	ready, err := e.Preflight(ctx, s.Directory, install)
	if err != nil {
		return ready, err
	}
	if err = writeJSON(pendingPath, pendingAutomaticUpdate{Install: abs, Current: e.Current, Tag: ready.Plan.Tag, Directory: ready.Directory}); err != nil {
		return ready, err
	}
	ready.CanApply = true
	return ready, nil
}

// StartBackgroundUpdates checks immediately, every five seconds for the first
// thirty seconds, then every thirty seconds. Checks and scheduling are serialized.
// A successful callback has scheduled installation and ends polling; failures
// retry on the next poll. Capture/input cleanup belongs to the caller.
func StartBackgroundUpdates(ctx context.Context, install string, onReady func(Stage) error, onError func(error)) {
	go runBackgroundUpdates(ctx, func(checkCtx context.Context) (Stage, error) {
		e, err := NewEngine()
		if err != nil {
			return Stage{}, err
		}
		return e.PrepareAutomaticUpdate(checkCtx, install)
	}, onReady, onError, time.Now, waitForUpdatePoll)
}

func waitForUpdatePoll(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func runBackgroundUpdates(ctx context.Context, prepare func(context.Context) (Stage, error), onReady func(Stage) error, onError func(error), now func() time.Time, wait func(context.Context, time.Duration) bool) {
	started := now()
	for ctx.Err() == nil {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		stage, err := prepare(checkCtx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err == nil && onReady != nil {
			err = onReady(stage)
			if err == nil {
				return
			}
		}
		if err != nil && !errors.Is(err, ErrNoUpdate) && ctx.Err() == nil && onError != nil {
			onError(err)
		}
		if ctx.Err() != nil {
			return
		}
		delay := 30 * time.Second
		if now().Sub(started) < 30*time.Second {
			delay = 5 * time.Second
		}
		// Wait after completion, so slow checks cannot create overlap or a
		// catch-up burst of downloads and failed apply attempts.
		if !wait(ctx, delay) {
			return
		}
	}
}

// ScheduleApply creates a private copy of the currently executing Mac host so
// the updater keeps running after the listener, capture helper and app exit.
// The worker revalidates the signed release and archive after waiting for PID.
func (e *Engine) ScheduleApply(ctx context.Context, stage, install string, parentPID int, relaunch bool) (string, error) {
	if runtime.GOOS != "darwin" || e.OS != "darwin" || parentPID != os.Getpid() {
		return "", errors.New("Mac update worker requires the current host PID")
	}
	s, err := e.Preflight(ctx, stage, install)
	if err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	runner, err := e.prepareRunner(executable)
	if err != nil {
		return "", err
	}
	result := filepath.Join(stage, "worker-result.json")
	arguments := []string{"--updater", "apply", "--stage", stage, "--install", install, "--wait-pid", strconv.Itoa(parentPID), "--confirm", s.Plan.confirmation(), "--result-file", result}
	if relaunch {
		arguments = append(arguments, "--relaunch")
	}
	command := exec.Command(runner, arguments...)
	command.Dir = filepath.Dir(runner)
	detachedCommand(command)
	log, err := os.OpenFile(filepath.Join(filepath.Dir(runner), "worker.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer log.Close()
	command.Stdout, command.Stderr = log, log
	if err = command.Start(); err != nil {
		return "", err
	}
	if err = command.Process.Release(); err != nil {
		return "", err
	}
	return result, nil
}

func (e *Engine) prepareRunner(executable string) (string, error) {
	if err := rejectLinks(e.Root, true); err != nil {
		return "", err
	}
	if err := os.MkdirAll(e.Root, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("running updater is not a regular executable")
	}
	source, err := os.Open(executable)
	if err != nil {
		return "", err
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() <= 0 || opened.Size() > MaxArchive {
		return "", errors.New("running updater changed or exceeded size limit")
	}
	root, err := os.MkdirTemp(e.Root, "runner-")
	if err != nil {
		return "", err
	}
	complete := false
	defer func() {
		if !complete && e.owned(root) {
			_ = os.RemoveAll(root)
		}
	}()
	runner := filepath.Join(root, "MacDowHost")
	target, err := os.OpenFile(runner, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(target, hash), io.LimitReader(source, opened.Size()+1))
	if err == nil && written != opened.Size() {
		err = errors.New("running updater changed during copy")
	}
	if err == nil {
		err = target.Sync()
	}
	closeErr := target.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	copied, err := os.Open(runner)
	if err != nil {
		return "", err
	}
	check := sha256.New()
	_, err = io.Copy(check, copied)
	copied.Close()
	if err != nil {
		return "", err
	}
	if string(hash.Sum(nil)) != string(check.Sum(nil)) {
		return "", errors.New("update worker copy mismatch")
	}
	if err = os.Chmod(runner, 0700); err != nil {
		return "", err
	}
	complete = true
	return runner, nil
}
