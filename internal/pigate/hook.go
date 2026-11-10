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
func Hook(in io.Reader, configDir, sessionID string, now time.Time) string {
	d := Decision{Decision: Ask, Reason: "Gate Inbox could not read the call"}
	raw, err := io.ReadAll(io.LimitReader(in, 1<<20))
	var req Request
	if err == nil && len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &req) == nil && req.ToolName != "" {
		home, _ := os.UserHomeDir()
		d = Decide(Env{
			ConfigDir: configDir, SessionID: sessionID,
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
func mergeGate(configDir, command, cwd string) (string, bool) {
	cfg, err := config.LoadDir(configDir)
	if err != nil || !cfg.MergeGate.Enabled() {
		return "", false
	}
	m, ok := mergegate.Parse(command, cwd)
	if !ok {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if mergegate.Check(ctx, cfg.MergeGate, m) != nil {
		return "", false
	}
	return fmt.Sprintf("Gate Inbox merge gate: the standing merge authority covers a squash merge of #%d", m.Number), true
}
