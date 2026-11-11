package pigate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/mcpshared"
	"github.com/usestring/gate-inbox/internal/mergegate"
	"github.com/usestring/gate-inbox/internal/parentseal"
	"github.com/usestring/gate-inbox/internal/piauto"
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
			AutoMode:  func(r piauto.Request) piauto.Answer { return askAutoMode(configDir, sessionID, r) },
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

// askAutoMode posts the call to the board's shared loopback server, which
// holds the TypeSafe key and asks the classifier (piauto.Handler). This
// process never reads the key. A server that is not running, or not the
// board's own, or any failure, is an answer with no allow in it.
func askAutoMode(configDir, sessionID string, r piauto.Request) piauto.Answer {
	down := piauto.Answer{Note: "pi auto mode is unavailable: the board's shared server is not answering"}
	url := mcpshared.URL(configDir)
	if url == "" || sessionID == "" || !mcpshared.Healthy(configDir, url) {
		return down
	}
	token, err := parentseal.ExistingCallerToken(configDir, sessionID)
	if err != nil || token == "" {
		return down
	}
	body, err := json.Marshal(r)
	if err != nil {
		return down
	}
	ctx, cancel := context.WithTimeout(context.Background(), piauto.Timeout+2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(url, "/")+piauto.Path, bytes.NewReader(body))
	if err != nil {
		return down
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(mcpshared.CallerHeader, sessionID)
	req.Header.Set(mcpshared.CallerTokenHeader, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return down
	}
	defer resp.Body.Close()
	var a piauto.Answer
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&a) != nil {
		return down
	}
	return a
}
