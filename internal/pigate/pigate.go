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
//     it, and otherwise it asks, with no rule offered. Nothing below can
//     allow a merge the gate refused.
//  4. Board grants in force for the session, and standing grants, read on
//     every call so a new one applies on the next call with no restart; then
//     the rules a person approved at this session's own dialog with "don't
//     ask again".
//  5. Allow rules from the settings files.
//  6. Local operation rules for reads, trusted creation commands and
//     pushes to opted-in repositories through the PR creation command.
//  7. What nothing above settled is then decided by the gate's mode
//     ([tools.<pi>] gate in the board's config):
//     - "exceptions" (the default), deny by exception: a call on the
//     deterministic danger list (danger.go) asks; otherwise the JEV
//     classifier, asked on the board's side (package piauto), is asked
//     whether it falls into a danger class, and a confident yes asks.
//     Everything else runs. When the classifier gives no verdict
//     (no key, auto mode off, a failure or timeout), classifier_failure
//     decides: "allow" (the default) runs it, "ask" asks.
//     - "allowlist", the gate before it: the JEV classifier may allow a call
//     it judges safe, and everything else asks.
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
	"github.com/usestring/gate-inbox/internal/piauto"
	"github.com/usestring/gate-inbox/internal/piext"
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
	// MCPServer and MCPTool are an omp MCP tool's server and tool as its
	// registration names them (mcpServerName and mcpToolName in
	// pi.getAllTools), which the model cannot set. omp's own name for the
	// tool, mcp__<server>_<tool> sanitised and maybe shortened, cannot be
	// split back into the two, so these are what give the call its Claude
	// Code name. Pi sends neither.
	MCPServer string `json:"mcp_server,omitempty"`
	MCPTool   string `json:"mcp_tool,omitempty"`
	// Brief and Recent are the session's first and latest user messages,
	// which auto mode shows the classifier.
	Brief  string   `json:"brief,omitempty"`
	Recent []string `json:"recent,omitempty"`
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
	Title          string                  `json:"title,omitempty"`
	Block          string                  `json:"block,omitempty"`
	OperationRules []store.PiOperationRule `json:"-"`
}

// piTools maps pi's built-in tool names to Claude Code's, so the operator's
// Claude rules apply to pi unchanged. An MCP tool already has Claude's name,
// and any other tool keeps its own. omp, pi's fork, adds glob.
var piTools = map[string]string{
	"bash":  "Bash",
	"read":  "Read",
	"write": "Write",
	"edit":  "Edit",
	"grep":  "Grep",
	"find":  "Glob",
	"glob":  "Glob",
	"ls":    "LS",
}

// ToolName is the call's tool as Claude Code's rules spell it. An omp MCP
// tool the extension names the server and tool of is given Claude Code's
// name for it; one it could not, outside the board's own server, keeps omp's
// name, which no Claude rule is written in, so it asks.
func ToolName(req Request) string {
	if name := claudeMCPName(req.MCPServer, req.MCPTool); name != "" {
		return name
	}
	pi := req.ToolName
	if name, ok := piTools[pi]; ok {
		return name
	}
	if tool, ok := strings.CutPrefix(pi, piext.OmpToolPrefix); ok {
		return "mcp__gate-inbox__" + tool
	}
	return pi
}

// claudeMCPName is the Claude Code name of server's tool, or "" without both.
// omp names a plugin's server <plugin>:<server>, and Claude Code names it
// plugin:<plugin>:<server>; either way it then spells the server and the tool
// with each character outside [A-Za-z0-9_-] as an underscore, so a plugin
// server's tools are mcp__plugin_<plugin>_<server>__<tool>. The board's own
// plugin server keeps the name its grants and rules use, mcp__gate-inbox__,
// and a server the board took from the operator's Claude Code config
// (piext.ClaudeMCPPlugin) keeps the name it has there.
func claudeMCPName(server, tool string) string {
	if server == "" || tool == "" {
		return ""
	}
	if server == piext.OmpServer {
		return "mcp__gate-inbox__" + tool
	}
	if name, ok := piext.ClaudeMCPServer(server); ok {
		server = name
	} else if strings.Contains(server, ":") {
		server = "plugin:" + server
	}
	return "mcp__" + claudeMCPPart(server) + "__" + claudeMCPPart(tool)
}

// claudeMCPPart spells a server or tool name as Claude Code puts it in a
// tool name.
func claudeMCPPart(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
}

// ompDevice reports a read or write of an MCP tool's xd:// device, which is
// how omp reaches a tool it mounts there rather than offering by name. The
// read is the tool's docs; the write is its call, which reaches tool_call
// again under the MCP tool's own name and is decided there.
func ompDevice(req Request) bool {
	if req.ToolName != "read" && req.ToolName != "write" {
		return false
	}
	path, _ := req.ToolInput["path"].(string)
	return strings.HasPrefix(path, "xd://mcp__")
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
	MergeGate      func(command, cwd string) (string, bool)
	OperationRules func(grant.Call) []store.PiOperationRule
	// AutoMode asks pi's auto-mode classifier (package piauto) about a call
	// nothing else settled. Nil means auto mode is not asked.
	AutoMode func(piauto.Request) piauto.Answer
	// DenyByException is the default gate: what nothing settled runs unless
	// the danger list or the classifier finds it dangerous. False is the
	// allow-list gate, which asks unless something allows.
	DenyByException bool
	// FailureAsks makes the deny-by-exception gate ask when the classifier
	// gives no verdict, instead of running the call.
	FailureAsks bool
	// SkipPermissions is a session launched with permissions skipped. Like
	// Claude Code's bypass mode, it allows what no deny or ask rule and no
	// merge gate has decided.
	SkipPermissions bool
	Now             time.Time
}

