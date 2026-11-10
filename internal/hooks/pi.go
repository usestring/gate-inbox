package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

// StatusSourcePi is the status_source config value for a pi session whose
// status comes from the board's pi extension (package piext): the extension
// reports pi's lifecycle events to `gate-inbox hook pi <event>`, which writes
// the same status file Claude Code's hooks do.
const StatusSourcePi = "pi-hooks"

// FileStatus reports whether a status_source writes the session's status
// file, so that the poller reads it as a tier-1 source ahead of the pane.
// Claude-only wiring (the --settings file, the plugin, argv checks) still
// asks for StatusSourceClaude by name.
func FileStatus(source string) bool {
	return source == StatusSourceClaude || source == StatusSourcePi
}

// PiPayload is what the pi extension sends for one lifecycle event. Each
// event fills only the fields it has.
type PiPayload struct {
	Event string `json:"event"`
	// Reason is session_start's and session_shutdown's reason.
	Reason string `json:"reason,omitempty"`
	// Errored says an agent_end's last assistant message stopped on an error.
	Errored bool `json:"errored,omitempty"`
	// Idle says pi had no run going when a ui_prompt_end fired.
	Idle bool   `json:"idle,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
}

// PiPermissionSuffix names the file beside a pi session's status file that
// holds the call its permission dialog is asking about, exactly as the call
// gave it, while the dialog is up: a long command the pane wraps reads back
// from it whole (dialog.Screen.WithExact).
const PiPermissionSuffix = ".permission.json"

// PiApprovalsSuffix names the file beside a pi session's status file that
// holds the rules a person approved at its dialog with "don't ask again". It
// lasts as long as the session.
const PiApprovalsSuffix = ".pi-approvals.json"

// PiPermissionFile is where id's pending permission call is kept.
func (m *Manager) PiPermissionFile(id string) string { return m.StatusFile(id) + PiPermissionSuffix }

// PiPermission is the call a pi session's permission dialog asks about.
type PiPermission struct {
	Tool       string `json:"tool"`
	Title      string `json:"title"`
	Block      string `json:"block"`
	Cwd        string `json:"cwd,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Suggestion string `json:"ruleSuggestion,omitempty"`
	ToolUseID  string `json:"tool_use_id,omitempty"`
}

// ReadPiPermission is the call id's dialog is asking about, if one is up.
func (m *Manager) ReadPiPermission(id string) (PiPermission, bool) {
	return ReadPiPermissionFile(m.PiPermissionFile(id))
}

// ReadPiPermissionFile is the call a pending-permission file holds, if any.
func ReadPiPermissionFile(path string) (PiPermission, bool) {
	if path == "" {
		return PiPermission{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return PiPermission{}, false
	}
	var p PiPermission
	if json.Unmarshal(raw, &p) != nil || p.Block == "" {
		return PiPermission{}, false
	}
	return p, true
}

// piErroredEvent is the event an errored line is logged under, which is
// what agent_settled looks for to leave it standing.
const piErroredEvent = "agent_end"

// PiStatus maps one pi lifecycle event to the line it appends to the status
// file. remove says the file is to go, which is pi quitting; a session
// replaced in place (/new, /resume, /fork, /reload) gets a session_start
// next, so its shutdown leaves the file. An empty state with remove false
// writes nothing.
//
// last is the status file's last state and event, so that agent_settled can
// keep an error standing: pi settles after every run, the failed ones too,
// and a finished written over the errored would hide the failure. A retry
// that follows the error starts a new run, whose agent_start says working.
func PiStatus(p PiPayload, lastState, lastEvent string) (state string, remove bool) {
	switch p.Event {
	case "session_start":
		return status.Idle, false
	case "agent_start":
		return status.Working, false
	case "agent_end":
		if p.Errored {
			return status.Errored, false
		}
		return "", false
	case "agent_settled":
		if lastState == status.Errored && lastEvent == piErroredEvent {
			return "", false
		}
		return status.Finished, false
	case "ui_prompt_start":
		return status.Waiting, false
	case "ui_prompt_end":
		// A dialog an extension command raised from the prompt closes onto
		// a resting pane, not a run.
		if p.Idle {
			return status.Idle, false
		}
		return status.Working, false
	case "session_shutdown":
		return "", p.Reason == "quit"
	}
	return "", false
}

// DispatchPi handles `gate-inbox hook pi <event>` for the session whose status
// file is statusFile (GATE_INBOX_STATUS_FILE). The event name on the command
// line is the one acted on; a payload naming another is refused. It answers
// the JSON the extension reads back, empty for a status event, which has
// nothing to say.
func DispatchPi(event string, in io.Reader, statusFile string) string {
	if statusFile == "" {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil {
		return ""
	}
	var p PiPayload
	if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &p) != nil {
		return ""
	}
	if p.Event != "" && p.Event != event {
		return ""
	}
	p.Event = event
	lastState, lastEvent := lastLine(statusFile)
	state, remove := PiStatus(p, lastState, lastEvent)
	if remove {
		_ = removeIfExists(statusFile)
		_ = removeIfExists(statusFile + AttestPendingSuffix)
		_ = removeIfExists(statusFile + SandboxStaleSuffix)
		_ = removeIfExists(statusFile + PiPermissionSuffix)
		_ = removeIfExists(statusFile + PiApprovalsSuffix)
		pruneWorktrees(p.Cwd)
		return ""
	}
	if state != "" {
		_ = appendLine(statusFile, state+" "+event)
	}
	return ""
}

func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// lastLineBytes bounds what lastLine reads: the log grows by a line per
// event for the life of the session, and only its last line matters.
const lastLineBytes = 512

// lastLine reads the state and event of the status file's last line.
func lastLine(path string) (state, event string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > lastLineBytes {
		if _, err := f.Seek(-lastLineBytes, io.SeekEnd); err != nil {
			return "", ""
		}
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return "", ""
	}
	raw = bytes.TrimRight(raw, "\n")
	if i := bytes.LastIndexByte(raw, '\n'); i >= 0 {
		raw = raw[i+1:]
	}
	s, e, _ := bytes.Cut(raw, []byte(" "))
	return string(s), string(e)
}

// pruneWorktrees is the Claude SessionEnd hook's teardown (sessionEndCommand)
// for a pi session: it drops the admin records of worktrees whose checkout
// is already gone, in the repository and its submodules. It is bounded, and
// every failure is swallowed, since a session ending outside a git tree is
// normal.
func pruneWorktrees(cwd string) {
	if cwd == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = cwd
		return cmd.Run()
	}
	if git("rev-parse", "--git-dir") != nil {
		return
	}
	_ = git("worktree", "prune")
	_ = git("submodule", "--quiet", "foreach", "--recursive", "git worktree prune >/dev/null 2>&1 || true")
}
