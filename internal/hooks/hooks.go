// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package hooks wires Claude Code hook events into status files: each
// managed session gets GATE_INBOX_STATUS_FILE in its environment, and
// the generated settings file makes Claude Code write its lifecycle state
// there. The poller reads these files as a tier-1 status source, ahead of
// the pane-regex heuristics.
package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tracing"
)

const EnvStatusFile = envname.StatusFile

// EnvSessionID identifies the managed session to the rename subcommand;
// every session gets it regardless of tool.
const EnvSessionID = envname.SessionID

// EnvExecutable is where a session finds the manager to run its
// subcommands, because the name alone does not find it: the manager is
// normally run from its own checkout (`go run .`, or a build sitting in it)
// and is not installed on anyone's PATH. A directive that told an agent to
// type the bare name got "command not found", the agent reported the rename
// it believed it had done, and no session on the board had ever carried a
// name its own agent chose.
const EnvExecutable = envname.Executable

// StatusSourceClaude is the status_source config value that enables this
// package for a tool.
const StatusSourceClaude = "claude-hooks"

const settingsName = "claude-settings.json"

type Manager struct {
	dir string
	// root is the config directory itself.
	root string
}

func NewManager(configDir string) *Manager {
	return &Manager{dir: filepath.Join(configDir, "hooks"), root: configDir}
}

// Dir exposes the hooks directory for other generated per-session
// artifacts, like MCP registration configs.
func (h *Manager) Dir() string {
	return h.dir
}

