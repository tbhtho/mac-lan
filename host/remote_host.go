package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tbhtho/MAC-DOW/host/internal/remote"
	"github.com/tbhtho/MAC-DOW/host/internal/updater"
)

// Remote commands reuse the session listener: the paired Windows PC opens a
// second WebSocket at /remote with the same pinned certificate and the current
// pairing's bearer token, exactly like file transfer and the clipboard. Nothing
// runs on this Mac until its user switches remote commands on.

// serveRemote authorizes and serves the remote-command link.
func (s *server) serveRemote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !s.transfers.authorized(r, s.peerAllowed) {
		transferError(w, http.StatusForbidden, "Pair with this Mac before using remote commands.")
		return
	}
	s.remote.Accept(w, r)
}

func (s *server) remoteBusy() bool { return s.remote != nil && s.remote.Busy() }

func computerName() string {
	if out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" && len(name) <= 128 {
			return name
		}
	}
	if name, err := os.Hostname(); err == nil && name != "" {
		return name
	}
	return "Mac"
}

// remoteHost is the Mac's remote-command node with its local endpoint.
type remoteHost struct {
	dir    string
	node   *remote.Node
	server *remote.Server
}

// startRemoteHost creates the node. It returns nil when the private folder is
// unavailable; pairing, video and transfers work without it.
func startRemoteHost() *remoteHost {
	dir, err := remote.StateDir()
	if err != nil {
		return nil
	}
	h := &remoteHost{dir: dir, node: remote.New(dir, computerName(), updater.BuildConfig().Version)}
	h.server = remote.NewServer(h.node)
	// Approval prompts for actions started on this Mac (its shell or an agent)
	// use the same native dialog as pairing in Terminal mode.
	h.node.SetConfirm(remoteConsent)
	_ = h.server.Listen(dir)
	return h
}

const remoteConsentScript = `on run argv
 if (count of argv) > 1 then
  set answer to display dialog (item 1 of argv) with title "mac-lan" buttons {"Deny", "Always Allow", "Allow"} default button "Deny" cancel button "Deny" giving up after 50
 else
  set answer to display dialog (item 1 of argv) with title "mac-lan" buttons {"Deny", "Allow"} default button "Deny" cancel button "Deny" giving up after 50
 end if
 if gave up of answer then return "Deny"
 return button returned of answer
end run`

