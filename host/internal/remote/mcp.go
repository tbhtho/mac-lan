package remote

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// ServeMCP runs a Model Context Protocol server on stdio (one JSON-RPC message
// per line). It is a thin client of mac-lan running on this computer: every
// tool call goes through mac-lan's local endpoint, so the app's switches,
// approval prompts and audit log apply to agents exactly as to people.

const mcpInstructions = `mac-lan links a Windows PC and a Mac on the same network. These tools act on the OTHER computer (the one this agent is not running on).

Workflow: call list_devices first. execute_command starts a shell command there and returns its output if it finishes within wait_seconds; otherwise poll get_execution_status with next_offset. Use cwd for Git and build commands, for example execute_command {"command":"git pull && swift build","cwd":"~/Projects/app"}. transfer_file copies files or folders in either direction. deploy_update pushes a file or folder and then runs an install or build command in the destination.

Safety: destructive or privileged commands, replacing existing files, and every deploy_update are shown to the user in the mac-lan window and run only if they approve; state "awaiting-confirmation" means that prompt is open, "denied" means they declined - do not retry or rephrase to avoid the prompt. The other computer's user must have switched remote commands on; if a tool says they are off, tell the user instead of looking for another route. Commands are not interactive: no prompts, editors or full-screen programs.`

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

var mcpTools = []mcpTool{
	{"list_devices", "List this computer and the paired computer: name, platform, whether it is connected and whether it accepts commands.", schema(nil, map[string]any{})},
	{"execute_command", "Run a shell command on the other computer and return its output, exit code and state. Waits up to wait_seconds for it to finish; a longer command keeps running and is read with get_execution_status. Destructive or privileged commands need the user's approval in the mac-lan window first.",
		schema([]string{"command"}, map[string]any{
			"command":         text("The command line. Runs in zsh on a Mac and cmd.exe on Windows unless shell is given."),
			"cwd":             text("Working folder on the other computer. ~ is its home folder, which is also the default."),
			"shell":           text("Optional: zsh, bash or sh on a Mac; cmd, powershell or pwsh on Windows."),
			"timeout_seconds": whole("Stop the command after this long. Default 1800, maximum 43200."),
			"wait_seconds":    whole("How long this call waits for the command to finish. Default 30, maximum 120."),
			"device":          deviceProperty})},
	{"get_execution_status", "Get the state and output of a command, transfer or deployment started earlier. Pass the next_offset from the previous answer as since_offset to receive only new output.",
		schema([]string{"execution_id"}, map[string]any{
			"execution_id": text("The execution_id returned when it was started."),
			"since_offset": whole("Return output from this offset. Default 0 (all kept output)."),
			"wait_seconds": whole("Wait up to this long for new output or the end. Default 0, maximum 120.")})},
	{"cancel_execution", "Stop a running command, transfer or deployment, or withdraw one that is waiting for approval.",
		schema([]string{"execution_id"}, map[string]any{"execution_id": text("The execution_id to stop.")})},
	{"transfer_file", "Copy a file or a whole folder between the two computers. push sends from this computer to the other; pull fetches from the other to this one. A folder's contents are copied into the destination folder. Existing files are never replaced unless overwrite is true, which needs the user's approval. Every file is verified with SHA-256.",
		schema([]string{"direction", "local_path", "remote_path"}, map[string]any{
			"direction":    map[string]any{"type": "string", "enum": []string{"push", "pull"}, "description": "push: this computer to the other. pull: the other computer to this one."},
			"local_path":   text("Path on this computer (the one the agent runs on)."),
			"remote_path":  text("Path on the other computer. ~ is its home folder."),
			"overwrite":    map[string]any{"type": "boolean", "description": "Replace files that already exist at the destination. Needs the user's approval."},
			"wait_seconds": whole("How long this call waits for the copy to finish. Default 60, maximum 120."),
			"device":       deviceProperty})},
	{"deploy_update", "Push an application or project update to the other computer and optionally run a command there afterwards (install, build, restart). Replaces existing files at the destination. Always needs the user's approval in the mac-lan window.",
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

func (m *mcpServer) tool(name string, args map[string]any) (any, error) {
	switch name {
	case "list_devices":
		reply, err := m.call(map[string]any{"op": "devices"})
		if err != nil {
			return nil, err
		}
		return map[string]any{"devices": reply["devices"]}, nil
	case "execute_command":
		reply, err := m.call(map[string]any{"op": "exec", "device": stringArg(args, "device"), "command": stringArg(args, "command"), "cwd": stringArg(args, "cwd"), "shell": stringArg(args, "shell"),
			"timeout": intArg(args, "timeout_seconds", 0, 43200), "wait": intArg(args, "wait_seconds", 30, 120)})
		if err != nil {
			return nil, err
		}
		return reply["job"], nil
	case "get_execution_status":
		reply, err := m.call(map[string]any{"op": "status", "id": stringArg(args, "execution_id"), "since": intArg(args, "since_offset", 0, 1<<53), "wait": intArg(args, "wait_seconds", 0, 120)})
		if err != nil {
			return nil, err
		}
		return reply["job"], nil
	case "cancel_execution":
		id := stringArg(args, "execution_id")
		if _, err := m.call(map[string]any{"op": "cancel", "id": id}); err != nil {
			return nil, err
		}
		reply, err := m.call(map[string]any{"op": "wait", "id": id, "wait": 5})
		if err != nil {
			return nil, err
		}
		return reply["job"], nil
	case "transfer_file":
		overwrite, _ := args["overwrite"].(bool)
		reply, err := m.call(map[string]any{"op": "transfer", "device": stringArg(args, "device"), "direction": stringArg(args, "direction"), "local": stringArg(args, "local_path"), "remote": stringArg(args, "remote_path"),
			"overwrite": overwrite, "wait": intArg(args, "wait_seconds", 60, 120)})
		if err != nil {
			return nil, err
		}
		return reply["job"], nil
	case "deploy_update":
		reply, err := m.call(map[string]any{"op": "deploy", "device": stringArg(args, "device"), "source": stringArg(args, "source_path"), "destination": stringArg(args, "destination_path"), "command": stringArg(args, "command"),
			"shell": stringArg(args, "shell"), "timeout": intArg(args, "timeout_seconds", 0, 43200), "wait": intArg(args, "wait_seconds", 60, 120)})
		if err != nil {
			return nil, err
		}
		return reply["job"], nil
	}
	return nil, errors.New("unknown tool: " + name)
}

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// ServeMCP serves until in closes. dir is mac-lan's private folder.
func ServeMCP(in io.Reader, out io.Writer, dir, version string) error {
	server := &mcpServer{dir: dir, version: version}
	reader := bufio.NewReaderSize(in, 1<<20)
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
		_ = json.NewEncoder(out).Encode(answer)
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
						result, err := server.tool(params.Name, params.Arguments)
						body, failed := "", false
						if err != nil {
							body, failed = err.Error(), true
						} else {
							data, _ := json.MarshalIndent(result, "", " ")
							body = string(data)
						}
						respond(id, map[string]any{"content": []map[string]any{{"type": "text", "text": body}}, "isError": failed}, 0, "")
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
