// Package agentnudge steers a board session's built-in Agent calls onto the
// board. Claude Code's Agent tool (Task in older builds) runs a subagent
// inside the calling conversation: the user never sees it on the board, and
// its permission prompts and questions never reach a parent that could answer
// them. A board session has create_session for that work, and the launch
// instructions say so, yet sessions kept reaching for Agent, so a PreToolUse
// hook turns the instruction into a check at the moment it matters.
//
// A read-only subagent is a lookup, which the instructions keep in-process,
// so it passes. A subagent that can edit files or start agents of its own is
// work, and its first call is denied with a create_session call ready to
// send. The deny is once per call: sending the same call again runs it, so a
// session whose gate-inbox tools are down, or whose task needs the
// in-process subagent's shared context, is never stuck.
package agentnudge

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// Verdict is what the hook decided for one Agent call.
type Verdict int

const (
	// Allow passes the call: it is not an Agent call, or its subagent is
	// read-only.
	Allow Verdict = iota
	// Retry passes a write-capable call this session was already steered
	// away from once.
	Retry
	// Steer denies a write-capable call with a create_session call to send
	// instead.
	Steer
)

// readOnlyBuiltins are Claude Code's own subagent types that cannot change
// the checkout: Explore and Plan have every tool but Agent, Edit, Write and
// NotebookEdit; claude-code-guide has Bash, Read, WebFetch and WebSearch.
// statusline-setup can Edit, but only the user's status line setting, which
// is not work a board session would take.
var readOnlyBuiltins = []string{"Explore", "Plan", "claude-code-guide", "statusline-setup"}

// writeTools are the tools that make a subagent write-capable: they change
// files or start further agents. Bash is not among them, for the same reason
// Explore keeps it: a lookup runs git log and grep.
var writeTools = []string{"Edit", "MultiEdit", "Write", "NotebookEdit", "Agent", "Task"}

// Input is the part of a PreToolUse payload the hook reads.
type Input struct {
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		SubagentType string `json:"subagent_type"`
		Description  string `json:"description"`
		Prompt       string `json:"prompt"`
		Model        string `json:"model"`
	} `json:"tool_input"`
}

// Subagent is the subagent type a call asks for; an omitted type is
// general-purpose, as Claude Code runs it.
func (in Input) Subagent() string {
	if t := strings.TrimSpace(in.ToolInput.SubagentType); t != "" {
		return t
	}
	return "general-purpose"
}

// IsAgentCall reports whether the payload is a built-in subagent call.
func (in Input) IsAgentCall() bool {
	return in.ToolName == "Agent" || in.ToolName == "Task"
}

// ReadOnly reports whether subagent cannot write: a read-only built-in, or a
// custom agent whose definition, found from cwd or home, withholds every
// write tool. general-purpose, fork and any agent without a definition are
// write-capable.
func ReadOnly(subagent, cwd, home string) bool {
	if slices.Contains(readOnlyBuiltins, subagent) {
		return true
	}
	def, ok := findDefinition(subagent, cwd, home)
	return ok && def.readOnly()
}

// Classify sorts an Agent call into Allow or Steer without the once-only
// memory; a call that is not an Agent call is Allow.
func Classify(in Input, home string) Verdict {
	if !in.IsAgentCall() || ReadOnly(in.Subagent(), in.Cwd, home) {
		return Allow
	}
	return Steer
}

// Key names one call, so a resent identical call can be told from a new one.
func Key(in Input) string {
	sum := sha256.Sum256([]byte(in.Subagent() + "\x00" + in.ToolInput.Description + "\x00" + in.ToolInput.Prompt))
	return hex.EncodeToString(sum[:8])
}

// definition is the frontmatter of a custom subagent's markdown file.
type definition struct {
	tools      []string // nil when the field is absent: every tool
	disallowed []string
}

func (d definition) readOnly() bool {
	for _, tool := range writeTools {
		if slices.Contains(d.disallowed, tool) {
			continue
		}
		if d.tools == nil || slices.Contains(d.tools, tool) || slices.Contains(d.tools, "*") {
			return false
		}
	}
	return true
}

