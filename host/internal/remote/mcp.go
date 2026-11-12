package remote

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// ServeMCP runs a Model Context Protocol server on stdio (one JSON-RPC message
// per line). It is a thin client of mac-lan running on this computer: every
// tool call goes through mac-lan's local endpoint, so the app's switches,
// approval prompts and audit log apply to agents exactly as to people.

const mcpInstructions = `mac-lan links a Windows PC and a Mac on the same network. These tools act on the OTHER computer (the one this agent is not running on), so you can build, test and edit a project there while working here.

Start with list_devices: it names the other computer, its operating system, user, home folder and default shell, and says whether it is connected and accepts commands.

Commands: execute_command runs one shell command there and returns its output and exit code if it finishes within wait_seconds; a longer one keeps running and is read with get_execution_status (pass next_offset as since_offset). Every command starts in a fresh login shell, so use cwd instead of a separate cd, and chain steps with &&. The shell is zsh on a Mac and cmd.exe on Windows (pass shell "powershell" for PowerShell syntax). Commands cannot prompt: no password prompts, editors or full-screen programs. Very long output is shortened in the middle; redirect it to a file and read that, or filter it.

Files: read_file, write_file and edit_file work on text files there directly, without shell quoting. list_directory lists one folder. transfer_file copies files or whole folders in either direction (use it for binaries and for many files). A typical loop is edit_file, then execute_command to build and test.

Screen: take_screenshot returns a picture of the other computer's main display, for checking what an app looks like. These tools cannot click or type in apps.

Approval: commands that delete, need administrator rights, change system settings, end programs or discard Git work, replacing an existing file, and every deploy_update are shown to the user in a mac-lan prompt and run only if they approve. State "awaiting-confirmation" means the prompt is open; "denied" means they declined or did not answer - do not retry or rephrase to get around it, tell the user instead. The user can choose "always" for a project folder, after which edits in it are not asked about. If a tool says remote commands or agent access are switched off, tell the user; there is no other route.`

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func schema(required []string, properties map[string]any) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func text(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func whole(description string) map[string]any {
	return map[string]any{"type": "integer", "minimum": 0, "description": description}
}

var deviceProperty = text(`Optional. The other computer's id from list_devices ("mac" or "windows"). Leave out to use the connected one.`)

func flag(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

var mcpTools = []mcpTool{
	{"list_devices", "List this computer and the paired computer: name, operating system, user, home folder, default shell, whether it is connected and whether it accepts commands. Call this first.", schema(nil, map[string]any{})},
	{"execute_command", "Run a shell command on the other computer and return its output, exit code and state. Waits up to wait_seconds for it to finish; a longer command keeps running and is read with get_execution_status. Each command starts in a fresh shell: use cwd, not cd. Destructive or privileged commands need the user's approval in a mac-lan prompt first.",
		schema([]string{"command"}, map[string]any{
			"command":         text("The command line. Runs in zsh on a Mac and cmd.exe on Windows unless shell is given."),
			"cwd":             text("Working folder on the other computer. ~ is its home folder, which is also the default."),
			"shell":           text("Optional: zsh, bash or sh on a Mac; cmd, powershell or pwsh on Windows."),
			"timeout_seconds": whole("Stop the command after this long. Default 1800, maximum 43200."),
			"wait_seconds":    whole("How long this call waits for the command to finish. Default 30, maximum 120."),
			"interactive":     flag("Keep standard input open so send_input can write to the command (a REPL, a yes/no question). Default false."),
			"device":          deviceProperty})},
	{"get_execution_status", "Get the state and output of a command, transfer or deployment started earlier. Pass the next_offset from the previous answer as since_offset to receive only new output.",
		schema([]string{"execution_id"}, map[string]any{
			"execution_id": text("The execution_id returned when it was started."),
			"since_offset": whole("Return output from this offset. Default 0 (all kept output)."),
			"wait_seconds": whole("Wait up to this long for new output or the end. Default 0, maximum 120.")})},
	{"cancel_execution", "Stop a running command, transfer or deployment, or withdraw one that is waiting for approval.",
		schema([]string{"execution_id"}, map[string]any{"execution_id": text("The execution_id to stop.")})},
	{"send_input", "Write text to the standard input of a running command that was started with interactive true. Include the line ending (\\n) yourself.",
		schema([]string{"execution_id", "text"}, map[string]any{
			"execution_id": text("The execution_id of the running command."),
			"text":         text("The text to write, for example \"y\\n\".")})},
	{"read_file", "Read a text file on the other computer. The answer starts with one bracketed line naming the file and the lines returned; the file's text follows exactly as stored. Files up to 8 MB; for larger or binary files use a command or transfer_file.",
		schema([]string{"path"}, map[string]any{
			"path":       text("Path on the other computer. ~ is its home folder; a relative path is relative to it."),
			"start_line": whole("First line to return, counting from 1. Default 1."),
			"max_lines":  whole("How many lines to return. Default 2000."),
			"device":     deviceProperty})},
	{"write_file", "Create a text file on the other computer with the given content, creating missing folders. An existing file is not replaced unless overwrite is true, which asks the user unless the folder is one they trust. To change part of a file, use edit_file.",
		schema([]string{"path", "content"}, map[string]any{
			"path":      text("Path of the file on the other computer."),
			"content":   text("The whole content of the file."),
			"overwrite": flag("Replace the file if it exists. Default false."),
			"device":    deviceProperty})},
	{"edit_file", "Change a text file on the other computer by replacing old_text with new_text. old_text must match the file exactly (read_file it first) and occur once, unless replace_all is true. The file keeps its permissions. Asks the user unless the folder is one they trust.",
		schema([]string{"path", "old_text", "new_text"}, map[string]any{
			"path":        text("Path of the file on the other computer."),
			"old_text":    text("The exact text to replace, with enough surrounding lines to be unique."),
			"new_text":    text("The text that takes its place. Empty deletes old_text."),
			"replace_all": flag("Replace every occurrence. Default false."),
			"device":      deviceProperty})},
	{"list_directory", "List the files and folders in one folder on the other computer (not its subfolders), with sizes and modification times.",
		schema(nil, map[string]any{
			"path":   text("Folder on the other computer. Default: its home folder."),
			"device": deviceProperty})},
	{"take_screenshot", "Take a picture of the other computer's main display and return it as an image, to see what is on screen there (an app's window, a dialog, a rendering result).",
		schema(nil, map[string]any{
			"max_width": whole("Largest width in pixels; a wider screen is scaled down. Default 1600, maximum 3840."),
			"device":    deviceProperty})},
	{"transfer_file", "Copy a file or a whole folder between the two computers. push sends from this computer to the other; pull fetches from the other to this one. A folder's contents are copied into the destination folder. Existing files are never replaced unless overwrite is true, which needs the user's approval. Every file is verified with SHA-256.",
		schema([]string{"direction", "local_path", "remote_path"}, map[string]any{
			"direction":    map[string]any{"type": "string", "enum": []string{"push", "pull"}, "description": "push: this computer to the other. pull: the other computer to this one."},
			"local_path":   text("Path on this computer (the one the agent runs on)."),
			"remote_path":  text("Path on the other computer. ~ is its home folder."),
			"overwrite":    flag("Replace files that already exist at the destination. Needs the user's approval."),
			"wait_seconds": whole("How long this call waits for the copy to finish. Default 60, maximum 120."),
			"device":       deviceProperty})},
	{"deploy_update", "Push an application or project update to the other computer and optionally run a command there afterwards (install, build, restart). Replaces existing files at the destination. Always needs the user's approval in a mac-lan prompt.",
		schema([]string{"source_path", "destination_path"}, map[string]any{
			"source_path":      text("File or folder on this computer."),
			"destination_path": text("Where it goes on the other computer. A folder's contents are copied into this folder."),
			"command":          text("Optional command to run on the other computer after the copy, in the destination folder."),
			"shell":            text("Optional shell for the command."),
			"timeout_seconds":  whole("Stop the command after this long. Default 1800."),
			"wait_seconds":     whole("How long this call waits for completion. Default 60, maximum 120."),
			"device":           deviceProperty})},
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpServer struct {
	dir     string
	version string
}

// call sends one request to mac-lan over its own short-lived connection, so a
// long wait in one tool call never delays another (a cancel, for example).
func (m *mcpServer) call(v map[string]any) (map[string]any, error) {
	client, err := Dial(m.dir, "")
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Call(v)
}

func intArg(args map[string]any, name string, fallback, max int) int {
	value, ok := args[name].(float64)
	if !ok || value < 0 {
		return fallback
	}
	if int(value) > max {
		return max
	}
	return int(value)
}
func stringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return value
}

// Output handed to a model is kept to a size it can read: the start and the
// end of long output, which is where commands say what they did and how it ended.
const (
	mcpOutputHead = 12 << 10
	mcpOutputTail = 48 << 10
	mcpFileBytes  = 256 << 10
)

func shorten(output string) string {
	if len(output) <= mcpOutputHead+mcpOutputTail {
		return output
	}
	head, tail := mcpOutputHead, len(output)-mcpOutputTail
	for head > 0 && !utf8.RuneStart(output[head]) {
		head--
	}
	for tail < len(output) && !utf8.RuneStart(output[tail]) {
		tail++
	}
	return output[:head] + fmt.Sprintf("\n[... %d bytes of output left out here. Redirect long output to a file and use read_file, or filter it ...]\n", tail-head) + output[tail:]
}

// jobText describes a command, transfer or deployment for a model: a few
// labelled lines, then the output as plain text.
func jobText(value any) string {
	job, _ := value.(map[string]any)
	if job == nil {
		return "mac-lan sent no result"
	}
	word := func(name string) string { s, _ := job[name].(string); return s }
	var b strings.Builder
	state := word("state")
	b.WriteString("state: " + state)
	if code, ok := job["exit_code"].(float64); ok {
		b.WriteString(", exit code " + strconv.Itoa(int(code)))
	}
	b.WriteString("\n")
	finished, _ := job["finished"].(bool)
	if !finished {
		b.WriteString("execution_id: " + word("execution_id") + "\n")
		if next, ok := job["next_offset"].(float64); ok {
			b.WriteString("next_offset: " + strconv.FormatInt(int64(next), 10) + "\n")
		}
		if total, _ := job["bytes_total"].(float64); total > 0 {
			done, _ := job["bytes_done"].(float64)
			b.WriteString("copied: " + sizeText(int64(done)) + " of " + sizeText(int64(total)) + "\n")
		}
		if state == "running" || state == "starting" {
			b.WriteString("Still running. Call get_execution_status with this execution_id and since_offset set to next_offset.\n")
		}
	}
	if note := word("note"); note != "" {
		b.WriteString("note: " + note + "\n")
	}
	if dropped, _ := job["output_truncated"].(bool); dropped {
		b.WriteString("note: earlier output was dropped; only the most recent 1 MB is kept.\n")
	}
	output := word("output")
	if output == "" {
		if finished {
			b.WriteString("(no output)\n")
		}
		return b.String()
	}
	b.WriteString("--- output ---\n" + shorten(output))
	return b.String()
}

func words(s string) []map[string]any { return []map[string]any{{"type": "text", "text": s}} }

func (m *mcpServer) tool(name string, args map[string]any) ([]map[string]any, error) {
	job := func(request map[string]any) ([]map[string]any, error) {
		reply, err := m.call(request)
		if err != nil {
			return nil, err
		}
		return words(jobText(reply["job"])), nil
	}
	switch name {
	case "list_devices":
		reply, err := m.call(map[string]any{"op": "devices"})
		if err != nil {
			return nil, err
		}
		data, _ := json.MarshalIndent(map[string]any{"devices": reply["devices"]}, "", " ")
		return words(string(data)), nil
	case "execute_command":
		interactive, _ := args["interactive"].(bool)
		return job(map[string]any{"op": "exec", "device": stringArg(args, "device"), "command": stringArg(args, "command"), "cwd": stringArg(args, "cwd"), "shell": stringArg(args, "shell"),
			"timeout": intArg(args, "timeout_seconds", 0, 43200), "wait": intArg(args, "wait_seconds", 30, 120), "interactive": interactive})
	case "get_execution_status":
		return job(map[string]any{"op": "status", "id": stringArg(args, "execution_id"), "since": intArg(args, "since_offset", 0, 1<<53), "wait": intArg(args, "wait_seconds", 0, 120)})
	case "cancel_execution":
		id := stringArg(args, "execution_id")
		if _, err := m.call(map[string]any{"op": "cancel", "id": id}); err != nil {
			return nil, err
		}
		return job(map[string]any{"op": "wait", "id": id, "wait": 5})
	case "send_input":
		if _, err := m.call(map[string]any{"op": "stdin", "id": stringArg(args, "execution_id"), "data": stringArg(args, "text")}); err != nil {
			return nil, err
		}
		return words("Sent. Read the command's reply with get_execution_status."), nil
	case "read_file":
		path := stringArg(args, "path")
		reply, err := m.call(map[string]any{"op": "read", "device": stringArg(args, "device"), "path": path})
		if err != nil {
			return nil, err
		}
		content, _ := reply["content"].(string)
		return words(fileText(path, content, intArg(args, "start_line", 1, 1<<40), intArg(args, "max_lines", 2000, 1<<40))), nil
	case "write_file":
		overwrite, _ := args["overwrite"].(bool)
		return job(map[string]any{"op": "write", "device": stringArg(args, "device"), "path": stringArg(args, "path"), "content": stringArg(args, "content"), "overwrite": overwrite, "wait": 90})
	case "edit_file":
		all, _ := args["replace_all"].(bool)
		return job(map[string]any{"op": "edit", "device": stringArg(args, "device"), "path": stringArg(args, "path"), "old": stringArg(args, "old_text"), "new": stringArg(args, "new_text"), "all": all, "wait": 90})
	case "list_directory":
		path := stringArg(args, "path")
		if path == "" {
			path = "~"
		}
		reply, err := m.call(map[string]any{"op": "dir", "device": stringArg(args, "device"), "path": path})
		if err != nil {
			return nil, err
		}
		return words(folderText(reply)), nil
	case "take_screenshot":
		reply, err := m.call(map[string]any{"op": "shot", "device": stringArg(args, "device"), "width": intArg(args, "max_width", 0, 3840)})
		if err != nil {
			return nil, err
		}
		width, _ := reply["width"].(float64)
		height, _ := reply["height"].(float64)
		return []map[string]any{{"type": "image", "data": reply["image"], "mimeType": "image/jpeg"},
			{"type": "text", "text": fmt.Sprintf("The other computer's main display, %d x %d pixels.", int(width), int(height))}}, nil
	case "transfer_file":
		overwrite, _ := args["overwrite"].(bool)
		return job(map[string]any{"op": "transfer", "device": stringArg(args, "device"), "direction": stringArg(args, "direction"), "local": stringArg(args, "local_path"), "remote": stringArg(args, "remote_path"),
			"overwrite": overwrite, "wait": intArg(args, "wait_seconds", 60, 120)})
	case "deploy_update":
		return job(map[string]any{"op": "deploy", "device": stringArg(args, "device"), "source": stringArg(args, "source_path"), "destination": stringArg(args, "destination_path"), "command": stringArg(args, "command"),
			"shell": stringArg(args, "shell"), "timeout": intArg(args, "timeout_seconds", 0, 43200), "wait": intArg(args, "wait_seconds", 60, 120)})
	}
	return nil, errors.New("unknown tool: " + name)
}

// fileText returns the requested lines of a file under one bracketed heading.
func fileText(path, content string, start, count int) string {
	if content == "" {
		return "[" + path + ": empty file]\n"
	}
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	if start < 1 {
		start = 1
	}
	if start > total {
		return fmt.Sprintf("[%s: the file has %d lines; start_line %d is past the end]\n", path, total, start)
	}
	if count < 1 {
		count = 2000
	}
	end := start - 1
	size := 0
	for end < total && end-(start-1) < count && (size+len(lines[end]) <= mcpFileBytes || end == start-1) {
		size += len(lines[end])
		end++
	}
	heading := fmt.Sprintf("[%s: lines %d-%d of %d", path, start, end, total)
	if end < total {
		heading += fmt.Sprintf("; continue with start_line %d", end+1)
	}
	body := strings.Join(lines[start-1:end], "")
	if len(body) > 2*mcpFileBytes { // one enormous line
		cut := 2 * mcpFileBytes
		for cut > 0 && !utf8.RuneStart(body[cut]) {
			cut--
		}
		body = body[:cut]
		heading += "; this line is longer than can be shown and was cut"
	}
	return heading + "]\n" + body
}

func folderText(reply map[string]any) string {
	path, _ := reply["path"].(string)
	entries, _ := reply["entries"].([]any)
	var b strings.Builder
	fmt.Fprintf(&b, "[%s: %d entries", path, len(entries))
	if more, _ := reply["more"].(bool); more {
		b.WriteString("; the folder has more than are shown")
	}
	b.WriteString("]\n")
	for _, value := range entries {
		e, _ := value.(map[string]any)
		name, _ := e["name"].(string)
		modified, _ := e["modified"].(string)
		if folder, _ := e["folder"].(bool); folder {
			fmt.Fprintf(&b, "%s/\t\t%s\n", name, modified)
			continue
		}
		size, _ := e["size"].(float64)
		fmt.Fprintf(&b, "%s\t%s\t%s\n", name, sizeText(int64(size)), modified)
	}
	return b.String()
}

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// ServeMCP serves until in closes. dir is mac-lan's private folder.
func ServeMCP(in io.Reader, out io.Writer, dir, version string) error {
	server := &mcpServer{dir: dir, version: version}
	reader := bufio.NewReaderSize(in, 1<<20)
	encode := func(v any) {
		// Plain JSON without HTML escaping, so < > & in output stay readable.
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		if encoder.Encode(v) == nil {
			_, _ = out.Write(buffer.Bytes())
		}
	}
	var wmu sync.Mutex
	respond := func(id json.RawMessage, result any, code int, message string) {
		answer := map[string]any{"jsonrpc": "2.0", "id": id}
		if code != 0 {
			answer["error"] = map[string]any{"code": code, "message": message}
		} else {
			answer["result"] = result
		}
		wmu.Lock()
		defer wmu.Unlock()
		encode(answer)
	}
	var running sync.WaitGroup
	defer running.Wait()
	for {
		line, err := reader.ReadBytes('\n')
		line = bytes.TrimPrefix(line, []byte{0xEF, 0xBB, 0xBF}) // some launchers prepend a byte-order mark
		if len(bytes.TrimSpace(line)) > 0 {
			var q rpcRequest
			if json.Unmarshal(line, &q) != nil || q.JSONRPC != "2.0" {
				respond(json.RawMessage("null"), nil, -32700, "Parse error")
			} else if len(q.ID) > 0 && string(q.ID) != "null" { // notifications carry no id and get no answer
				switch q.Method {
				case "initialize":
					var params struct {
						ProtocolVersion string `json:"protocolVersion"`
					}
					_ = json.Unmarshal(q.Params, &params)
					chosen := mcpVersions[0]
					for _, v := range mcpVersions {
						if v == params.ProtocolVersion {
							chosen = v
						}
					}
					respond(q.ID, map[string]any{"protocolVersion": chosen, "capabilities": map[string]any{"tools": map[string]any{}},
						"serverInfo": map[string]any{"name": "mac-lan", "title": "mac-lan remote commands", "version": server.version}, "instructions": mcpInstructions}, 0, "")
				case "ping":
					respond(q.ID, map[string]any{}, 0, "")
				case "tools/list":
					respond(q.ID, map[string]any{"tools": mcpTools}, 0, "")
				case "tools/call":
					var params struct {
						Name      string         `json:"name"`
						Arguments map[string]any `json:"arguments"`
					}
					if json.Unmarshal(q.Params, &params) != nil || params.Name == "" {
						respond(q.ID, nil, -32602, "Invalid params")
						break
					}
					// Each call runs on its own goroutine so a long wait does not hold up a cancel.
					running.Add(1)
					go func(id json.RawMessage) {
						defer running.Done()
						content, err := server.tool(params.Name, params.Arguments)
						if err != nil {
							content = words(err.Error())
						}
						respond(id, map[string]any{"content": content, "isError": err != nil}, 0, "")
					}(q.ID)
				default:
					respond(q.ID, nil, -32601, "Method not found")
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
