// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

// Package hooks wires Claude Code hook events into status files: each
// managed session gets GATE_INBOX_STATUS_FILE in its environment, and
// the generated settings file makes Claude Code write its lifecycle state
// there. The poller reads these files as a tier-1 status source, ahead of
// the pane-regex heuristics.
package hooks

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/envname"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tracing"
)

const EnvStatusFile = envname.StatusFile

// EnvExitFile names the file the launch script writes the agent's exit
// status to; see ExitFile.
const EnvExitFile = envname.ExitFile

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
//
// It appends rather than overwrites. The poller reads the file every couple
// of seconds, and a single overwritten word lost every event between two
// reads: a whole turn that started and ended inside one interval left the
// file saying "finished" exactly as before, so the turn was never seen and
// its alert never raised. A log keeps them, and an append of one short line
// is atomic, where a truncate-then-write left an empty file for a reader to
// find. See Events.
func statusCommand(state, event string) string {
	return statusFileVar + `[ -z "$f" ] || ` + appendEvent(state, event)
}

// appendEvent is the shell that writes one event line, the unit every hook
// command is built from.
func appendEvent(state, event string) string {
	return `printf '` + state + ` ` + event + `\n' >> "$f"`
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
		`"'; then ` + appendEvent(status.Waiting, "PreToolUse") + `; else ` + appendEvent(status.Working, "PreToolUse") + `; fi; }`
}

// blockingNotifications are the Notification types that leave the turn
// stuck on the user. The event also fires for the idle reminder, for
// authentication and for background agents finishing, none of which
// block, so the matcher names the two that do.
const blockingNotifications = "permission_prompt|elicitation_dialog"

// limitStopFailure is the StopFailure error type that means the account
// hit a usage or rate limit. The pane also classifies those as errored;
// the hook write covers the turn before the banner is visible.
const limitStopFailure = "rate_limit"

