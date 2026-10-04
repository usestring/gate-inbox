// Package envname owns the names of the environment variables Gate Inbox
// sets for the sessions it launches and reads for its own settings.
package envname

import (
	"os"
	"strings"
)

const (
	// SessionID identifies the managed session a process runs in.
	SessionID = "GATE_INBOX_SESSION_ID"
	// Executable is the manager binary a session runs its subcommands with.
	Executable = "GATE_INBOX_BIN"
	// StatusFile is where a managed Claude Code session's hooks write state.
	StatusFile = "GATE_INBOX_STATUS_FILE"
	// ExitFile is where a managed pane's launch script records how its
	// agent exited.
	ExitFile = "GATE_INBOX_EXIT_FILE"
	// AgentPID is the pid of the adopted claude a global hook runs for, which
	// the hook's prelude has already matched against the pane's marker.
	AgentPID = "GATE_INBOX_AGENT_PID"
	// Editor overrides the editor Settings opens the snippets file in.
	Editor = "GATE_INBOX_EDITOR"
	// Golden re-records the golden files when set to "write".
	Golden = "GATE_INBOX_GOLDEN"
)

// Get reads a variable, treating a blank value as unset.
func Get(name string) string {
	if value := os.Getenv(name); strings.TrimSpace(value) != "" {
		return value
	}
	return ""
}
