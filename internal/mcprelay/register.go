package mcprelay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// ServerName is the relay's name in the user's Claude Code config. It is the
// name a launched session's own server has, so a launch's --mcp-config entry
// shadows it there and the relay never runs beside it, and an adopted
// session's tools are named exactly as a launched one's.
const ServerName = "gate-inbox"

// relayTag opens the relay's command, so its entry is told from one an
// operator wrote under the same name.
const relayTag = ": gate-inbox-relay"

// fallback answers, with sh builtins only, for a relay whose binary is gone:
// it lists no tools and stays up, because Claude Code shows a server whose
// command is missing, or that exits, as failed in every session.
const fallback = `while IFS= read -r l; do ` +
	`case "$l" in *'"id"'*) ;; *) continue;; esac; ` +
	`i=${l#*\"id\":}; i=${i%%[,\}]*}; ` +
	`case "$l" in ` +
	`*'"method":"initialize"'*) r='{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"gate-inbox","version":"absent"}}';; ` +
	`*'"method":"tools/list"'*) r='{"tools":[]}';; ` +
	`*'"method":"ping"'*) r='{}';; ` +
	`*'"method":'*) printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Gate Inbox is not installed"}}\n' "$i"; continue;; ` +
	`*) continue;; ` +
	`esac; ` +
	`printf '{"jsonrpc":"2.0","id":%s,"result":%s}\n' "$i" "$r"; ` +
	`done`

func shellQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
}

// Script is the relay's sh command for the board at configDir.
func Script(configDir, bin string) string {
	return relayTag + " " + shellQuote(configDir) + "; " +
		"b=" + shellQuote(bin) + `; [ -x "$b" ] && GATE_INBOX_HOME=` + shellQuote(configDir) +
		` exec "$b" mcp-relay; ` + fallback
}

// Spec is the relay's entry as `claude mcp add-json` takes it.
func Spec(configDir, bin string) map[string]any {
	return map[string]any{"type": "stdio", "command": "/bin/sh", "args": []any{"-c", Script(configDir, bin)}}
}

// ClaudeStatePath is the file Claude Code keeps user-scope MCP servers in.
func ClaudeStatePath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, ".claude.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude.json"), nil
}

// State is what the user's config holds under ServerName.
type State int

const (
	Absent State = iota
	// Current is this board's relay, exactly as this build writes it.
	Current
	// Stale is a relay this build would write differently.
	Stale
	// OtherBoard is the relay of a board whose home still exists.
	OtherBoard
	// Foreign is an entry Gate Inbox did not write.
	Foreign
)

// Lookup reads the entry under ServerName in Claude Code's state file.
func Lookup(path, configDir, bin string) (State, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Absent, nil
	}
	if err != nil {
		return Absent, err
	}
	var state struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return Absent, fmt.Errorf("%s: %w", path, err)
	}
	entry, ok := state.MCPServers[ServerName]
	if !ok {
		return Absent, nil
	}
	script := relayScript(entry)
	if !strings.HasPrefix(script, relayTag+" ") {
		return Foreign, nil
	}
	if entryMatches(entry, Spec(configDir, bin)) {
		return Current, nil
	}
	if !strings.HasPrefix(script, relayTag+" "+shellQuote(configDir)+";") {
		if other := taggedDir(script); other != "" {
			if _, err := os.Stat(other); err == nil {
				return OtherBoard, nil
			}
		}
	}
	return Stale, nil
}

func relayScript(entry map[string]any) string {
	args, _ := entry["args"].([]any)
	if entry["command"] != "/bin/sh" || len(args) != 2 || args[0] != "-c" {
		return ""
	}
	script, _ := args[1].(string)
	return script
}

// taggedDir is the home a relay script names, for a home quoted without an
// embedded quote, which is every home this package can tell apart.
func taggedDir(script string) string {
	rest := strings.TrimPrefix(script, relayTag+" '")
	dir, _, ok := strings.Cut(rest, "';")
	if !ok || rest == script {
		return ""
	}
	return dir
}

func entryMatches(entry, want map[string]any) bool {
	got := map[string]any{"type": entry["type"], "command": entry["command"], "args": entry["args"]}
	if got["type"] == nil {
		got["type"] = "stdio"
	}
	data, _ := json.Marshal(want)
	var normal map[string]any
	_ = json.Unmarshal(data, &normal)
	return reflect.DeepEqual(got, normal)
}

// Claude runs the claude CLI with args.
type Claude func(args ...string) error

// Register makes the user's config carry this board's relay, through the
// claude CLI so Claude Code's own writer touches its state file. An entry
// Gate Inbox did not write, or another live board's, is left alone.
func Register(claude Claude, path, configDir, bin string) (changed bool, err error) {
	state, err := Lookup(path, configDir, bin)
	if err != nil {
		return false, err
	}
	switch state {
	case Current:
		return false, nil
	case Foreign:
		return false, fmt.Errorf("%s already has an MCP server named %q that Gate Inbox did not write; left it alone", path, ServerName)
	case OtherBoard:
		return false, fmt.Errorf("%s already carries the Gate Inbox relay of another board; left it alone", path)
	case Stale:
		if err := claude("mcp", "remove", "-s", "user", ServerName); err != nil {
			return false, err
		}
	}
	spec, err := json.Marshal(Spec(configDir, bin))
	if err != nil {
		return false, err
	}
	if err := claude("mcp", "add-json", "-s", "user", ServerName, string(spec)); err != nil {
		return false, err
	}
	return true, nil
}

// Unregister removes the relay, this board's or, when all is set, any
// board's. An operator's own entry under the name stays.
func Unregister(claude Claude, path, configDir string, all bool) (changed bool, err error) {
	state, err := Lookup(path, configDir, "")
	if err != nil {
		return false, err
	}
	switch {
	case state == Absent, state == Foreign, state == OtherBoard && !all:
		return false, nil
	}
	if err := claude("mcp", "remove", "-s", "user", ServerName); err != nil {
		return false, err
	}
	return true, nil
}
