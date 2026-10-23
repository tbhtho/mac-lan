package updater

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func LocalEngine() (*Engine, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	c := BuildConfig()
	return &Engine{Root: filepath.Join(cache, "MAC-DOW", "updates"), OS: runtime.GOOS, Arch: runtime.GOARCH, Current: c.Version}, nil
}
func NewEngine() (*Engine, error) {
	e, err := LocalEngine()
	if err != nil {
		return nil, err
	}
	g, err := ExistingGH()
	if err != nil {
		return nil, err
	}
	e.Source = g
	e.Verify = g.Verify
	return e, nil
}

type Result struct {
	OK      bool   `json:"ok"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Version string `json:"version,omitempty"`
	Plan    *Plan  `json:"plan,omitempty"`
	Stage   *Stage `json:"stage,omitempty"`
	Journal string `json:"journal,omitempty"`
}

func Run(args []string, out io.Writer) int {
	if len(args) > 0 && args[0] == "console" {
		return Console(os.Stdin, out)
	}
	result := Result{}
	var resultFile string
	defer func() {
		_ = json.NewEncoder(out).Encode(result)
		if resultFile != "" {
			if local, er := LocalEngine(); er == nil && local.owned(filepath.Dir(resultFile)) && filepath.Base(resultFile) == "worker-result.json" {
				_ = writeJSON(resultFile, result)
			}
		}
	}()
	if len(args) == 0 {
		result.Message = "Use info, check, stage, preflight, apply, rollback or console."
		return 2
	}
	if args[0] == "info" {
		result = Result{OK: true, Status: "info", Version: BuildConfig().Version, Message: "Existing GitHub CLI authentication; SHA-256 + signed-release provenance required for automatic apply."}
		return 0
	}
	flags := flag.NewFlagSet("MAC-DOW updates", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	channel := flags.String("channel", BuildConfig().DefaultChannel, "stable or preview")
	stage := flags.String("stage", "", "owned staged update")
	install := flags.String("install", "", "recognized portable install")
	confirm := flags.String("confirm", "", "exact explicit confirmation")
	pid := flags.Int("wait-pid", 0, "wait for parent to exit")
	relaunch := flags.Bool("relaunch", false, "user explicitly approved relaunch")
	flags.StringVar(&resultFile, "result-file", "", "owned worker result")
	journal := flags.String("journal", "", "rollback journal")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		result.Message = "Invalid updater arguments"
		return 2
	}
	var e *Engine
	var err error
	e, err = NewEngine()
	if err != nil {
		result.Status = "manual"
		result.Message = err.Error()
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	switch args[0] {
	case "check":
		var p Plan
		p, err = e.Check(ctx, *channel)
		if errors.Is(err, ErrNoUpdate) {
			result = Result{OK: true, Status: "current", Version: e.Current, Message: err.Error()}
			return 0
		}
		if err == nil {
			result = Result{OK: true, Status: "available", Version: e.Current, Plan: &p, Message: "Compatible update available; download only after Update action."}
		}
	case "stage":
		var s Stage
		s, err = e.Stage(ctx, *channel)
		if err == nil {
			result = Result{OK: true, Status: "staged", Version: e.Current, Stage: &s, Message: s.Message}
		}
	case "preflight":
		var s Stage
		s, err = e.Preflight(ctx, *stage, *install)
		if err == nil {
			s.CanApply = true
			result = Result{OK: true, Status: "ready", Stage: &s, Message: "Revalidated update; explicit apply/relaunch confirmation still required."}
		}
	case "apply":
		if *confirm == "" {
			err = errors.New("explicit apply confirmation required")
			break
		}
		if err = WaitParent(*pid); err == nil {
			var j string
			j, err = e.Apply(ctx, *stage, *install, *confirm, *relaunch)
			result = Result{OK: err == nil, Status: "applied", Journal: j, Message: "Update applied; settings preserved and backup retained."}
		}
	case "rollback-preflight":
		var s Stage
		s, err = e.PrepareRollback(ctx, *journal, *install)
		if err == nil {
			result = Result{OK: true, Status: "ready", Stage: &s, Message: "Previous release downloaded and signed provenance verified. Explicit rollback confirmation required."}
		}
	case "rollback":
		if *confirm != "ROLLBACK" {
			err = errors.New("explicit rollback confirmation required")
			break
		}
		if err = WaitParent(*pid); err == nil {
			err = e.Rollback(ctx, *journal, *install, *confirm, *relaunch)
			result = Result{OK: err == nil, Status: "rolled-back", Journal: *journal, Message: "Previous signed release reinstalled; external settings preserved."}
		}
	default:
		err = errors.New("unknown updater command")
	}
	if err != nil {
		result.OK = false
		result.Status = "blocked"
		result.Message = err.Error()
		return 1
	}
	return 0
}
func StartupCheck(out io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	e, err := NewEngine()
	if err != nil {
		fmt.Fprintln(out, "Updates: existing gh authentication unavailable; run MacDowHost --updates for manual fallback.")
		return
	}
	p, err := e.Check(ctx, BuildConfig().DefaultChannel)
	if err == nil {
		fmt.Fprintf(out, "Update %s available. Stop streaming, then run MacDowHost --updates. No automatic install.\n", p.Tag)
	} else if !errors.Is(err, ErrNoUpdate) {
		fmt.Fprintln(out, "Update check unavailable; existing private GitHub access is required. No credentials saved.")
	} else {
		fmt.Fprintln(out, "MAC-DOW", e.Current, "- no newer compatible update.")
	}
}
func Console(in io.Reader, out io.Writer) int {
	fmt.Fprintln(out, "MAC-DOW updates", BuildConfig().Version, "- Check / Update (stage) / Apply / Rollback / Quit. No capture or listener in this mode.")
	fmt.Fprintln(out, "Uses existing gh auth only. Private manual fallback: https://github.com/tbhtho/MAC-DOW/releases . No login or OS security changes are performed.")
	scanner := bufio.NewScanner(in)
	var staged *Stage
	var journal string
	channel := BuildConfig().DefaultChannel
	for {
		fmt.Fprint(out, "[C]heck [U]pdate [A]pply [R]ollback [S]table [P]review [Q]uit > ")
		if !scanner.Scan() {
			return 0
		}
		command := scanner.Text()
		if command == "Q" || command == "q" {
			return 0
		}
		if command == "S" || command == "s" {
			channel = "stable"
			fmt.Fprintln(out, "Stable channel selected")
			continue
		}
		if command == "P" || command == "p" {
			channel = "preview"
			fmt.Fprintln(out, "Preview channel selected")
			continue
		}
		var e *Engine
		var err error
		e, err = NewEngine()
		if err != nil {
			fmt.Fprintln(out, err)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		switch command {
		case "C", "c":
			p, er := e.Check(ctx, channel)
			if er != nil {
				fmt.Fprintln(out, er)
			} else {
				fmt.Fprintf(out, "Available %s (%s/%s, %.1f MiB). U downloads; no automatic replacement.\n", p.Tag, p.OS, p.Arch, float64(p.Asset.Size)/(1<<20))
			}
		case "U", "u":
			s, er := e.Stage(ctx, channel)
			if er != nil {
				fmt.Fprintln(out, er)
			} else {
				staged = &s
				fmt.Fprintln(out, s.Message, "\nStaged:", s.Directory)
			}
		case "A", "a":
			if staged == nil {
				fmt.Fprintln(out, "Stage an update first.")
				break
			}
			if !staged.CanApply {
				fmt.Fprintln(out, "Automatic apply blocked:", staged.Message)
				break
			}
			exe, er := os.Executable()
			if er != nil {
				fmt.Fprintln(out, er)
				break
			}
			fmt.Fprintf(out, "This closes update mode and replaces the portable companion, keeping a rollback backup. Type %s: ", staged.Plan.confirmation())
			if !scanner.Scan() || scanner.Text() != staged.Plan.confirmation() {
				fmt.Fprintln(out, "Cancelled")
				break
			}
			journal, er = e.Apply(ctx, staged.Directory, filepath.Dir(exe), staged.Plan.confirmation(), false)
			if er != nil {
				fmt.Fprintln(out, er)
			} else {
				fmt.Fprintln(out, "Applied. Backup journal:", journal, "Run the updated host yourself; permissions/pairing are never reused automatically.")
				cancel()
				return 0
			}
		case "R", "r":
			if journal == "" {
				fmt.Fprint(out, "Exact updater transaction.json path > ")
				if !scanner.Scan() {
					break
				}
				journal = scanner.Text()
			}
			fmt.Fprint(out, "Rollback re-downloads and verifies the older signed release; network/private GitHub access required. Type ROLLBACK (no capture restart): ")
			if scanner.Scan() && scanner.Text() == "ROLLBACK" {
				exe, er := os.Executable()
				if er == nil {
					er = e.Rollback(ctx, journal, filepath.Dir(exe), "ROLLBACK", false)
				}
				fmt.Fprintln(out, er)
			}
		default:
			fmt.Fprintln(out, "Choose C, U, A, R or Q.")
		}
		cancel()
	}
}
