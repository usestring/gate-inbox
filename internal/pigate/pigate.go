// Package pigate decides each tool call a pi session makes: allow, deny or
// ask. The board's pi extension (package piext) sends every call to
// `gate-inbox hook pi PreToolUse` and waits for the answer before pi runs
// it, so the decision is made here, in the same Go rule code Claude Code
// sessions are judged by (package grant), and not by a second rule engine in
// the extension.
//
// The order, toward asking at every step:
//
//  1. Gate Inbox's hard denials: the sealing-key directory.
//  2. The operator's Claude Code permission rules (managed, user and project
//     settings): a deny anywhere beats an ask anywhere, which beats an allow.
//  3. The merge gate, for a gh pr merge: the standing merge authority allows
//     it. Otherwise only a grant or an allow rule can; the fast path never
//     does.
//  4. Board grants in force for the session, and standing grants, read on
//     every call so a new one applies on the next call with no restart; then
//     the rules a person approved at this session's own dialog with "don't
//     ask again".
//  5. Allow rules from the settings files.
//  6. The read-only fast path: reading or searching inside the working
//     directory, simple read-only shell commands that stay inside it, and an
//     MCP tool its server marks read-only.
//  7. Otherwise ask.
//
// A grant is applied after the rules' denials and asks, as a launched Claude
// Code session reads its grants from an allow list its own deny and ask rules
// still beat.
package pigate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/grant"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/store"
)

// The three answers.
const (
	Allow = "allow"
	Deny  = "deny"
	Ask   = "ask"
)

// Request is what the extension sends for one tool call.
type Request struct {
	// ToolName is pi's own name for the tool (bash, read, mcp__x__y, ...).
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	Cwd       string         `json:"cwd"`
	ToolUseID string         `json:"tool_use_id"`
	// ReadOnly is the readOnlyHint the tool's own registration declares
	// (pi.getAllTools), which the model cannot set. It counts only for an
	// MCP tool.
	ReadOnly bool `json:"read_only,omitempty"`
}

// Decision is the answer the extension reads back.
type Decision struct {
	Decision string `json:"permissionDecision"`
	Reason   string `json:"reason,omitempty"`
	// Tool is the call's tool under its Claude Code name, which is what the
	// rules and the dialog name it by.
	Tool string `json:"tool"`
	// Suggestion is a rule that would allow calls like this one, offered
	// with an ask as "don't ask again for".
	Suggestion string `json:"ruleSuggestion,omitempty"`
	// Title and Block are what the permission dialog shows for an ask: what
	// kind of call it is, and the full command, path or URL it is about.
	Title string `json:"title,omitempty"`
	Block string `json:"block,omitempty"`
}

// piTools maps pi's built-in tool names to Claude Code's, so the operator's
// Claude rules apply to pi unchanged. An MCP tool already has Claude's name,
// and any other tool keeps its own.
var piTools = map[string]string{
	"bash":  "Bash",
	"read":  "Read",
	"write": "Write",
	"edit":  "Edit",
	"grep":  "Grep",
	"find":  "Glob",
	"ls":    "LS",
}

// ToolName is a pi tool's name as Claude Code's rules spell it.
func ToolName(pi string) string {
	if name, ok := piTools[pi]; ok {
		return name
	}
	return pi
}

// mcpForm spells an MCP tool name, or a rule naming one, with its server's
// hyphens as underscores. Pi registers a server's tools under that spelling
// (mcp__gate_inbox__send_session for the board's mcp__gate-inbox__ server),
// while rules and grants are written as Claude Code spells them, so both
// sides of a match are put in the one spelling. Anything else is unchanged.
func mcpForm(name string) string {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return name
	}
	server, tail, found := strings.Cut(rest, "__")
	server = strings.ReplaceAll(server, "-", "_")
	if !found {
		return "mcp__" + server
	}
	return "mcp__" + server + "__" + tail
}

// passThrough are tools that run other tools: each call they make reaches
// tool_call again, with its own name, and is decided there.
var passThrough = map[string]bool{"codemode": true, "tool_search": true}

