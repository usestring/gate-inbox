package pigate

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/usestring/gate-inbox/grant"
)

// The read-only fast path is what Claude Code's default mode runs without
// asking: reading and searching inside the working directory. Everything it
// cannot read for certain asks.

// readOnlyTools read or search the path they are given.
var readOnlyTools = []string{"Read", "Grep", "Glob", "LS"}

// readOnlyCommands are shell commands that only read, each with the
// subcommands that keep it so ("" for the bare command, nil for any).
var readOnlyCommands = map[string][]string{
	"ls": nil, "pwd": nil, "cat": nil, "head": nil, "tail": nil, "wc": nil,
	"file": nil, "stat": nil, "which": nil, "echo": nil, "grep": nil, "rg": nil,
	"tree": nil, "du": nil, "diff": nil,
	"git": {"status", "log", "diff", "show", "branch", "rev-parse", "ls-files", "blame"},
}

// readOnly reports whether call is on the fast path, and why.
func readOnly(call grant.Call, hint bool) (string, bool) {
	switch {
	case strings.HasPrefix(call.Tool, "mcp__"):
		if hint {
			return "its MCP server marks it read-only", true
		}
	case slices.Contains(readOnlyTools, call.Tool):
		if insideCwd(call) {
			return "it reads inside the working directory", true
		}
	case call.Tool == "Bash":
		if readOnlyCommand(call) {
			return "a read-only command inside the working directory", true
		}
	}
	return "", false
}

// insideCwd reports whether a file or search tool's target, and for a glob
// its pattern, stays inside the working directory, symlinks resolved.
func insideCwd(call grant.Call) bool {
	if call.Cwd == "" || !filepath.IsAbs(call.Cwd) {
		return false
	}
	target, _ := call.Input["path"].(string)
	if target == "" {
		target, _ = call.Input["file_path"].(string)
	}
	if target == "" {
		target = "."
	}
	for _, field := range []string{"pattern", "glob"} {
		if call.Tool == "Grep" && field == "pattern" {
			continue // a regular expression, not a path
		}
		if p, _ := call.Input[field].(string); filepath.IsAbs(p) || strings.HasPrefix(p, "~") || strings.Contains(p, "..") {
			return false
		}
	}
	return within(call.Cwd, target)
}

// within reports whether path, read from cwd, is cwd or under it, both
// before and after symlinks are resolved.
func within(cwd, path string) bool {
	if strings.HasPrefix(path, "~") {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	root := filepath.Clean(cwd)
	under := func(p, dir string) bool {
		return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
	}
	if !under(path, root) {
		return false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	// A path that does not exist yet is judged by the nearest directory
	// above it that does, which is where a symlink out would be.
	for p := path; ; p = filepath.Dir(p) {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return under(resolved, resolvedRoot)
		}
		if p == root || filepath.Dir(p) == p {
			return false
		}
	}
}

// readOnlyCommand reports whether a shell call is one read-only command
// whose every argument that could be a path stays inside the working
// directory.
func readOnlyCommand(call grant.Call) bool {
	if outside, _ := call.Input["dangerouslyDisableSandbox"].(bool); outside {
		return false
	}
	command, _ := call.Input["command"].(string)
	if strings.ContainsAny(command, ";&|`$<>()\n\r\\'\"*?{}[]!") {
		return false
	}
	words := strings.Fields(command)
	if len(words) == 0 {
		return false
	}
	subs, ok := readOnlyCommands[words[0]]
	if !ok {
		return false
	}
	args := words[1:]
	if subs != nil {
		if len(args) == 0 || !slices.Contains(subs, args[0]) {
			return false
		}
		args = args[1:]
		// git can be pointed elsewhere, or made to run a program, by its
		// options.
		for _, a := range args {
			if strings.HasPrefix(a, "--output") || strings.HasPrefix(a, "--ext-diff") || strings.HasPrefix(a, "-c") ||
				strings.HasPrefix(a, "--git-dir") || strings.HasPrefix(a, "--work-tree") || a == "-C" {
				return false
			}
		}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			if _, value, ok := strings.Cut(a, "="); ok && !within(call.Cwd, value) {
				return false
			}
			continue
		}
		if !within(call.Cwd, a) {
			return false
		}
	}
	return call.Cwd != "" && filepath.IsAbs(call.Cwd)
}
