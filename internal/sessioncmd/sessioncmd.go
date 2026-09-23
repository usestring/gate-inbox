// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package sessioncmd implements the session-scoped commands an agent uses
// to talk to its running manager: naming the session and operating managed
// terminals. The CLI subcommands and the MCP server share this layer so
// validation and behavior stay identical.
package sessioncmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/priority"
)

var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]+$`)

func validSession(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("not inside a Gate Inbox session (%s is unset)", hooks.EnvSessionID)
	}
	if !sessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("invalid session id %q", sessionID)
	}
	return nil
}

func writeMailbox(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return refusedWrite(err)
	}
	return refusedWrite(os.WriteFile(path, []byte(content), 0o644))
}

// refusedWrite names the usual reason a session cannot write the manager's
// state directory. Claude Code runs a session's shell commands in a sandbox
// whose writable set does not include the config directory, so the CLI
// subcommand fails with "read-only file system" while the manager's MCP
// server, a child of the agent rather than of its shell, writes the same
// file without trouble. The bare error reads as the manager refusing, so
// the message has to say which door is open.
func refusedWrite(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EROFS) || errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w (a sandboxed shell cannot write the manager's state directory: "+
			"use the Gate Inbox MCP tool instead, or run this command outside the sandbox)", err)
	}
	return err
}

// Rename records a session's self-chosen name for the running manager to
// apply on its next poll. It only writes the name file; the manager owns
// the database and the tmux label.
func Rename(configDir, sessionID, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name is empty")
	}
	if err := validSession(sessionID); err != nil {
		return "", err
	}
	if err := writeMailbox(hooks.NewManager(configDir).NameFile(sessionID), name); err != nil {
		return "", err
	}
	return "session renamed to " + name, nil
}

// Priority records the tier a session declares for its own work, for the
// running manager to apply on its next poll.
//
// A session declares rather than the operator marking because the session
// is often the only thing that knows: a goal tick launches with a scoped
// prompt naming no ticket, and a fan-out child launches with a bare
// instruction, so nothing the manager can read from outside says whether
// this pane is the release or a side experiment.
//
// It sets the session's own tier, which is not necessarily the one it will
// triage at: a group above it may state a higher one, and the higher wins.
func Priority(configDir, sessionID, tier string) (string, error) {
	parsed, ok := priority.Parse(tier)
	if !ok {
		return "", fmt.Errorf("unknown priority %q (want one of: urgent, high, medium, low, none)", tier)
	}
	if err := validSession(sessionID); err != nil {
		return "", err
	}
	if err := writeMailbox(hooks.NewManager(configDir).PriorityFile(sessionID), string(parsed)); err != nil {
		return "", err
	}
	if parsed == priority.Unset {
		return "session priority cleared", nil
	}
	return "session priority set to " + parsed.Label(), nil
}
