package pigate

import (
	"encoding/json"
	"io"
	"os"
	"slices"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/hooks"
)

// The permission dialog's two records, both beside the session's status
// file: the call the dialog is asking about, while it is up, and the rules a
// person approved there with "don't ask again", for the rest of the session.

// PermissionPending answers `hook pi permission-pending`: the extension is
// about to draw a dialog for the call on in. It keeps the call whole for the
// board to read back, and marks the session waiting before the dialog draws.
func PermissionPending(in io.Reader, statusFile string) {
	if statusFile == "" {
		return
	}
	var p hooks.PiPermission
	if json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&p) != nil || p.Block == "" {
		return
	}
	body, err := json.Marshal(p)
	if err != nil {
		return
	}
	if writeFile(statusFile+hooks.PiPermissionSuffix, body) != nil {
		return
	}
	_ = appendStatus(statusFile, "waiting PermissionAsk")
}

// PermissionResolved answers `hook pi permission-resolved`: the dialog
// closed, however it was answered.
func PermissionResolved(statusFile string) {
	if statusFile != "" {
		_ = os.Remove(statusFile + hooks.PiPermissionSuffix)
	}
}

// GrantSession answers `hook pi grant-session`: the person at the dialog
// chose "don't ask again". What is approved is the rule the pending record
// offered for that same call, never a rule the payload names, so the
// approval is only ever the one the dialog showed.
func GrantSession(in io.Reader, statusFile string) {
	if statusFile == "" {
		return
	}
	var req struct {
		ToolUseID string `json:"tool_use_id"`
	}
	if json.NewDecoder(io.LimitReader(in, 1<<16)).Decode(&req) != nil {
		return
	}
	raw, err := os.ReadFile(statusFile + hooks.PiPermissionSuffix)
	if err != nil {
		return
	}
	var p hooks.PiPermission
	if json.Unmarshal(raw, &p) != nil || p.ToolUseID != req.ToolUseID || grant.CheckRule(p.Suggestion) != nil {
		return
	}
	path := statusFile + hooks.PiApprovalsSuffix
	rules := readApprovals(path)
	if slices.Contains(rules, p.Suggestion) {
		return
	}
	body, err := json.Marshal(append(rules, p.Suggestion))
	if err != nil {
		return
	}
	_ = writeFile(path, body)
}

// readApprovals is the rules in an approvals file, each checked again as a
// grant would be so a hand-edited file cannot widen past one. Never nil, so
// a caller can tell "read, none" from "not read".
func readApprovals(path string) []string {
	out := []string{}
	if path == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var rules []string
	if json.Unmarshal(raw, &rules) != nil {
		return out
	}
	for _, r := range rules {
		if grant.CheckRule(r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func writeFile(path string, body []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appendStatus(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