// onlyAsks are tools that change nothing and only put a question to the
// user. Claude Code never asks permission before them, and a dialog drawn in
// front of a question would only ask the user twice; a deny or ask rule
// naming one still decides it.
var onlyAsks = map[string]bool{"AskUserQuestion": true}

// Env is everything a decision reads beyond the call itself.
type Env struct {
	ConfigDir, SessionID string
	// StatusFile is the session's status file, beside which its dialog
	// approvals are kept.
	StatusFile       string
	Home, ClaudeHome string
	// Rules are the settings files' permission lists. Nil means load them
	// from Sources for the call's cwd.
	Rules []Rules
	// Grants are the board grants in force on the session. Nil means read
	// them from the store.
	Grants []grant.Grant
	// Approvals are the rules a person approved at the session's own
	// dialog with "don't ask again". Nil means read them from the file.
	Approvals []string
	// MergeGate reports whether the standing merge authority covers a
	// gh pr merge command, and why, or why not. Nil means no merge is
	// covered.
	MergeGate func(command, cwd string) (string, bool)
	Now       time.Time
}

// Decide answers one call.
func Decide(env Env, req Request) Decision {
	tool := ToolName(req.ToolName)
	d := Decision{Tool: tool}
	if req.ToolInput == nil {
		req.ToolInput = map[string]any{}
	}
	if passThrough[req.ToolName] {
		d.Decision, d.Reason = Allow, "each call it makes is decided on its own"
		return d
	}
	if env.ConfigDir != "" {
		payload, _ := json.Marshal(map[string]any{"tool_name": tool, "tool_input": req.ToolInput, "cwd": req.Cwd})
		if out := hooks.NewManager(env.ConfigDir).KeyDirDecision(payload); out != "" {
			d.Decision, d.Reason = Deny, hookReason(out)
			return d
		}
	}
	call := grant.Call{Tool: mcpForm(tool), Input: req.ToolInput, Cwd: req.Cwd, Home: env.Home}
	rules := env.Rules
	if rules == nil {
		rules = Load(Sources(env.ClaudeHome, env.Home, req.Cwd))
	}
	for _, r := range rules {
		c := call
		c.RuleBase = r.Source.Base
		for _, rule := range r.Deny {
			if grant.MatchesDeny(mcpForm(rule), c) {
				d.Decision, d.Reason = Deny, fmt.Sprintf("the deny rule %s in the %s (%s)", rule, r.Source.Label, r.Source.Path)
				return d
			}
		}
	}
	d.Suggestion = suggest(call)
	for _, r := range rules {
		c := call
		c.RuleBase = r.Source.Base
		for _, rule := range r.Ask {
			if grant.MatchesDeny(mcpForm(rule), c) {
				return asking(d, call, fmt.Sprintf("the ask rule %s in the %s", rule, r.Source.Label))
			}
		}
	}
	if onlyAsks[tool] {
		d.Decision, d.Reason = Allow, "it only asks the user"
		return d
	}
	var mergeRefusal string
	if merge := mergeCommand(call); merge != "" {
		if env.MergeGate != nil {
			why, ok := env.MergeGate(merge, req.Cwd)
			if ok {
				d.Decision, d.Reason = Allow, why
				return d
			}
			mergeRefusal = why
		}
	}
	grants := env.Grants
	if grants == nil {
		grants = sessionGrants(env)
	}
	for _, g := range grants {
		g.Value = mcpForm(g.Value)
		if grant.Allows(g, call) {
			d.Decision, d.Reason = Allow, "Gate Inbox grant: "+grant.Describe(g)
			return d
		}
	}
	approvals := env.Approvals
	if approvals == nil && env.StatusFile != "" {
		approvals = readApprovals(env.StatusFile + hooks.PiApprovalsSuffix)
	}
	for _, rule := range approvals {
		if grant.Allows(grant.Grant{Kind: grant.KindRule, Value: rule}, call) {
			d.Decision, d.Reason = Allow, "approved at this session's dialog: "+rule
			return d
		}
	}
	for _, r := range rules {
		c := call
		c.RuleBase = r.Source.Base
		for _, rule := range r.Allow {
			if grant.MatchesAllow(mcpForm(rule), c) {
				d.Decision, d.Reason = Allow, fmt.Sprintf("the allow rule %s in the %s", rule, r.Source.Label)
				return d
			}
		}
	}
	if mergeCommand(call) == "" {
		if why, ok := readOnly(call, req.ReadOnly); ok {
			d.Decision, d.Reason = Allow, why
			return d
		}
	}
	reason := "no rule or grant allows it"
	if mergeRefusal != "" {
		reason = mergeRefusal
	}
	return asking(d, call, reason)
}

