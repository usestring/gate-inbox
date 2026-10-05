package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/usestring/gate-inbox/internal/parentseal"
)

// KeyDirDecision is the PreToolUse output that refuses an adopted session's
// tool call when the call names the sealing-key directory, and "" for any
// other call.
//
// It stands in for the denials a launch puts on its command line, for a claude
// that started before the board wrote them into the user's settings and so
// never loaded them. It reads what the call names: the paths a file or search
// tool is given, and the text of a shell command. A command that reaches the
// directory without naming it (a cd into the board's home, then a relative
// path) is not caught here; the settings' sandbox denial covers the shell from
// the session's next start.
func (m *Manager) KeyDirDecision(payload []byte) string {
	var call struct {
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
		Cwd       string         `json:"cwd"`
	}
	if json.Unmarshal(payload, &call) != nil || call.ToolInput == nil {
		return ""
	}
	keyDir := parentseal.KeyDir(m.root)
	if !namesKeyDir(keyDir, m.root, call.ToolName, call.ToolInput, call.Cwd) {
		return ""
	}
	out, err := marshalPlain(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":      "PreToolUse",
		"permissionDecision": "deny",
		"permissionDecisionReason": "Gate Inbox keeps every session out of its sealing-key directory (" + keyDir +
			"): those keys would let a session forge the messages a parent sends its children.",
	}})
	if err != nil {
		return ""
	}
	return string(out)
}

// pathFields are the tool inputs that name a file or a directory to search.
var pathFields = []string{"file_path", "notebook_path", "path", "pattern", "glob"}

// A search tool given the board's home, or a directory between it and the
// keys, would read them on its way through, so that counts as naming them too;
// a search of anything wider is left to the settings' denials.
func namesKeyDir(keyDir, boardHome, tool string, input map[string]any, cwd string) bool {
	dirs := []string{filepath.Clean(keyDir)}
	if resolved, err := filepath.EvalSymlinks(keyDir); err == nil && resolved != dirs[0] {
		dirs = append(dirs, resolved)
	}
	spellings := append([]string{}, dirs...)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		for _, dir := range dirs {
			if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
				spellings = append(spellings, "~/"+rel, "$HOME/"+rel, "${HOME}/"+rel)
			}
		}
	}
	mentions := func(text string) bool {
		for _, s := range spellings {
			if strings.Contains(text, s) {
				return true
			}
		}
		return false
	}
	if tool == "Bash" {
		command, _ := input["command"].(string)
		return mentions(command)
	}
	for _, field := range pathFields {
		value, _ := input[field].(string)
		if value == "" {
			continue
		}
		if field == "pattern" || field == "glob" {
			if mentions(value) {
				return true
			}
			continue
		}
		path := value
		if strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[2:])
			}
		}
		if !filepath.IsAbs(path) {
			if cwd == "" {
				continue
			}
			path = filepath.Join(cwd, path)
		}
		candidates := []string{filepath.Clean(path)}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			candidates = append(candidates, resolved)
		}
		for _, c := range candidates {
			for _, dir := range dirs {
				if within(c, dir) {
					return true
				}
				if (tool == "Grep" || tool == "Glob") && within(dir, c) && within(c, filepath.Clean(boardHome)) {
					return true
				}
			}
		}
	}
	return false
}

// within reports whether path is dir or under it.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}