// findDefinition looks for the subagent's file the way Claude Code loads
// them: the project agents directory in the working directory or any parent
// of it, then the user's own. A plugin's agent (plugin:name) has no file
// here and is treated as unknown.
func findDefinition(name, cwd, home string) (definition, bool) {
	if strings.Contains(name, ":") || strings.ContainsAny(name, `/\`) {
		return definition{}, false
	}
	var dirs []string
	if cwd != "" {
		for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
			dirs = append(dirs, filepath.Join(dir, ".claude", "agents"))
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".claude", "agents"))
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			if fields := frontmatter(raw); fields["name"] == name {
				return definition{
					tools:      toolList(fields, "tools"),
					disallowed: toolList(fields, "disallowedTools"),
				}, true
			}
		}
	}
	return definition{}, false
}

// frontmatter reads the flat key: value lines between a file's leading ---
// fences. A YAML list under a key is folded into one comma-separated value.
func frontmatter(raw []byte) map[string]string {
	fields := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return fields
	}
	var last string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if item, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok && last != "" {
			fields[last] = strings.TrimPrefix(fields[last]+","+item, ",")
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") {
			continue
		}
		last = strings.TrimSpace(key)
		fields[last] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return fields
}

// toolList splits a tools field; nil means the field is absent.
func toolList(fields map[string]string, key string) []string {
	value, ok := fields[key]
	if !ok {
		return nil
	}
	value = strings.Trim(value, "[]")
	tools := []string{}
	for tool := range strings.SplitSeq(value, ",") {
		// A rule such as Edit(src/**) still grants the tool it names.
		name, _, _ := strings.Cut(strings.Trim(strings.TrimSpace(tool), `"'`), "(")
		if name = strings.TrimSpace(name); name != "" {
			tools = append(tools, name)
		}
	}
	return tools
}

// SessionName turns an Agent description into a kebab-case board name of
// at most four words.
func SessionName(description string) string {
	words := strings.FieldsFunc(strings.ToLower(description), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(words) > 4 {
		words = words[:4]
	}
	if len(words) == 0 {
		return "delegated-task"
	}
	return strings.Join(words, "-")
}

// inlinePromptLimit is the longest prompt the steer message carries in
// place; a longer one is written to a file and named by prompt_file, so the
// message does not repeat a long brief the model has just written.
const inlinePromptLimit = 2000

// CreateArgs is the create_session call that replaces the Agent call.
// briefPath is where a long prompt is written; empty keeps it inline.
func CreateArgs(in Input, briefPath string) (map[string]string, error) {
	args := map[string]string{"name": SessionName(in.ToolInput.Description)}
	if in.Cwd != "" {
		args["directory"] = in.Cwd
	}
	if model := in.ToolInput.Model; model != "" && model != "inherit" {
		args["model"] = model
	}
	prompt := in.ToolInput.Prompt
	if len(prompt) <= inlinePromptLimit || briefPath == "" {
		args["prompt"] = prompt
		return args, nil
	}
	if err := os.WriteFile(briefPath, []byte(prompt), 0o600); err != nil {
		return nil, err
	}
	args["prompt_file"] = briefPath
	return args, nil
}

// Reason is the deny message: why, the call to send, and the way out.
func Reason(in Input, args map[string]string) string {
	// Unescaped, so a prompt's <, > and & read as written.
	var call bytes.Buffer
	encoder := json.NewEncoder(&call)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(args)
	return "Gate Inbox: run this " + in.Subagent() + " subagent as a board session instead, so your user " +
		"sees it on the board and its questions and permission prompts come to you. " +
		"Open a new session only if your user explicitly asked for one: otherwise send_session the task to a session " +
		"already on the board that owns it (interrupt true if its current turn is wrong), or do it yourself. If they asked, call " +
		"mcp__gate-inbox__create_session with " + strings.TrimSpace(call.String()) + ", then wait_for_session for its report. " +
		"Read-only subagents (Explore, Plan, claude-code-guide) still run in-process. " +
		"If the gate-inbox tools are unavailable, or this task needs this conversation's context, " +
		"send the same " + in.ToolName + " call again unchanged and it will run."
}

// Output is the PreToolUse hook output that denies with reason.
func Output(reason string) string {
	out, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       "deny",
		"permissionDecisionReason": reason,
	}})
	if err != nil {
		return ""
	}
	return string(out)
}
