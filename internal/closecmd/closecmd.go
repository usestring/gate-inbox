// Package closecmd runs the operator's close command after Gate Inbox closes
// a session for good.
//
// Every close the board makes ends the agent by killing its tmux window and
// reaping the process tree under it, so the agent never runs its own
// session-end hooks. Whatever a project did there -- most often retiring the
// git worktrees the session created -- never happens for a session the board
// closed. The close command is where that work runs instead.
//
// Only a close counts: a kill, a stop, an archive, a child the board filed
// away. A hibernate, a restart, a migration or an account switch end the
// process but carry the conversation on, so none of them run it.
package closecmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// Why a session was closed, as the payload's reason carries it.
const (
	ReasonKilled   = "gate_inbox_killed"
	ReasonArchived = "gate_inbox_archived"
)

// LogName is the file under the log directory that the command's output is
// appended to. The command outlives the process that started it, so nothing
// is left to read its output but a file.
const LogName = "close-command.log"

// Payload is what the command reads on stdin. Its first four fields are
// shaped like Claude Code's SessionEnd hook input, so a project's existing
// session-end script can be named as the close command unchanged.
type Payload struct {
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	Reason        string `json:"reason"`
	// GateInboxSessionID is the board row; SessionID is the agent's own
	// conversation id.
	GateInboxSessionID string `json:"gate_inbox_session_id"`
	Tool               string `json:"tool"`
}

// inheritedSessionEnv are the variables that name the session a process runs
// in. The command is started by whichever process closed the session -- an
// MCP server inside another agent, or the stop worker that runs under its
// target's board id -- so it would otherwise inherit the closer's identity,
// and a script that falls back to them would act on the wrong session.
var inheritedSessionEnv = []string{
	"CLAUDE_SESSION_ID",
	"CLAUDE_CODE_SESSION_ID",
	"CODEX_THREAD_ID",
	"CODEX_SESSION_ID",
	"SESSION_ID",
	"TMUX",
	"TMUX_PANE",
	"GATE_INBOX_SESSION_ID",
}

// start launches cmd without waiting for it; tests replace it.
var start = func(cmd *exec.Cmd) error { return cmd.Start() }

// Closed runs cfg's close command for each session in sessions, detached:
// the close never waits for it, and a failure to start it is logged rather
// than returned. Shell sessions, sessions without an agent conversation id,
// and sessions whose working directory is gone are skipped.
func Closed(cfg config.Config, reason string, sessions ...store.Session) {
	command := strings.TrimSpace(cfg.CloseCommand)
	if command == "" {
		return
	}
	for _, sess := range sessions {
		if err := run(cfg, command, reason, sess); err != nil {
			logging.Warn("close command did not start", "session", sess.ID, "err", err)
		}
	}
}

func run(cfg config.Config, command, reason string, sess store.Session) error {
	if cfg.Tools[sess.Tool].Shell {
		return nil
	}
	// Without the agent's own id a session-end script falls back to the
	// identity it runs under, which is never the closed session's.
	if sess.AgentSessionID == "" {
		logging.Info("close command skipped: session has no agent conversation id", "session", sess.ID)
		return nil
	}
	if info, err := os.Stat(sess.Cwd); err != nil || !info.IsDir() {
		logging.Info("close command skipped: working directory is gone", "session", sess.ID, "cwd", sess.Cwd)
		return nil
	}
	payload, err := json.Marshal(Payload{
		SessionID:          sess.AgentSessionID,
		Cwd:                sess.Cwd,
		HookEventName:      "SessionEnd",
		Reason:             reason,
		GateInboxSessionID: sess.ID,
		Tool:               sess.Tool,
	})
	if err != nil {
		return err
	}
	stdin, err := payloadFile(payload)
	if err != nil {
		return err
	}
	defer stdin.Close()
	out, err := openLog()
	if err != nil {
		return err
	}
	defer out.Close()
	fmt.Fprintf(out, "%s close command for %s (%s, %s) in %s\n",
		time.Now().Format(time.RFC3339), sess.ID, sess.AgentSessionID, reason, sess.Cwd)

	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = sess.Cwd
	cmd.Env = append(scrubbedEnv(os.Environ()),
		"GATE_INBOX_CLOSED_SESSION_ID="+sess.ID,
		"GATE_INBOX_CLOSED_AGENT_SESSION_ID="+sess.AgentSessionID,
		"GATE_INBOX_CLOSE_REASON="+reason,
	)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, out, out
	// Its own session, so ending the pane or process that closed the
	// session does not take the command down with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := start(cmd); err != nil {
		return err
	}
	// The child holds its own descriptors; nothing here waits on it. Release
	// lets the runtime forget it, and init reaps it once this process exits.
	if cmd.Process != nil {
		return cmd.Process.Release()
	}
	return nil
}

// payloadFile is the payload as a file the command reads as its stdin. A pipe
// would need this process to stay alive to feed it; an unlinked file does not.
func payloadFile(payload []byte) (*os.File, error) {
	f, err := os.CreateTemp("", "gate-inbox-close-*.json")
	if err != nil {
		return nil, err
	}
	os.Remove(f.Name())
	if _, err := f.Write(payload); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func openLog() (*os.File, error) {
	home, err := config.Dir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, logging.DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, LogName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

func scrubbedEnv(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.Contains(inheritedSessionEnv, name) {
			kept = append(kept, entry)
		}
	}
	return kept
}