// ConfigDir is the directory this manager was built on, which is what the
// session subcommands are constructed from. The TUI holds a hooks manager
// rather than the path it came from, and what it does through those
// subcommands needs the path reachable from here.
func (h *Manager) ConfigDir() string {
	return h.root
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookMatcher struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

type settingsFile struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

// statusCommand always exits 0 and no-ops outside managed sessions, so
// the settings file is harmless if Claude Code loads it elsewhere.
func statusCommand(state string) string {
	return statusFileVar + `[ -z "$f" ] || printf ` + state + ` > "$f"`
}

// statusFileVar opens every hook command by reading the status file's path
// into $f.
var statusFileVar = `f="$` + EnvStatusFile + `"; `

// blockingTool is the tool whose call is not work but a question: Claude
// Code runs AskUserQuestion by drawing a dialog and waiting for a person,
// so its PreToolUse means waiting, not working, and no later event arrives
// until the answer. There is no separate hook for it and matchers cannot
// exclude a tool from "*", while two matchers that both hit run in parallel
// and would race on the file. So one command covers every tool and reads
// the event on stdin to tell them apart, which is what the hook docs
// prescribe for an exclusion.
const blockingTool = "AskUserQuestion"

// preToolUseCommand reads the hook payload on stdin to tell the two apart.
// An unreadable or unexpected payload falls through to working, which the
// next event corrects.
func preToolUseCommand() string {
	return statusFileVar + `[ -z "$f" ] || { if grep -q '"tool_name"[[:space:]]*:[[:space:]]*"` + blockingTool +
		`"'; then printf ` + status.Waiting + `; else printf ` + status.Working + `; fi > "$f"; }`
}

// blockingNotifications are the Notification types that leave the turn
// stuck on the user. The event also fires for the idle reminder, for
// authentication and for background agents finishing, none of which
// block, so the matcher names the two that do.
const blockingNotifications = "permission_prompt|elicitation_dialog"

// limitStopFailures are the StopFailure error types that mean the account
// hit a usage or rate limit. The pane also classifies those as errored;
// the hook write covers the turn before the banner is visible.
const limitStopFailures = "rate_limit"

// sessionEndCommand clears the status file and prunes the repository's
// stale worktree records. A session that spawns per-task worktrees leaves
// an admin record behind whenever its checkout is deleted without a
// `git worktree remove`, and nothing else reaps them: on this fleet 81 of
// 204 records in one repository were already dead. That matters beyond
// tidiness, because Claude Code turns every registered worktree into
// filesystem deny paths on the sandbox profile it passes as one argv
// string. Enough of them and the profile crosses the kernel's 128KB
// single-argument limit, and every sandboxed command in every session
// dies with E2BIG before the shell starts.
//
// Pruning only drops records whose gitdir file is already gone, so it
// never touches a live worktree or anything uncommitted. Failures are
// swallowed and the command always exits 0: teardown must not block on a
// repository that moved, and a session ending outside a git tree is
// normal, not an error.
func sessionEndCommand() string {
	return statusFileVar + `[ -z "$f" ] || rm -f "$f"; ` +
		`git rev-parse --git-dir >/dev/null 2>&1 && { git worktree prune >/dev/null 2>&1; ` +
		`git submodule --quiet foreach --recursive 'git worktree prune >/dev/null 2>&1 || true' >/dev/null 2>&1; }; ` +
		`exit 0`
}

func settingsContent() ([]byte, error) {
	run := func(matcher, command string) []hookMatcher {
		return []hookMatcher{{Matcher: matcher, Hooks: []hookCommand{{Type: "command", Command: command}}}}
	}
	report := func(matcher, state string) []hookMatcher {
		return run(matcher, statusCommand(state))
	}
	content := settingsFile{Hooks: map[string][]hookMatcher{
		"UserPromptSubmit": report("", status.Working),
		"PreToolUse":       run("*", preToolUseCommand()),
		"PostToolUse":      report("*", status.Working),
		"Notification":     report(blockingNotifications, status.Waiting),
		"Stop":             report("", status.Finished),
		"StopFailure":      report(limitStopFailures, status.Errored),
		// compact fires SessionStart in the middle of an active turn
		"SessionStart": report("startup|resume|clear", status.Idle),
		"SessionEnd": {{Hooks: []hookCommand{{
			Type:    "command",
			Command: sessionEndCommand(),
		}}}},
	}}
	return json.MarshalIndent(content, "", "  ")
}

// readMailbox reads one of the per-session files, and reports the read.
//
// A single read here was measured at under ten microseconds, so the span is
// not here because one of them is slow. It is here because the poller does
// three per session on every pass and nothing in the program says so out loud;
// a trace that carries them turns "the hook files are cheap" from a belief
// into a number, and turns a filesystem having a bad minute into something
// visible rather than a pass that is inexplicably late.
//
// The file's contents never go on the span. They are a status, a name or a
// tier -- small things, but a name is written by an agent and a trace leaves
// the machine, so what is reported is that the file was there and how long
// reading it took.
//
// A missing file is not the span's error. Most of these reads miss by design:
// a mailbox exists only between a session writing it and the next pass
// consuming it, so marking the misses as failures would paint the whole trace
// red for a board doing exactly what it should.
func readMailbox(kind, id, path string) ([]byte, bool) {
	if !tracing.Enabled() {
		raw, err := os.ReadFile(path)
		return raw, err == nil
	}
	started := time.Now()
	raw, err := os.ReadFile(path)
	tracing.Record("hooks.read", started, time.Now(), nil,
		tracing.Attr{Key: "mailbox", Value: kind},
		tracing.Attr{Key: "session", Value: id},
		tracing.Attr{Key: "found", Value: err == nil})
	if err != nil {
		return nil, false
	}
	return raw, true
}

// recordWrite reports one of this package's writes, which are rarer than the
// reads and cost more: each is a directory create and a file write, and the
// settings file is a read and a compare before either.
//
// A write with no session is the settings file, which is the whole fleet's and
// belongs to nobody; it carries no session attribute rather than an empty one,
// since an attribute that is always blank is a column of nothing to query.
func recordWrite(name string, started time.Time, id string, err error) {
	var attrs []tracing.Attr
	if id != "" {
		attrs = append(attrs, tracing.Attr{Key: "session", Value: id})
	}
	tracing.Record(name, started, time.Now(), err, attrs...)
}

// EnsureSettings writes the hook settings file, refreshing it when the
// wanted content changed (e.g. after an upgrade), and returns its path.
func (m *Manager) EnsureSettings() (settings string, err error) {
	// Every launch waits on this, and a session cannot report its own status
	// until the file it writes through exists.
	if tracing.Enabled() {
		started := time.Now()
		defer func() { recordWrite("hooks.settings", started, "", err) }()
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return "", err
	}
	wanted, err := settingsContent()
	if err != nil {
		return "", err
	}
	path := filepath.Join(m.dir, settingsName)
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, wanted) {
		return path, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.WriteFile(path, wanted, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// SettingsPath is where EnsureSettings would write, without writing it. A dry
// run needs the path the command line will carry; ensuring the file is a side
// effect it must not have.
func (m *Manager) SettingsPath() string {
	return filepath.Join(m.dir, settingsName)
}

// SettingsArgv is how the settings file appears on a launched session's
// command line, which is the only evidence that a running process is wired to
// these hooks at all. Nothing in the default settings chain writes a status
// file -- not the operator's ~/.claude/settings.json, not a project's
// .claude/settings.json -- so a claude process without this on its argv will
// never write one, however healthy it is otherwise.
//
// It is built here rather than spelled out by the caller so it cannot drift
// from what launch actually appends. launch.Environment composes the same two
// pieces; a copy of this string that fell behind a flag rename would report
// the whole board unwired.
func SettingsArgv(path string) string {
	return "--settings " + path
}

func (m *Manager) StatusFile(id string) string {
	return filepath.Join(m.dir, id+".status")
}

// Read returns the hook-reported status for a session. The file is
// written by shell hooks, so anything but a known status is rejected.
func (m *Manager) Read(id string) (string, bool) {
	raw, ok := readMailbox("status", id, m.StatusFile(id))
	if !ok {
		return "", false
	}
	state := strings.TrimSpace(string(raw))
	switch state {
	case status.Working, status.Waiting, status.Finished, status.Idle, status.Errored:
		return state, true
	}
	return "", false
}

// Write puts a status into a session's hook file, as the session's own hooks
// would have written it.
//
// The directory is created here: a session written before any hook has run
// would otherwise have nowhere to write.
func (m *Manager) Write(id, state string) (err error) {
	if tracing.Enabled() {
		started := time.Now()
		defer func() { recordWrite("hooks.write", started, id, err) }()
	}
	switch state {
	case status.Working, status.Waiting, status.Finished, status.Idle, status.Errored:
	default:
		return errors.New("not a status: " + state)
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(m.StatusFile(id), []byte(state), 0o644)
}

func (m *Manager) Remove(id string) error {
	return removeIfExists(m.StatusFile(id))
}

// NameFile is the mailbox the rename subcommand writes a session's
// self-chosen name into; the poller applies and deletes it.
func (m *Manager) NameFile(id string) string {
	return filepath.Join(m.dir, id+".name")
}

const maxNameLength = 80

// ReadName returns the pending rename for a session. found reports that
// the file exists, so the caller can consume it even when the content
// normalizes to nothing. The file is written by agents, so the name is
// squashed to one bounded line.
func (m *Manager) ReadName(id string) (name string, found bool) {
	raw, ok := readMailbox("name", id, m.NameFile(id))
	if !ok {
		return "", false
	}
	name = strings.Join(strings.Fields(string(raw)), " ")
	if runes := []rune(name); len(runes) > maxNameLength {
		name = string(runes[:maxNameLength])
	}
	return name, true
}

func (m *Manager) RemoveName(id string) error {
	return removeIfExists(m.NameFile(id))
}

// PriorityFile is where a session leaves the tier it has declared for its
// own work, for the manager to apply on its next poll.
//
// A mailbox rather than a direct write for the same reason a rename is one:
// the manager is the only writer of the database, and a session that could
// reach the row itself would be a second one. The file is one word, so a
// truncated write cannot mean a different tier.
func (m *Manager) PriorityFile(id string) string {
	return filepath.Join(m.dir, id+".priority")
}

// ReadPriority returns the tier a session has declared. found reports that
// the file exists, so the caller consumes it even when the content is not a
// tier at all -- a session that wrote nonsense must not have it retried on
// every poll for the rest of the session.
func (m *Manager) ReadPriority(id string) (tier string, found bool) {
	raw, ok := readMailbox("priority", id, m.PriorityFile(id))
	if !ok {
		return "", false
	}
	return strings.TrimSpace(string(raw)), true
}

func (m *Manager) RemovePriority(id string) error {
	return removeIfExists(m.PriorityFile(id))
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