// asking is d as an ask, with what its dialog shows.
func asking(d Decision, call grant.Call, reason string) Decision {
	d.Decision, d.Reason = Ask, reason
	d.Title, d.Block = prompt(call)
	return d
}

// hookReason is the reason a Claude-shaped PreToolUse output gives.
func hookReason(out string) string {
	var doc struct {
		Output struct {
			Reason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if json.Unmarshal([]byte(out), &doc) != nil || doc.Output.Reason == "" {
		return "Gate Inbox refuses this call"
	}
	return doc.Output.Reason
}

// mergeCommand is a shell call's command when any command in it runs
// gh pr merge, and "" otherwise.
func mergeCommand(call grant.Call) string {
	if call.Tool != "Bash" {
		return ""
	}
	command, _ := call.Input["command"].(string)
	for _, c := range grant.Commands(command) {
		if strings.HasPrefix(strings.Join(strings.Fields(c), " "), "gh pr merge") {
			return command
		}
	}
	return ""
}

// suggest is the rule a person could approve so calls like this one stop
// asking: the command's first word or two as a prefix, or the file's own path.
// It offers only a rule a grant could be (grant.CheckRule), and none for a
// compound command.
func suggest(call grant.Call) string {
	var rule string
	switch call.Tool {
	case "Bash":
		command, _ := call.Input["command"].(string)
		if len(grant.Commands(command)) != 1 || strings.ContainsAny(command, ";&|`$<>\n\r") {
			return ""
		}
		words := strings.Fields(command)
		if len(words) == 0 {
			return ""
		}
		prefix := words[0]
		if len(words) > 1 && !strings.HasPrefix(words[1], "-") {
			prefix += " " + words[1]
		}
		rule = "Bash(" + prefix + ":*)"
	case "Edit", "Write":
		path, _ := call.Input["path"].(string)
		if path == "" {
			return ""
		}
		if !filepath.IsAbs(path) {
			if call.Cwd == "" {
				return ""
			}
			path = filepath.Join(call.Cwd, path)
		}
		rule = "Edit(/" + filepath.Clean(path) + ")"
	default:
		return ""
	}
	if grant.CheckRule(rule) != nil {
		return ""
	}
	return rule
}

// sessionGrants is every grant in force on the session: its own rows in the
// store and the standing grants. The session's own settings file is written
// whenever its row holds a grant and removed when it holds none, so its
// absence ends the lookup without opening the store, which is nearly every
// call.
func sessionGrants(env Env) []grant.Grant {
	if env.ConfigDir == "" {
		return nil
	}
	m := hooks.NewManager(env.ConfigDir)
	out := m.StandingGrants()
	if env.SessionID == "" {
		return out
	}
	if _, err := os.Stat(m.SessionSettingsPath(env.SessionID)); err != nil {
		return out
	}
	path := filepath.Join(env.ConfigDir, "state.db")
	if _, err := os.Stat(path); err != nil {
		return out
	}
	st, err := store.Open(path)
	if err != nil {
		return out
	}
	defer st.Close()
	rows, err := st.Grants(env.SessionID, true)
	if err != nil {
		return out
	}
	now := env.Now
	if now.IsZero() {
		now = time.Now()
	}
	for _, row := range rows {
		if !row.ExpiresAt.IsZero() && !now.Before(row.ExpiresAt) {
			continue
		}
		g := grant.Grant{Kind: grant.Kind(row.Kind), Value: row.Value}
		if !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}
