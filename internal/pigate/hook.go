package pigate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/mergegate"
)

// Hook answers `gate-inbox hook pi PreToolUse`: the call on in, the decision
// as JSON. A payload it cannot read asks, and the extension reads any answer
// it cannot read, or none, the same way.
func Hook(in io.Reader, configDir, sessionID, statusFile string, now time.Time) string {
	d := Decision{Decision: Ask, Reason: "Gate Inbox could not read the call"}
	raw, err := io.ReadAll(io.LimitReader(in, 1<<20))
	var req Request
	if err == nil && len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &req) == nil && req.ToolName != "" {
		home, _ := os.UserHomeDir()
		d = Decide(Env{
			ConfigDir: configDir, SessionID: sessionID, StatusFile: statusFile,
			Home: home, ClaudeHome: convo.ClaudeHome(),
			MergeGate: func(command, cwd string) (string, bool) { return mergeGate(configDir, command, cwd) },
			Now:       now,
		}, req)
	}
	out, err := json.Marshal(d)
	if err != nil {
		return `{"permissionDecision":"ask"}`
	}
	return string(out)
}

// mergeGate is the merge-gate mod's check (merge-gate-check) for a pi call.
// When it does not allow the merge it says why, in merge-gate-check's words,
// so the model learns what would have to change rather than only that the
// call needs approval.
func mergeGate(configDir, command, cwd string) (string, bool) {
	cfg, err := config.LoadDir(configDir)
	if err != nil {
		return "", false
	}
	if !cfg.MergeGate.Enabled() {
		return "merge gate: no standing merge authority is configured", false
	}
	m, ok := mergegate.Parse(command, cwd)
	if !ok {
		return "merge gate: the standing merge authority does not cover that shape of command", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := mergegate.Check(ctx, cfg.MergeGate, m); err != nil {
		return "merge gate: " + err.Error(), false
	}
	return fmt.Sprintf("Gate Inbox merge gate: the standing merge authority covers a squash merge of #%d", m.Number), true
}
