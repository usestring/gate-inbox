package search

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Locator turns a board row's tool and conversation id into a Target, and
// remembers the answer: a transcript never moves once found, and a miss is
// worth not repeating on every pass.
type Locator struct {
	// ClaudeHome is ~/.claude; its projects/ tree holds the JSONL transcripts.
	ClaudeHome string
	// CodexRoot is ~/.codex/sessions; rollouts sit in date directories under it.
	CodexRoot string

	found   map[string]string
	missing map[string]time.Time
	now     func() time.Time
}

// missTTL is how long a conversation that had no file is not looked for
// again. A session that just launched writes its transcript within seconds.
const missTTL = 20 * time.Second

// NewLocator resolves against the given roots. Either may be empty, which
// makes that tool's rows unresolvable rather than an error.
func NewLocator(claudeHome, codexRoot string) *Locator {
	return &Locator{ClaudeHome: claudeHome, CodexRoot: codexRoot,
		found: map[string]string{}, missing: map[string]time.Time{}, now: time.Now}
}

// Target resolves one row. ok is false when the row has no conversation the
// index can read: no id yet, an unknown tool, or a file not yet on disk.
func (l *Locator) Target(key, tool, cwd, agentID string) (Target, bool) {
	if key == "" || agentID == "" {
		return Target{}, false
	}
	if tool == ToolOpenCode {
		return Target{Key: key, Tool: tool, AgentID: agentID}, true
	}
	cacheKey := tool + "\x00" + agentID
	if path, ok := l.found[cacheKey]; ok {
		return Target{Key: key, Tool: tool, Path: path, AgentID: agentID}, true
	}
	if at, ok := l.missing[cacheKey]; ok && l.now().Sub(at) < missTTL {
		return Target{}, false
	}
	path := ""
	switch tool {
	case ToolClaude:
		path = l.claudePath(cwd, agentID)
	case ToolCodex:
		path = l.codexPath(agentID)
	}
	if path == "" {
		l.missing[cacheKey] = l.now()
		return Target{}, false
	}
	delete(l.missing, cacheKey)
	l.found[cacheKey] = path
	return Target{Key: key, Tool: tool, Path: path, AgentID: agentID}, true
}

// claudePath is the transcript under the cwd's project directory, or under
// any project directory when the session was launched somewhere else and
// moved: the file's base name is the session id either way.
func (l *Locator) claudePath(cwd, id string) string {
	if l.ClaudeHome == "" || strings.ContainsAny(id, `/\`) {
		return ""
	}
	projects := filepath.Join(l.ClaudeHome, "projects")
	if cwd != "" {
		direct := filepath.Join(projects, projectDir(cwd), id+".jsonl")
		if _, err := os.Stat(direct); err == nil {
			return direct
		}
	}
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return ""
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		candidate := filepath.Join(projects, dir.Name(), id+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// codexPath finds the rollout whose name ends in the conversation id. Newest
// date directories are walked first so a live session is found in the
// first few entries of a tree that keeps months of rollouts.
func (l *Locator) codexPath(id string) string {
	if l.CodexRoot == "" || strings.ContainsAny(id, `/\`) {
		return ""
	}
	suffix := "-" + id + ".jsonl"
	found := ""
	_ = walkNewestFirst(l.CodexRoot, func(path string, d fs.DirEntry) bool {
		if !d.IsDir() && strings.HasSuffix(d.Name(), suffix) {
			found = path
			return false
		}
		return true
	})
	return found
}

// walkNewestFirst visits a tree of date-named directories in reverse name
// order, stopping when visit returns false.
func walkNewestFirst(root string, visit func(path string, d fs.DirEntry) bool) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		path := filepath.Join(root, entry.Name())
		if !visit(path, entry) {
			return fs.SkipAll
		}
		if entry.IsDir() {
			if err := walkNewestFirst(path, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// projectDir is Claude Code's directory name for a working directory: every
// character outside [A-Za-z0-9] becomes a dash.
func projectDir(cwd string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '-'
	}, cwd)
}
