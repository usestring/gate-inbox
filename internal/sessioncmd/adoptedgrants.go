package sessioncmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
)

// Grants on an adopted session.
//
// A launched session's grants ride the settings file on its command line. An
// adopted claude loaded no such file and will not load one until it is
// resumed, so its grants are applied by its global hooks instead, from the
// ledger, on every call: a rule or command-prefix grant answers PreToolUse
// with allow for the calls it covers, and a soft grant reaches the auto-mode
// classifier as a PostToolUse note. A sandbox exclusion and a network domain
// have no hook form and wait for a resume (grant.Live).
//
// The ledger is read on each call rather than once, so a revoke takes effect
// on the next call and a grant lapses the moment its window closes, without
// the board's expiry pass having to restart anything.
//
// Both hooks run only from the global dispatch, which runs only for a pane the
// board adopted (hooks.DispatchGlobal); they check again that the row is an
// adopted one, so nothing here can reach a session the board launched, whose
// grants are its settings file's alone.

// adoptedGrants is the grants in force on an adopted row id at now, or none.
// The session settings file is written whenever a row holds a grant and
// removed when it holds none, so its absence ends the hook without opening
// the store, which is every call of nearly every adopted session.
func adoptedGrants(configDir, id string, now time.Time) []store.PermissionGrant {
	if id == "" {
		return nil
	}
	if _, err := os.Stat(hooks.NewManager(configDir).SessionSettingsPath(id)); err != nil {
		return nil
	}
	path := filepath.Join(configDir, "state.db")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	st, err := store.Open(path)
	if err != nil {
		return nil
	}
	defer st.Close()
	sess, err := st.Get(id)
	if err != nil || sess.TmuxPaneID == "" {
		return nil
	}
	rows, err := st.Grants(id, true)
	if err != nil {
		return nil
	}
	var live []store.PermissionGrant
	for _, row := range rows {
		if !row.ExpiresAt.IsZero() && !now.Before(row.ExpiresAt) {
			continue
		}
		live = append(live, row)
	}
	return live
}

type hookCall struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	Cwd       string         `json:"cwd"`
}

// AdoptedGrantDecision is the PreToolUse output that lets an adopted
// session's call through on a grant in force, and "" for every other call,
// which goes to the session's own permission flow as before.
func AdoptedGrantDecision(configDir, sessionID string, payload []byte, now time.Time) string {
	var call hookCall
	if json.Unmarshal(payload, &call) != nil || call.ToolInput == nil {
		return ""
	}
	grants := adoptedGrants(configDir, sessionID, now)
	if len(grants) == 0 {
		return ""
	}
	home, _ := os.UserHomeDir()
	c := grant.Call{Tool: call.ToolName, Input: call.ToolInput, Cwd: call.Cwd, Home: home}
	for _, row := range grants {
		g := grant.Grant{Kind: grant.Kind(row.Kind), Value: row.Value}
		if !grant.Allows(g, c) {
			continue
		}
		out, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":      "PreToolUse",
			"permissionDecision": "allow",
			"permissionDecisionReason": fmt.Sprintf("Gate Inbox grant from parent session %s, approved by its user "+
				"until %s: %s", row.GrantedBy, row.ExpiresAt.Format("15:04"), grant.Describe(g)),
		}})
		if err != nil {
			return ""
		}
		return string(out)
	}
	return ""
}

// AdoptedSoftNote is the PostToolUse output that tells an adopted session's
// auto-mode classifier about a soft grant in force: grant.SoftRule, the words
// a launch puts in autoMode.allow, as classifierContext. Each grant is said
// once to each claude adopted into the row, on a call the classifier sees, and
// one per call, since the field is capped across every hook on the call.
func AdoptedSoftNote(configDir, sessionID string, agentPID int, payload []byte, now time.Time) string {
	var call hookCall
	if json.Unmarshal(payload, &call) != nil || readOnlyLookups[call.ToolName] || agentPID <= 0 {
		return ""
	}
	m := hooks.NewManager(configDir)
	for _, row := range adoptedGrants(configDir, sessionID, now) {
		if grant.Kind(row.Kind) != grant.KindSoft {
			continue
		}
		if !m.NoteOnce(sessionID, agentPID, strconv.FormatInt(row.ID, 10)) {
			continue
		}
		out, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":     "PostToolUse",
			"classifierContext": grant.SoftRule(row.Value),
		}})
		if err != nil {
			return ""
		}
		return string(out)
	}
	return ""
}
