package grant

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Grants on a session Gate Inbox did not launch.
//
// A launched session reads its grants from the settings file on its command
// line. A session adopted from outside the board loaded no such file, and
// Claude Code reads settings only as a session starts, so what reaches it
// live is what its PreToolUse hook can say: allow one call. Live is the line
// between what that covers and what waits for a resume, and Allows is the
// check the hook runs on each call.
//
// Allows is deliberately narrower than Claude Code's own matching. A call it
// does not let through is not refused; it goes to the session's own
// permission flow, exactly as it would have without the grant. So every case
// it cannot read for certain -- a compound shell command, a path rule written
// relative to a settings file, a tool whose rule syntax it does not know --
// falls through to asking.

// Call is one tool call as a PreToolUse hook payload carries it.
type Call struct {
	Tool  string
	Input map[string]any
	// Cwd is the session's working directory, and Home the user's.
	Cwd, Home string
}

// fileTools are the tools each file rule covers, as Claude Code applies them:
// a Read rule to every tool that reads, an Edit rule to every tool that
// edits.
var fileTools = map[string][]string{
	"Read":         {"Read", "Grep", "Glob", "LS", "NotebookRead"},
	"Edit":         {"Edit", "MultiEdit", "Write", "NotebookEdit"},
	"Write":        {"Write"},
	"MultiEdit":    {"MultiEdit"},
	"NotebookEdit": {"NotebookEdit"},
}

// searchTools take a directory and read what is under it.
var searchTools = []string{"Grep", "Glob", "LS"}

// Live splits what g does for an adopted session: live is what its hook
// applies from the next tool call, resume what only a resume on the board
// brings, each "" when there is none.
func Live(g Grant) (live, resume string) {
	g = Normalise(g)
	switch g.Kind {
	case KindRule:
		if liveRule(g.Value) {
			return Describe(g), ""
		}
		return "", fmt.Sprintf("the permission rule %s, which a hook cannot apply (Gate Inbox applies Bash, "+
			"file and WebFetch rules live, and file rules only with an absolute, home or working-directory path)", g.Value)
	case KindUnsandboxed:
		return fmt.Sprintf("running commands that start with %q without asking (the rule %s), including when "+
				"the session asks to run one outside the sandbox", g.Value, Rules(g)[0]),
			fmt.Sprintf("the sandbox exclusion that runs %q outside the sandbox on its own", g.Value)
	case KindDomain:
		return "", Describe(g)
	case KindSoft:
		return Describe(g), ""
	}
	return "", Describe(g)
}

// Allows reports whether g lets call through without asking. Only rule and
// unsandboxed-command grants allow anything; a rule grant never allows a
// shell command the session asked to run outside the sandbox, which only an
// unsandboxed-command grant was approved for.
func Allows(g Grant, call Call) bool {
	g = Normalise(g)
	if Check(g) != nil {
		return false
	}
	switch g.Kind {
	case KindRule:
		if outsideSandbox(call) {
			return false
		}
		return ruleAllows(g.Value, call)
	case KindUnsandboxed:
		return ruleAllows(Rules(g)[0], call)
	}
	return false
}

func outsideSandbox(call Call) bool {
	bypass, _ := call.Input["dangerouslyDisableSandbox"].(bool)
	return bypass
}

// splitRule is a rule's tool and the pattern between its parentheses.
func splitRule(rule string) (tool, pattern string, ok bool) {
	rule = strings.TrimSpace(rule)
	open := strings.Index(rule, "(")
	if open <= 0 || !strings.HasSuffix(rule, ")") {
		return "", "", false
	}
	return rule[:open], strings.TrimSpace(rule[open+1 : len(rule)-1]), true
}

// liveRule reports whether Allows can read rule at all.
func liveRule(rule string) bool {
	tool, pattern, ok := splitRule(rule)
	if !ok {
		return false
	}
	switch {
	case tool == "Bash":
		return true
	case tool == "WebFetch":
		return strings.HasPrefix(pattern, "domain:")
	case fileTools[tool] != nil:
		return !singleSlash(pattern)
	}
	return false
}

// singleSlash is a path pattern Claude Code reads relative to the settings
// file it sits in, which an adopted session never loaded.
func singleSlash(pattern string) bool {
	return strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "//")
}

func ruleAllows(rule string, call Call) bool {
	tool, pattern, ok := splitRule(rule)
	if !ok || pattern == "" {
		return false
	}
	switch {
	case tool == "Bash":
		command, _ := call.Input["command"].(string)
		return call.Tool == "Bash" && commandAllowed(pattern, command)
	case tool == "WebFetch":
		host, ok := strings.CutPrefix(pattern, "domain:")
		raw, _ := call.Input["url"].(string)
		u, err := url.Parse(raw)
		return ok && call.Tool == "WebFetch" && err == nil && u.Hostname() != "" &&
			strings.EqualFold(u.Hostname(), strings.TrimSpace(host))
	case slices.Contains(fileTools[tool], call.Tool):
		return pathAllowed(pattern, call)
	}
	return false
}