// remoteConsent asks the person at this Mac to approve an action started here.
// The dialog closes by itself when the request is withdrawn.
func remoteConsent(r remote.ConfirmRequest) remote.Answer {
	who := "You"
	if r.Origin == "agent" {
		who = "A local tool or AI agent"
	}
	text := fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s asked for this through mac-lan.", r.Title, clip(r.Detail, 600), r.Reason, who)
	args := []string{"-e", remoteConsentScript, "--", text}
	if r.Remember != "" {
		args[3] = text + "\n\nAlways Allow: " + r.Remember + "."
		args = append(args, "remember")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	if r.Withdrawn != nil {
		go func() {
			select {
			case <-r.Withdrawn:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", args...).Output()
	if err != nil || len(out) > 32 {
		return remote.Deny
	}
	switch strings.TrimSpace(string(out)) {
	case "Allow":
		return remote.AllowOnce
	case "Always Allow":
		if r.Remember != "" {
			return remote.AllowAlways
		}
	}
	return remote.Deny
}

const remoteInputScript = `on run argv
 set answer to display dialog (item 1 of argv) with title "mac-lan" default answer "" buttons {"Cancel", "Add"} default button "Add" cancel button "Cancel" giving up after 110
 if gave up of answer then return ""
 return text returned of answer
end run`

// folders shows the folders on the PC in which files may be replaced without asking.
func (h *remoteHost) folders(emit func(string)) {
	const add = "Add a folder…"
	trusted := h.node.Settings().Trusted
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	args := append([]string{"-e", remoteMenuScript, "--",
		"Folders on your Windows PC where files may be replaced without asking each time. Choose a folder to remove it from the list.", add}, trusted...)
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", args...).Output()
	if err != nil {
		return
	}
	picked := strings.TrimSpace(string(out))
	switch {
	case picked == "":
	case picked == add:
		typed, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", remoteInputScript, "--",
			"Folder on your Windows PC, for example C:\\Projects\\app or ~\\source\\app. Files in it and in its subfolders can then be replaced from this Mac without asking.").Output()
		if folder := strings.TrimSpace(string(typed)); err == nil && folder != "" {
			if err := h.node.Trust(folder); err != nil {
				remoteNote("That folder was not added: " + err.Error() + ".")
				return
			}
			emit("Files in " + folder + " on the PC can now be replaced without asking.")
		}
	default:
		if h.node.Untrust(picked) == nil {
			emit("Replacing files in " + picked + " on the PC asks again.")
		}
	}
}

func clip(text string, limit int) string {
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}

func (h *remoteHost) close() {
	if h == nil {
		return
	}
	h.server.Close(h.dir)
	h.node.Close()
}

const remoteMenuScript = `on run argv
 set choices to {}
 repeat with i from 2 to count of argv
  set end of choices to item i of argv
 end repeat
 set picked to choose from list choices with title "mac-lan" with prompt (item 1 of argv) OK button name "Choose" cancel button name "Close"
 if picked is false then return ""
 return item 1 of picked
end run`

const remoteNoteScript = `on run argv
 display dialog (item 1 of argv) with title "mac-lan" buttons {"OK"} default button "OK" giving up after 60
end run`

func remoteNote(text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "/usr/bin/osascript", "-e", remoteNoteScript, "--", text).Run()
}

func onOff(on bool) string {
	if on {
		return "On"
	}
	return "Off"
}

// menu shows the Remote Commands choices from the Mac menu-bar item.
func (h *remoteHost) menu(emit func(string)) {
	if h == nil {
		emit("Remote commands are unavailable: mac-lan's private settings folder cannot be used.")
		return
	}
	settings := h.node.Settings()
	const terminal, setup = "Open Remote Terminal on Windows", "Copy setup command for AI agents (MCP)"
	folders := fmt.Sprintf("Folders on the PC that can be changed without asking (%d)…", len(settings.Trusted))
	accept := "Let the paired PC run commands on this Mac: " + onOff(settings.Accept)
	agents := "Let local tools and AI agents use the link: " + onOff(settings.Agents)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", remoteMenuScript, "--",
		"Remote commands between this Mac and your Windows PC. Both switches are off until you turn them on.", terminal, accept, agents, folders, setup).Output()
	if err != nil {
		return
	}
	switch strings.TrimSpace(string(out)) {
	case terminal:
		if err := h.openTerminal(); err != nil {
			remoteNote("Could not open the remote terminal: " + err.Error())
		}
	case folders:
		h.folders(emit)
	case accept:
		if !settings.Accept && !nativeConsent("Let the paired Windows PC run commands on this Mac?\n\nThe PC you approved can then run shell commands, read or write files and take pictures of this Mac's screen with your account's rights, from its mac-lan Terminal window or from AI agents you allowed on that PC. Destructive commands are confirmed on the PC first. Every request is recorded in mac-lan's remote-audit.log.\n\nTurn this off again from the same menu.") {
			return
		}
		settings.Accept = !settings.Accept
		h.save(settings, emit, "Commands from the paired PC are now "+strings.ToLower(onOff(settings.Accept))+".")
	case agents:
		if !settings.Agents && !nativeConsent("Let local tools and AI agents on this Mac use the link?\n\nPrograms running as you on this Mac, such as an AI coding agent connected through mac-lan's MCP server, can then run commands, read and change files and take pictures of the screen on the paired Windows PC if that PC allows commands. Destructive actions still ask you here first.") {
			return
		}
		settings.Agents = !settings.Agents
		h.save(settings, emit, "Access for local tools and AI agents is now "+strings.ToLower(onOff(settings.Agents))+".")
	case setup:
		executable, err := os.Executable()
		if err != nil {
			return
		}
		command := "claude mcp add mac-lan -- \"" + executable + "\" --mcp"
		copyCommand := exec.Command("/usr/bin/pbcopy")
		copyCommand.Stdin = strings.NewReader(command)
		_ = copyCommand.Run()
		remoteNote("Copied to the clipboard:\n\n" + command + "\n\nThat line registers mac-lan with Claude Code. Other agents (Codex and similar) use the same program and argument: \"" + executable + "\" with --mcp, as a stdio MCP server.\n\nAlso turn on \"Let local tools and AI agents use the link\".")
	}
}

func (h *remoteHost) save(settings remote.Settings, emit func(string), done string) {
	if err := h.node.SetSettings(settings); err != nil {
		remoteNote("The setting could not be saved.")
		return
	}
	emit(done)
}

// openTerminal opens Terminal with a shell on the paired PC. The script holds a
// single-use ticket that expires after a minute and is readable only by this user.
func (h *remoteHost) openTerminal() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	script := filepath.Join(h.dir, "remote-terminal.command")
	body := "#!/bin/sh\nclear\nexec '" + strings.ReplaceAll(executable, "'", `'\''`) + "' --remote-shell --ticket " + h.server.Ticket() + "\n"
	_ = os.Remove(script)
	if err = os.WriteFile(script, []byte(body), 0o700); err != nil {
		return err
	}
	return exec.Command("/usr/bin/open", script).Run()
}

// remoteCommandLine handles the modes that talk to a running mac-lan instead of
// starting one: the MCP server, the shell, and the two switches for Macs run
// without the menu-bar app. It reports whether it handled the arguments.
func remoteCommandLine(args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	dir, err := remote.StateDir()
	fail := func() (int, bool) {
		fmt.Fprintln(os.Stderr, "mac-lan's private settings folder is unavailable.")
		return 1, true
	}
	switch args[0] {
	case "--mcp":
		if err != nil {
			return fail()
		}
		if err = remote.ServeMCP(os.Stdin, os.Stdout, dir, updater.BuildConfig().Version); err != nil {
			return 1, true
		}
		return 0, true
	case "--remote-shell":
		if err != nil {
			return fail()
		}
		ticket, rest := "", args[1:]
		if len(rest) >= 2 && rest[0] == "--ticket" {
			ticket, rest = rest[1], rest[2:]
		}
		return remote.RunShell(os.Stdin, os.Stdout, dir, ticket, rest), true
	case "--remote-commands", "--remote-agents":
		if err != nil {
			return fail()
		}
		if len(args) != 2 || args[1] != "on" && args[1] != "off" {
			fmt.Fprintln(os.Stderr, "Use: "+args[0]+" on|off")
			return 2, true
		}
		node := remote.New(dir, "", "")
		defer node.Close()
		settings := node.Settings()
		if args[0] == "--remote-commands" {
			settings.Accept = args[1] == "on"
		} else {
			settings.Agents = args[1] == "on"
		}
		if err = node.SetSettings(settings); err != nil {
			fmt.Fprintln(os.Stderr, "The setting could not be saved.")
			return 1, true
		}
		fmt.Println("Saved. It applies the next time mac-lan starts sharing.")
		return 0, true
	}
	return 0, false
}