// stopFailureCommand reports how a turn that died on an API error ended.
//
// Every error type ends the turn, and Stop does not fire for any of them, so
// a hook that only listened for the limit left every other failure -- an
// overloaded API, a server error, a request the API refused -- reading
// working from the last tool call until something else happened, which
// nothing would. A limit is errored, because recovering from it is the
// board's job; any other failure is a turn that ended and needs a person,
// which is what finished says. One command reads the type off the payload,
// for the reason preToolUseCommand does: two matchers on one event run in
// parallel, and their lines would land in either order.
func stopFailureCommand() string {
	return statusFileVar + `[ -z "$f" ] || { if grep -q '"` + limitStopFailure + `"'; then ` +
		appendEvent(status.Errored, "StopFailure") + `; else ` + appendEvent(status.Finished, "StopFailure") + `; fi; }`
}

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
	report := func(event, matcher, state string) []hookMatcher {
		return run(matcher, statusCommand(state, event))
	}
	content := settingsFile{Hooks: map[string][]hookMatcher{
		"UserPromptSubmit": report("UserPromptSubmit", "", status.Working),
		"PreToolUse":       run("*", preToolUseCommand()),
		"PostToolUse":      report("PostToolUse", "*", status.Working),
		"Notification":     report("Notification", blockingNotifications, status.Waiting),
		"Stop":             report("Stop", "", status.Finished),
		"StopFailure":      run("", stopFailureCommand()),
		// compact fires SessionStart in the middle of an active turn
		"SessionStart": report("SessionStart", "startup|resume|clear", status.Idle),
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

// InstallStatusFile is the mailbox an install started from the setup
// dialog writes the command's exit status to.
func (m *Manager) InstallStatusFile(id string) string {
	return filepath.Join(m.dir, id+".install")
}

// WriteInstallScript stores the script that install runs, so the shell in
// the pane is typed one short command instead of a quoted line the user's
// shell may read differently from sh.
func (m *Manager) WriteInstallScript(id, body string) (string, error) {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(m.dir, id+".install.sh")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

// Event is one line of a session's hook log: the status the hook reported
// and the hook event that reported it.
type Event struct {
	State string
	Name  string
}

// parseEvent reads one log line. The file is written by shell hooks, so
// anything but a known status is rejected. A line with no event name is the
// single word this file held before it became a log, which a session
// launched on the old settings goes on writing until it restarts.
func parseEvent(line string) (Event, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || len(fields) > 2 {
		return Event{}, false
	}
	switch fields[0] {
	case status.Working, status.Waiting, status.Finished, status.Idle, status.Errored:
	default:
		return Event{}, false
	}
	ev := Event{State: fields[0]}
	if len(fields) == 2 {
		ev.Name = fields[1]
	}
	return ev, true
}

// statusTailBytes bounds what Read looks at. The newest event is the last
// line, and a line is a status and an event name, so this is several lines
// of slack rather than a guess at one.
const statusTailBytes = 256

// Read returns the hook-reported status for a session: the newest event in
// its log.
//
// Only the tail is read. The log grows by a line per tool call for as long as
// the session runs, and this runs on every poll for every session.
func (m *Manager) Read(id string) (string, bool) {
	raw, start, ok := readLog(id, m.StatusFile(id), func(size int64) int64 { return size - statusTailBytes })
	if !ok {
		return "", false
	}
	if start > 0 {
		// The first line is cut short; drop it.
		if i := bytes.IndexByte(raw, '\n'); i >= 0 {
			raw = raw[i+1:]
		}
	}
	lines := strings.Split(string(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		ev, ok := parseEvent(lines[i])
		if !ok {
			// The newest line decides. An unreadable one is a write this
			// package does not recognise, and reaching past it would report
			// an event that is no longer the newest.
			return "", false
		}
		return ev.State, true
	}
	return "", false
}

// Events returns the events a session's hooks have logged since offset, and
// the offset to pass next time. ok is false when there is no log.
//
// This is the part of the log a single status cannot carry: what happened
// between two looks. A turn that started and ended between two polls leaves
// the newest line where it was, and only the lines in between say it ran.
//
// A log shorter than offset is not this session's log as it was; the file
// was removed and written again. Every line in it is new.
func (m *Manager) Events(id string, offset int64) (events []Event, next int64, ok bool) {
	raw, start, found := readLog(id, m.StatusFile(id), func(size int64) int64 {
		if offset > size {
			return 0
		}
		return offset
	})
	if !found {
		return nil, 0, false
	}
	// A line still being written has no newline yet; it is read next time.
	end := bytes.LastIndexByte(raw, '\n') + 1
	for _, line := range strings.Split(string(raw[:end]), "\n") {
		if ev, ok := parseEvent(line); ok {
			events = append(events, ev)
		}
	}
	return events, start + int64(end), true
}

// readLog reads a status log from the offset from picks, given the file's
// size, to its end, and returns the bytes and the offset they start at.
func readLog(id, path string, from func(size int64) int64) (raw []byte, start int64, ok bool) {
	started := time.Now()
	raw, start, err := func() ([]byte, int64, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, 0, err
		}
		start := max(from(info.Size()), 0)
		buf := make([]byte, info.Size()-start)
		n, err := f.ReadAt(buf, start)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, err
		}
		return buf[:n], start, nil
	}()
	if tracing.Enabled() {
		tracing.Record("hooks.read", started, time.Now(), nil,
			tracing.Attr{Key: "mailbox", Value: "status"},
			tracing.Attr{Key: "session", Value: id},
			tracing.Attr{Key: "found", Value: err == nil})
	}
	return raw, start, err == nil
}

// Write logs a status as the session's own hooks would have, attributed to
// the manager rather than to a hook event.
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
	f, err := os.OpenFile(m.StatusFile(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(state + " manager\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ExitFile is where a managed pane's launch script writes its agent's exit
// status once the agent returns. It is the one record of how an agent ended
// that survives the pane: a reboot leaves none, an agent the operator quit
// leaves 0, and one that crashed leaves what it died of. Every tool gets it,
// hooks or not, because the script writes it rather than the agent.
func (m *Manager) ExitFile(id string) string {
	return filepath.Join(m.dir, id+".exit")
}

// ReadExit returns the exit status the launch script recorded and when it
// wrote it. ok is false when there is no record, or one that is not a number.
func (m *Manager) ReadExit(id string) (code int, at time.Time, ok bool) {
	path := m.ExitFile(id)
	raw, found := readMailbox("exit", id, path)
	if !found {
		return 0, time.Time{}, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, time.Time{}, false
	}
	if info, err := os.Stat(path); err == nil {
		at = info.ModTime()
	}
	return code, at, true
}

// RemoveExit drops the record, which a relaunch does so the new agent cannot
// be judged by the last one's exit.
func (m *Manager) RemoveExit(id string) error {
	return removeIfExists(m.ExitFile(id))
}

func (m *Manager) Remove(id string) error {
	return removeIfExists(m.StatusFile(id))
}

// NameFile is the mailbox the rename subcommand writes a session's
// self-chosen name into; the poller applies and deletes it. The first
// line is the id of the request, so its answer comes back to the caller
// that asked and not to another rename for the same session.
func (m *Manager) NameFile(id string) string {
	return filepath.Join(m.dir, id+".name")
}

// NewRequestID names one rename, keeping its answer apart from the answer
// to a rename another caller queued for the same session.
func NewRequestID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// NameRequest is one queued rename: the id its answer comes back under,
// and the name the session is asked to take.
func NameRequest(request, name string) string {
	return request + "\n" + name
}

const maxNameLength = 80

// ReadName returns the pending rename for a session: the name it asks
// for, without the request id a queued rename leads with. found reports
// that the file exists, so the caller can consume it even when the
// content normalizes to nothing.
func (m *Manager) ReadName(id string) (name string, found bool) {
	raw, ok := readMailbox("name", id, m.NameFile(id))
	if !ok {
		return "", false
	}
	_, name = parseNameRequest(string(raw))
	return name, true
}

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// parseNameRequest splits a queued rename into the request its answer
// comes back under and the name asked for. The file is written by agents,
// so a first line that is not a request this package issued is no request
// at all: the whole file is the name, and the rename is applied with
// nobody to answer rather than letting that line reach a file path.
func parseNameRequest(raw string) (request, name string) {
	first, rest, split := strings.Cut(raw, "\n")
	if !split || !requestIDPattern.MatchString(first) {
		return "", NormalizeName(raw)
	}
	return first, NormalizeName(rest)
}

// NormalizeName is the name a session actually takes: written by agents,
// so squashed to one bounded line. Whoever asks for a rename normalizes
// its own name the same way to recognize the answer it gets back.
func NormalizeName(raw string) string {
	name := strings.Join(strings.Fields(raw), " ")
	if runes := []rune(name); len(runes) > maxNameLength {
		name = string(runes[:maxNameLength])
	}
	return name
}

func (m *Manager) RemoveName(id string) error {
	if err := removeIfExists(m.claimedNameFile(id)); err != nil {
		return err
	}
	return removeIfExists(m.NameFile(id))
}

// NameResultLifetime is how long an answer nobody claimed is kept. It is
// far longer than any caller waits, so a sweep never takes an answer out
// from under one still reading for it.
const NameResultLifetime = 5 * time.Minute

// SweepNameResults drops answers nobody came back for, including those of
// sessions that have since been deleted, whose ids never come round again.
func (m *Manager) SweepNameResults(now time.Time) error {
	paths, err := filepath.Glob(filepath.Join(m.dir, "*.renamed"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		if now.Sub(info.ModTime()) < NameResultLifetime {
			continue
		}
		if err := removeIfExists(path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) claimedNameFile(id string) string {
	return filepath.Join(m.dir, id+".name.claimed")
}

// ClaimName takes a pending rename for the manager to apply: the file is
// moved aside in one step, so a rename written while this one is being
// applied lands on a free mailbox and waits for the next poll instead of
// being consumed with it. A claim left by a manager that stopped midway
// is picked up again ahead of anything newer. ReleaseName ends the claim.
func (m *Manager) ClaimName(id string) (request, name string, found bool, err error) {
	claimed := m.claimedNameFile(id)
	raw, err := os.ReadFile(claimed)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(m.NameFile(id), claimed); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", "", false, nil
			}
			return "", "", false, err
		}
		raw, err = os.ReadFile(claimed)
	}
	if err != nil {
		return "", "", false, err
	}
	request, name = parseNameRequest(string(raw))
	return request, name, true, nil
}

// ClaimedRequest reports the rename the manager has taken to apply, so a
// caller can tell its request being worked on from one a later rename
// replaced in the mailbox before it was ever claimed.
func (m *Manager) ClaimedRequest(id string) (request string, found bool, err error) {
	raw, err := os.ReadFile(m.claimedNameFile(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	request, _ = parseNameRequest(string(raw))
	return request, true, nil
}

func (m *Manager) ReleaseName(id string) error {
	return removeIfExists(m.claimedNameFile(id))
}

// NameResultFile is where the poller reports what became of one rename,
// under a request id this package issued, so the name never reaches the
// path.
// so the subcommand that queued it can tell the agent the truth instead
// of assuming success. It is named for the request, so a second rename
// for the same session neither reads nor removes this answer. The verdict
// is "renamed" or "refused", then the name that was asked for, then the
// applied name or the reason.
func (m *Manager) NameResultFile(id, request string) string {
	return filepath.Join(m.dir, id+"."+request+".renamed")
}

// WriteNameResult answers the rename that asked for requested. The
// waiting subcommand polls for this file, so it lands whole: a
// half-written verdict would read as a rename that never happened.
func (m *Manager) WriteNameResult(id, request, requested, applied string, refusal error) error {
	content := "renamed\n" + requested + "\n" + applied
	if refusal != nil {
		content = "refused\n" + requested + "\n" + refusal.Error()
	}
	return WriteWhole(m.NameResultFile(id, request), content)
}

// NameVerdict is what became of one rename: the name it asked for, and
// either the name the session took or the reason it kept the one it had.
type NameVerdict struct {
	Requested string
	Applied   string
	Refusal   error
}

// ReadNameResult returns the poller's answer, if it has written one. A
// caller compares Requested against its own name to know whether the
// answer is the one it is waiting for. Anything but the two verdicts this
// package writes is no answer at all, while a mailbox that cannot be read
// is an error rather than silence, since silence reads as "not yet".
func (m *Manager) ReadNameResult(id, request string) (verdict NameVerdict, found bool, err error) {
	raw, err := os.ReadFile(m.NameResultFile(id, request))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return NameVerdict{}, false, nil
		}
		return NameVerdict{}, false, err
	}
	state, rest, hasName := strings.Cut(string(raw), "\n")
	requested, detail, hasDetail := strings.Cut(rest, "\n")
	if !hasName || !hasDetail || requested == "" || detail == "" {
		return NameVerdict{}, false, nil
	}
	switch state {
	case "renamed":
		return NameVerdict{Requested: requested, Applied: detail}, true, nil
	case "refused":
		return NameVerdict{Requested: requested, Refusal: errors.New(detail)}, true, nil
	}
	return NameVerdict{}, false, nil
}

func (m *Manager) RemoveNameResult(id, request string) error {
	return removeIfExists(m.NameResultFile(id, request))
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

// WriteWhole leaves the file complete or absent, never half written, so
// a reader polling for it never picks up a partial line. Each writer
// stages under a name of its own, so two of them cannot publish each
// other's content.
func WriteWhole(path, content string) (err error) {
	staging, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.part")
	if err != nil {
		return err
	}
	// The staging file is gone once it lands, so this collects the one a
	// failure left behind rather than letting it sit in the mailbox.
	defer func() {
		if leftover := removeIfExists(staging.Name()); err == nil {
			err = leftover
		}
	}()
	if _, err := staging.WriteString(content); err != nil {
		return errors.Join(err, staging.Close())
	}
	if err := staging.Close(); err != nil {
		return err
	}
	if err := os.Chmod(staging.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(staging.Name(), path)
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
