package codexprobe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The fixtures the probe's codex runs: this test binary, re-executed with
// roleEnv set. "mcp" is a stdio MCP server that lists no tools until the
// trigger file exists, then sends notifications/tools/list_changed; "hook:<Event>"
// is a hook command. Both append one JSON record per event to logEnv's file,
// with the process ancestry, so the test can tell where codex spawned them.
const (
	roleEnv    = "CODEX_PROBE_ROLE"
	logEnv     = "CODEX_PROBE_LOG"
	triggerEnv = "CODEX_PROBE_TRIGGER"

	hookCodeword = "PAPAYA-19"
	toolCodeword = "MANGO-7"
	toolName     = "probe_ping"
)

type record struct {
	T        float64         `json:"t"`
	PID      int             `json:"pid"`
	Ev       string          `json:"ev"`
	Chain    []int           `json:"chain,omitempty"`
	Pane     string          `json:"tmux_pane,omitempty"`
	Cwd      string          `json:"cwd,omitempty"`
	Method   string          `json:"method,omitempty"`
	N        int             `json:"n,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	HookName string          `json:"hook,omitempty"`
	Role     string          `json:"role,omitempty"`
}

var (
	logMu       sync.Mutex
	fixtureRole string
)

func appendRecord(r record) {
	r.T = float64(time.Now().UnixNano()) / 1e9
	r.PID = os.Getpid()
	r.Role = fixtureRole
	b, _ := json.Marshal(r)
	logMu.Lock()
	defer logMu.Unlock()
	f, err := os.OpenFile(os.Getenv(logEnv), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// launchEnv reads name from the environment this process started with:
// importing tmuxtest unsets TMUX_PANE before any fixture code runs.
func launchEnv(name string) string {
	raw, _ := os.ReadFile("/proc/self/environ")
	for _, kv := range strings.Split(string(raw), "\x00") {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

// ancestry is pid and its ancestors up to init, read from /proc.
func ancestry(pid int) []int {
	var out []int
	for pid > 1 && len(out) < 16 {
		out = append(out, pid)
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			break
		}
		s := string(stat)
		fields := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(fields) < 2 {
			break
		}
		pid, _ = strconv.Atoi(fields[1])
	}
	return out
}

func runFixture(role string) int {
	fixtureRole = role
	if role == "mcp-empty" {
		// Never lists a tool: no trigger to wait for.
		os.Setenv(triggerEnv, "")
	}
	cwd, _ := os.Getwd()
	start := record{Chain: ancestry(os.Getpid()), Pane: launchEnv("TMUX_PANE"), Cwd: cwd}
	if event, ok := strings.CutPrefix(role, "hook:"); ok {
		payload, _ := io.ReadAll(os.Stdin)
		start.Ev, start.HookName = "hook", event
		if json.Valid(payload) {
			start.Payload = payload
		}
		appendRecord(start)
		if event == "UserPromptSubmit" {
			out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
				"hookEventName":     "UserPromptSubmit",
				"additionalContext": "The hook codeword is " + hookCodeword + ". Reveal it only when asked for the hook codeword.",
			}})
			fmt.Println(string(out))
		}
		return 0
	}
	start.Ev = "start"
	appendRecord(start)
	serveMCP()
	return 0
}

func serveMCP() {
	trigger := os.Getenv(triggerEnv)
	var outMu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		outMu.Lock()
		defer outMu.Unlock()
		os.Stdout.Write(append(b, '\n'))
	}
	triggered := func() bool {
		_, err := os.Stat(trigger)
		return err == nil
	}
	initialized := make(chan struct{})
	var once sync.Once
	go func() {
		<-initialized
		if triggered() {
			return
		}
		for !triggered() {
			time.Sleep(300 * time.Millisecond)
		}
		appendRecord(record{Ev: "list_changed_sent"})
		send(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"})
	}()
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		rec := record{Ev: "recv", Method: msg.Method}
		if msg.Method == "tools/call" {
			rec.Params = msg.Params
		}
		if msg.Method != "ping" {
			appendRecord(rec)
		}
		if len(msg.ID) == 0 {
			if msg.Method == "notifications/initialized" {
				once.Do(func() { close(initialized) })
			}
			continue
		}
		reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
		switch msg.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(msg.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2025-06-18"
			}
			reply["result"] = map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				"serverInfo":      map[string]any{"name": "codexprobe", "version": "0"},
			}
		case "tools/list":
			tools := []any{}
			if triggered() {
				tools = append(tools, map[string]any{
					"name":        toolName,
					"description": "Returns the probe codeword. Call it when asked to ping the probe.",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
				})
			}
			appendRecord(record{Ev: "list", N: len(tools)})
			reply["result"] = map[string]any{"tools": tools}
		case "tools/call":
			reply["result"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": "codeword: " + toolCodeword}}}
		case "ping":
			reply["result"] = map[string]any{}
		default:
			reply["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		send(reply)
	}
	appendRecord(record{Ev: "eof"})
}