// Decide answers one call.
func Decide(env Env, req Request) Decision {
	tool := ToolName(req)
	d := Decision{Tool: tool}
	if req.ToolInput == nil {
		req.ToolInput = map[string]any{}
	}
	if passThrough[req.ToolName] || ompDevice(req) {
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
	// An ask rule is checked before any approval, so a "don't ask again"
	// rule offered on its dialog would be saved and never apply; like
	// Claude Code, that dialog offers no rule.
	for _, r := range rules {
		c := call
		c.RuleBase = r.Source.Base
		for _, rule := range r.Ask {
			if grant.MatchesDeny(mcpForm(rule), c) {
				return asking(d, call, fmt.Sprintf("the ask rule %s in the %s", rule, r.Source.Label))
			}
		}
	}
	if mergeCommand(call) == "" {
		d.Suggestion = suggest(call)
	}
	if onlyAsks[tool] {
		d.Decision, d.Reason = Allow, "it only asks the user"
		return d
	}
	if merge := mergeCommand(call); merge != "" {
		// The merge gate is terminal, as Claude Code's merge-gate hook is:
		// what it does not cover asks, and no grant, dialog approval, allow
		// rule, fast path or auto mode can allow it, so its dialog offers no
		// rule to remember either.
		why, ok := "merge gate: no standing merge authority is configured", false
		if env.MergeGate != nil {
			why, ok = env.MergeGate(merge, req.Cwd)
		}
		if ok {
			d.Decision, d.Reason = Allow, why
			return d
		}
		d.Suggestion = ""
		return asking(d, call, why)
	}
	if env.SkipPermissions {
		d.Decision, d.Reason = Allow, "permissions are skipped for this session"
		return d
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
	// An allow rule speaks for the sandboxed command only, so a call outside
	// the sandbox asks, and its dialog offers no rule that could not cover it.
	// That holds for a rule approved at this session's own dialog too, which
	// is why this comes before them.
	if outside, _ := call.Input["dangerouslyDisableSandbox"].(bool); outside && tool == "Bash" {
		d.Suggestion = ""
		return asking(d, call, "Bash command, outside the sandbox (requested by the model)")
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
	if why, ok := readOnly(call, req.ReadOnly); ok {
		d.Decision, d.Reason = Allow, why
		d.OperationRules = []store.PiOperationRule{store.PiReadOnly}
		return d
	}
	if env.OperationRules != nil {
		if rules := env.OperationRules(call); len(rules) != 0 {
			d.Decision, d.Reason = Allow, "covered by the configured Pi operation rules"
			d.OperationRules = rules
			return d
		}
	}
	if env.DenyByException {
		return byException(env, req, call, d)
	}
	reason := "no rule or grant allows it"
	if autoModeMayAsk(env, call) {
		a := env.AutoMode(piauto.Request{Tool: tool, Input: req.ToolInput, Cwd: req.Cwd, Brief: req.Brief, Recent: req.Recent})
		if a.Allow {
			d.Decision, d.Reason = Allow, fmt.Sprintf("auto-allowed by JEV (in_scope %.2f, safe %.2f)", a.InScope, a.Safe)
			return d
		}
		if a.Note != "" {
			reason += "; " + a.Note
		}
	}
	return asking(d, call, reason)
}

// byException decides a call nothing settled under the deny-by-exception
// gate: the danger list, then the classifier's danger question, then
// classifier_failure.
func byException(env Env, req Request, call grant.Call, d Decision) Decision {
	if why, ok := dangerous(call); ok {
		return asking(d, call, "Gate Inbox asks about "+why)
	}
	note := "pi auto mode is off"
	if autoModeMayAsk(env, call) {
		a := env.AutoMode(piauto.Request{Tool: d.Tool, Input: req.ToolInput, Cwd: req.Cwd, Sandbox: d.Tool == "Bash",
			Question: piauto.QuestionDanger, Brief: req.Brief, Recent: req.Recent})
		if a.Judged && a.Question == piauto.QuestionDanger {
			if a.Danger >= piauto.DangerThreshold {
				return asking(d, call, fmt.Sprintf("the JEV classifier finds it dangerous (JEV danger %.2f → asking)", a.Danger))
			}
			d.Decision, d.Reason = Allow, fmt.Sprintf("nothing on Gate Inbox's danger list, and JEV finds no danger (danger %.2f)", a.Danger)
			return d
		}
		note = "the JEV classifier did not answer"
		if a.Note != "" {
			note = a.Note
		}
	}
	if env.FailureAsks {
		return asking(d, call, "nothing on Gate Inbox's danger list, but "+note+` (classifier_failure = "ask")`)
	}
	d.Decision, d.Reason = Allow, "nothing on Gate Inbox's danger list; the classifier gave no verdict ("+note+")"
	return d
}

// autoModeMayAsk reports whether auto mode may be asked about call at all.
// It never overrides a request to run outside the sandbox, which only a
// grant or a person can approve. A merge never reaches it: the merge gate
// has already decided.
func autoModeMayAsk(env Env, call grant.Call) bool {
	if env.AutoMode == nil {
		return false
	}
	outside, _ := call.Input["dangerouslyDisableSandbox"].(bool)
	return !outside
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