// commandAllowed matches one shell command against a Bash rule's pattern:
// "prefix:*" for the prefix as a whole word, a pattern with * for anything in
// its place, and anything else for the exact command. A command that is more
// than one command -- a separator, a pipe, a substitution, a redirect, a
// second line -- matches nothing, since a prefix would otherwise allow
// whatever is chained after it.
func commandAllowed(pattern, command string) bool {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, commandMeta) {
		return false
	}
	if prefix, ok := strings.CutSuffix(pattern, ":*"); ok {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" || !strings.HasPrefix(command, prefix) {
			return false
		}
		if len(command) == len(prefix) || command[len(prefix)] == ' ' {
			return true
		}
		last := prefix[len(prefix)-1]
		return !(last >= 'a' && last <= 'z' || last >= 'A' && last <= 'Z' || last >= '0' && last <= '9' || last == '_' || last == '-')
	}
	if strings.Contains(pattern, "*") {
		parts := strings.Split(pattern, "*")
		for i, p := range parts {
			parts[i] = regexp.QuoteMeta(p)
		}
		re, err := regexp.Compile("^" + strings.Join(parts, ".*") + "$")
		return err == nil && re.MatchString(command)
	}
	return command == pattern
}

// pathAllowed matches the path a file tool is given against a rule's path
// pattern. A search tool is matched as reaching everything under the
// directory it is given, so a rule covering only some of it covers none.
func pathAllowed(pattern string, call Call) bool {
	root, ok := resolvePattern(pattern, call)
	if !ok {
		return false
	}
	re, err := globRegexp(root)
	if err != nil {
		return false
	}
	target := ""
	for _, field := range []string{"file_path", "notebook_path", "path"} {
		if v, _ := call.Input[field].(string); v != "" {
			target = v
			break
		}
	}
	search := slices.Contains(searchTools, call.Tool)
	if target == "" {
		if !search {
			return false
		}
		target = call.Cwd
	}
	if search {
		if glob, _ := call.Input["pattern"].(string); call.Tool == "Glob" && (filepath.IsAbs(glob) || strings.Contains(glob, "..")) {
			return false
		}
	}
	path, ok := resolvePath(target, call)
	if !ok {
		return false
	}
	candidates := []string{path}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		candidates = append(candidates, resolved)
	}
	for _, c := range candidates {
		if search {
			c = filepath.Join(c, "\x01", "\x01")
		}
		if !re.MatchString(c) {
			return false
		}
	}
	return true
}

// resolvePattern makes a rule's path pattern absolute the way Claude Code
// reads it: //path from the root, ~/path from the home directory, and a bare
// or ./ path from the working directory.
func resolvePattern(pattern string, call Call) (string, bool) {
	switch {
	case strings.HasPrefix(pattern, "//"):
		return "/" + strings.TrimLeft(pattern, "/"), true
	case singleSlash(pattern):
		return "", false
	case strings.HasPrefix(pattern, "~/"):
		if call.Home == "" {
			return "", false
		}
		return call.Home + "/" + pattern[2:], true
	case strings.HasPrefix(pattern, "~"):
		return "", false
	}
	if call.Cwd == "" || !filepath.IsAbs(call.Cwd) {
		return "", false
	}
	return filepath.Clean(call.Cwd) + "/" + strings.TrimPrefix(pattern, "./"), true
}

func resolvePath(path string, call Call) (string, bool) {
	if strings.HasPrefix(path, "~/") {
		if call.Home == "" {
			return "", false
		}
		path = filepath.Join(call.Home, path[2:])
	}
	if !filepath.IsAbs(path) {
		if call.Cwd == "" || !filepath.IsAbs(call.Cwd) {
			return "", false
		}
		path = filepath.Join(call.Cwd, path)
	}
	return filepath.Clean(path), true
}

// globRegexp reads an absolute path pattern: ** for any run of directories, *
// and ? within one name. A pattern with no wildcard names a file or a
// directory and everything under it.
func globRegexp(pattern string) (*regexp.Regexp, error) {
	if strings.Contains(pattern, "/../") || strings.HasSuffix(pattern, "/..") {
		return nil, fmt.Errorf("grant: %q climbs out of its directory", pattern)
	}
	if !strings.ContainsAny(pattern, "*?") {
		pattern = strings.TrimSuffix(pattern, "/")
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case c == '*' && strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case c == '*' && strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if !strings.ContainsAny(pattern, "*?") {
		b.WriteString("(?:/.*)?")
	} else if strings.HasSuffix(pattern, "/") {
		b.WriteString(".*")
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
