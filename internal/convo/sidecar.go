package convo

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ClaudeSession is what a live Claude Code process says about itself: which
// process it is, which conversation it holds, and where.
type ClaudeSession struct {
	PID       int
	SessionID string
	Cwd       string
	Name      string
}

// ClaudeHome is where Claude Code keeps its state. CLAUDE_CONFIG_DIR is
// honoured because Claude Code honours it: an operator who has moved their
// configuration has moved their session files with it. Empty when neither
// the variable nor a home directory can be found.
func ClaudeHome() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// LiveClaudeSessions reads every session file under claudeHome whose
// process is still the one that wrote it.
func LiveClaudeSessions(claudeHome string) []ClaudeSession {
	if claudeHome == "" {
		return nil
	}
	ix := &Index{claude: claudeHome}
	var out []ClaudeSession
	for _, sc := range ix.liveSidecars() {
		out = append(out, ClaudeSession{PID: sc.PID, SessionID: sc.SessionID, Cwd: sc.Cwd, Name: sc.Name})
	}
	return out
}

// ClaudeSessionInTree finds the session whose process sits among pids, which
// a caller takes from one pane's process tree. A process match is an
// identity, which is what makes this exact rather than a guess.
func ClaudeSessionInTree(sessions []ClaudeSession, pids []int) (ClaudeSession, bool) {
	for _, session := range sessions {
		if slices.Contains(pids, session.PID) {
			return session, true
		}
	}
	return ClaudeSession{}, false
}
