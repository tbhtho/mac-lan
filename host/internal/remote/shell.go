package remote

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
)

// RunShell is a line-based terminal for the other computer, used from a normal
// terminal window. With args it runs that one command and returns its exit
// code; otherwise it reads commands until "exit". It is not a full terminal
// emulator: full-screen programs and password prompts are not supported.
func RunShell(in io.Reader, out io.Writer, dir, ticket string, args []string) int {
	client, err := Dial(dir, ticket)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	defer client.Close()
	name := "the other computer"
	if reply, err := client.Call(map[string]any{"op": "devices"}); err != nil {
		fmt.Fprintln(out, err)
		return 1
	} else if devices, ok := reply["devices"].([]any); ok {
		for _, d := range devices {
			if device, ok := d.(map[string]any); ok && device["this_device"] != true {
				if text, _ := device["name"].(string); text != "" {
					name = text
				}
				if note, _ := device["note"].(string); note != "" {
					fmt.Fprintln(out, note)
				}
			}
		}
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	// run starts a job-producing request and prints its output as it arrives.
	run := func(request map[string]any) int {
		reply, err := client.Call(request)
		if err != nil {
			fmt.Fprintln(out, err)
			return 1
		}
		job, _ := reply["job"].(map[string]any)
		id, _ := job["execution_id"].(string)
		var since float64
		noted := ""
		for {
			if text, _ := job["output"].(string); text != "" {
				fmt.Fprint(out, text)
			}
			since, _ = job["next_offset"].(float64)
			if note, _ := job["note"].(string); note != "" && note != noted {
				noted = note
				fmt.Fprintln(out, "["+note+"]")
			}
			if done, _ := job["finished"].(bool); done {
				state, _ := job["state"].(string)
				code, hasCode := job["exit_code"].(float64)
				if state != "succeeded" {
					if hasCode {
						fmt.Fprintf(out, "[%s, exit code %d]\n", state, int(code))
					} else {
						fmt.Fprintf(out, "[%s]\n", state)
					}
				}
				if state == "succeeded" {
					return 0
				}
				if hasCode && code != 0 {
					return int(code)
				}
				return 1
			}
			select {
			case <-interrupts:
				_, _ = client.Call(map[string]any{"op": "cancel", "id": id})
			default:
			}
			reply, err = client.Call(map[string]any{"op": "status", "id": id, "since": int64(since), "wait": 1})
			if err != nil {
				fmt.Fprintln(out, err)
				return 1
			}
			job, _ = reply["job"].(map[string]any)
		}
	}
	if len(args) > 0 {
		return run(map[string]any{"op": "exec", "command": strings.Join(args, " ")})
	}
	cwd := ""
	if reply, err := client.Call(map[string]any{"op": "resolve", "path": "", "cwd": ""}); err == nil {
		cwd, _ = reply["path"].(string)
	} else {
		fmt.Fprintln(out, err)
	}
	fmt.Fprintln(out, "mac-lan remote shell on "+name+". Each line runs there as one command. Type :help for file transfer, exit to leave, Ctrl+C to stop a running command.")
	reader := bufio.NewReader(in)
	for {
		fmt.Fprintf(out, "%s:%s> ", name, cwd)
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		switch fields := strings.Fields(line); {
		case line == "":
		case line == "exit" || line == "quit":
			return 0
		case line == ":help":
			fmt.Fprintln(out, "cd <folder>                 change the folder commands run in\n:push <local> <remote>      copy a file or folder to "+name+"\n:pull <remote> <local>      copy a file or folder from "+name+"\n:push! / :pull!             the same, replacing existing files (asks in mac-lan)\nexit                        leave\nPaths with spaces are not supported by :push and :pull; use the Terminal window or MCP tools for those.")
		case fields[0] == "cd":
			target := strings.TrimSpace(strings.TrimPrefix(line, "cd"))
			target = strings.Trim(target, `"'`)
			if target == "" {
				target = "~"
			}
			if reply, err := client.Call(map[string]any{"op": "resolve", "path": target, "cwd": cwd}); err != nil {
				fmt.Fprintln(out, err)
			} else {
				cwd, _ = reply["path"].(string)
			}
		case (fields[0] == ":push" || fields[0] == ":push!") && len(fields) == 3:
			run(map[string]any{"op": "transfer", "direction": "push", "local": fields[1], "remote": fields[2], "overwrite": fields[0] == ":push!"})
		case (fields[0] == ":pull" || fields[0] == ":pull!") && len(fields) == 3:
			run(map[string]any{"op": "transfer", "direction": "pull", "remote": fields[1], "local": fields[2], "overwrite": fields[0] == ":pull!"})
		case strings.HasPrefix(fields[0], ":"):
			fmt.Fprintln(out, "Unknown command. Type :help.")
		default:
			run(map[string]any{"op": "exec", "command": line, "cwd": cwd})
		}
		if err != nil {
			return 0
		}
	}
}
