package agentnudge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Paths are where the hook keeps its state.
type Paths struct {
	// Dir holds every session's state: <session>.steered lists the keys of
	// calls already denied once, one per line, and <session>-<key>.md is a
	// long prompt handed to create_session as prompt_file.
	Dir       string
	SessionID string
	// Home is the user's home, where their own agent definitions live.
	Home string
}

// retention is how long the hook keeps a session's state. Nothing removes
// it when the session ends, so each deny clears what has outlived it; a
// brief only has to last until create_session reads it, and a resent call
// comes within the same turn or the next.
const retention = 7 * 24 * time.Hour

// Hook decides one PreToolUse payload and returns the hook output: a deny
// with a create_session call for a first write-capable Agent call, nothing
// for everything else. Any failure returns nothing, which lets the call run.
func Hook(payload []byte, paths Paths, now time.Time) (Verdict, string) {
	var in Input
	if err := json.Unmarshal(payload, &in); err != nil || Classify(in, paths.Home) == Allow {
		return Allow, ""
	}
	if paths.Dir == "" || paths.SessionID == "" || strings.ContainsAny(paths.SessionID, `/\.`) {
		return Allow, ""
	}
	key := Key(in)
	steered := filepath.Join(paths.Dir, paths.SessionID+".steered")
	if raw, err := os.ReadFile(steered); err == nil && slices.Contains(strings.Fields(string(raw)), key) {
		return Retry, ""
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return Allow, ""
	}
	prune(paths.Dir, now.Add(-retention))
	args, err := CreateArgs(in, filepath.Join(paths.Dir, paths.SessionID+"-"+key+".md"))
	if err != nil {
		return Allow, ""
	}
	out := Output(Reason(in, args))
	if out == "" || remember(steered, key) != nil {
		return Allow, ""
	}
	return Steer, out
}

// remember records key before the deny goes out, so a deny is never
// repeated for the same call even if the next one races this write.
func remember(path, key string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(key + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// prune removes the state files last written before cutoff.
func prune(dir string, cutoff time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && !entry.IsDir() && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// StateDir is where the hook keeps its state under the config directory.
func StateDir(configDir string) string {
	return filepath.Join(configDir, "agent-nudge")
}
