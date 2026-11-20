package grant

import (
	"regexp"
	"slices"
	"strings"
)

// Rules a person wrote in a Claude Code settings file.
//
// A grant is checked before it is written, so Allows refuses the shapes Check
// refuses. A settings file's rules were written by the person whose machine it
// is, and the board applies them as Claude Code does for a session with no
// settings of its own to read them (a pi session): a bare tool name covers the
// whole tool, and an MCP server's name covers every tool it serves.
//
// The two directions are read differently, each toward asking. An allow rule
// is read as narrowly as a grant: a compound shell command matches no prefix,
// since a prefix would otherwise allow whatever is chained after it. A deny or
// ask rule is read broadly: each command in a compound one is matched on its
// own, so a deny of rm -rf still catches `cd x && rm -rf y`.

// MatchesAllow reports whether a settings permissions.allow rule lets call
// through without asking.
func MatchesAllow(rule string, call Call) bool {
	rule = strings.TrimSpace(rule)
	if covered, whole := wholeTool(rule, call.Tool); whole {
		if !covered {
			return false
		}
		// Running outside the sandbox is a separate approval, as it is for a
		// grant (Allows): a rule for the tool does not give it.
		return !(call.Tool == "Bash" && outsideSandbox(call))
	}
	if call.Tool == "Bash" && outsideSandbox(call) {
		return false
	}
	return ruleAllows(rule, call)
}

// MatchesDeny reports whether a settings permissions.deny or permissions.ask
// rule covers call.
func MatchesDeny(rule string, call Call) bool {
	rule = strings.TrimSpace(rule)
	if covered, whole := wholeTool(rule, call.Tool); whole {
		return covered
	}
	tool, pattern, ok := splitRule(rule)
	if !ok || pattern == "" {
		return false
	}
	if tool == "Bash" {
		if call.Tool != "Bash" {
			return false
		}
		command, _ := call.Input["command"].(string)
		return slices.ContainsFunc(Commands(command), func(c string) bool { return commandMatches(pattern, c) })
	}
	return ruleAllows(rule, call)
}

// wholeTool reads a rule that names a tool, or an MCP server, with no
// pattern: whole is true when rule is one, and covered when it covers tool.
func wholeTool(rule, tool string) (covered, whole bool) {
	name := rule
	if t, pattern, ok := splitRule(rule); ok {
		if pattern != "" && pattern != "*" {
			return false, false
		}
		name = t
	} else if strings.ContainsAny(rule, "()") {
		return false, true
	}
	if name == "" {
		return false, true
	}
	if server, ok := strings.CutSuffix(name, "__*"); ok && strings.HasPrefix(server, "mcp__") {
		return strings.HasPrefix(tool, server+"__"), true
	}
	if strings.HasPrefix(name, "mcp__") && strings.Count(name, "__") == 1 {
		return strings.HasPrefix(tool, name+"__"), true
	}
	if name == tool {
		return true, true
	}
	return slices.Contains(fileTools[name], tool), true
}

// commandSplit cuts a shell command at everything that starts another
// command: separators, pipes, a background &, a subshell's parentheses and a
// command substitution.
var commandSplit = regexp.MustCompile("&&|\\|\\||[;|&\\n\\r`()]|\\$\\(")

// assignment is one VAR=value word in front of a command.
var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S*\s+`)

// Commands is every command a shell command line runs, as near as text can
// tell: each piece between separators, with VAR=value words in front of it
// dropped. It reads toward finding more commands than run, never fewer.
func Commands(command string) []string {
	var out []string
	for _, part := range commandSplit.Split(command, -1) {
		part = strings.TrimSpace(part)
		for {
			rest := assignment.ReplaceAllString(part, "")
			if rest == part {
				break
			}
			part = rest
		}
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
