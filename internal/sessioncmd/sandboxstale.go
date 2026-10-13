package sessioncmd

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A session whose sandbox has gone stale.
//
// Claude Code binds a set of paths into its bwrap sandbox -- every registered
// worktree's metadata, and files such as .git/config.lock that happened to
// exist -- and a path deleted after it was listed makes every sandboxed
// command fail (hooks.staleSandboxSignature) until Claude Code lists them
// again. Measured over 1,694 stale paths to 2026-10-11, half failed once and
// cleared, while one in ten kept failing for eight minutes or more, and a few
// for days. Agents met the first failure by switching to
// dangerouslyDisableSandbox and staying there, which sent every later command
// to the auto-mode classifier and its refusals to the user as approval
// questions.
//
// So the first failure gets a note to retry inside the sandbox. A second
// failure in the same session means the list is not coming back on its own:
// the child ends its turn, and the board restarts it on its own conversation,
// which lists the paths afresh.

// Flag contents: the hook has seen one failure, or more than one.
const (
	staleOnce       = "once"
	stalePersistent = "persistent"
)

// staleOnceLifetime is how long a single failure is remembered. One that came
// back only hours later is a fresh deletion, not the same stale list.
const staleOnceLifetime = 15 * time.Minute

const staleRetryNote = "Gate Inbox: that Bash call failed because a path Claude Code bound into this session's " +
	"sandbox has since been deleted (a removed worktree or lock file), not because of the command. This usually clears " +
	"on its own. Retry the same command inside the sandbox; do not switch to dangerouslyDisableSandbox, and do not ask " +
	"anyone for approval over it."

const staleChildNote = "Gate Inbox: this session's sandbox is still stale -- a path Claude Code bound when listing " +
	"them has been deleted, so every sandboxed command fails the same way. Do not retry with " +
	"dangerouslyDisableSandbox and do not ask anyone for approval over it. End your turn now with a one-line note of " +
	"the step you were on; Gate Inbox restarts this session on its own conversation, which rebuilds the sandbox, and " +
	"tells you to carry on."

const staleTopNote = "Gate Inbox: this session's sandbox is still stale -- a path Claude Code bound when listing " +
	"them has been deleted, so every sandboxed command fails the same way until the process restarts. Do not retry " +
	"each command with dangerouslyDisableSandbox; tell your user to restart this session (exit, then resume it) to " +
	"rebuild the sandbox."

// StaleSandboxResume is what a restarted child is told.
const StaleSandboxResume = "Gate Inbox restarted this session to rebuild its sandbox, which had gone stale. " +
	"Carry on with your task from where you stopped, and rerun the command that failed inside the sandbox."

// staleSandboxLine is how bwrap reports a bind source that no longer exists,
// at the start of the failed call's output.
var staleSandboxLine = regexp.MustCompile(`(?m)^bwrap: ` + staleRefusal + ` `)

// staleRefusal is bwrap's wording for a missing bind source; older builds
// print "Can't find source path". A command that only mentions it, such as
// a grep for it, is not one.
const staleRefusal = `Can't ` + staleWords

const staleWords = `(?:get type of source|find source path)`

var staleRefusalText = regexp.MustCompile(staleWords)

// staleSandboxFailure reports whether a PostToolUseFailure payload is a Bash
// call that bwrap refused for a missing bind source. A command that merely
// mentions the message, such as a grep for it that found nothing, is not one.
func staleSandboxFailure(payload []byte) bool {
	var call struct {
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
		Error string `json:"error"`
	}
	if json.Unmarshal(payload, &call) != nil {
		return false
	}
	return staleSandboxLine.MatchString(call.Error) && !staleRefusalText.MatchString(call.ToolInput.Command)
}

// SandboxStaleHook records a stale-sandbox failure in flag and returns the
// PostToolUseFailure note for it, or nothing for any other failure.
func SandboxStaleHook(configDir, sessionID, flag string, payload []byte, now time.Time) string {
	if !staleSandboxFailure(payload) {
		return ""
	}
	if restoreWorktreeSource(staleSource(payload)) {
		return failureNote(staleRestoredNote)
	}
	note := staleRetryNote
	if staleSeenBefore(flag, now) {
		_ = hooks.WriteWhole(flag, stalePersistent)
		note = staleTopNote
		if spawned(configDir, sessionID) {
			note = staleChildNote
		}
	} else {
		_ = hooks.WriteWhole(flag, staleOnce)
	}
	return failureNote(note)
}

func failureNote(note string) string {
	out, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":     "PostToolUseFailure",
		"additionalContext": note,
	}})
	if err != nil {
		return ""
	}
	return string(out)
}

// Most stale paths are a removed worktree's metadata under
// <gitdir>/worktrees/<name>/ (62 of 82 failures, 2026-10-11 22:30 to
// 2026-10-12 04:25). Putting an empty file back where bwrap expects one fixes
// the session in place, and every other session that bound the same path,
// with no restart. Git does not see the placeholder: an entry with no gitdir
// file is left out of `git worktree list`, holds no branch, and the next
// `git worktree prune` removes it. Lock files and checkout files are never
// recreated; an empty config.lock would block every git config write.

const staleRestoredNote = "Gate Inbox: that Bash call failed because a removed worktree's metadata, which Claude " +
	"Code had bound into this session's sandbox, was deleted. Gate Inbox has put an empty placeholder back, so the " +
	"sandbox works again. Retry the same command inside the sandbox now; do not switch to " +
	"dangerouslyDisableSandbox, and do not ask anyone for approval over it."

var staleSourcePath = regexp.MustCompile(`(?m)^bwrap: ` + staleRefusal + ` (.+?): No such file or directory`)

// worktreeMetadataFiles are the per-worktree files bwrap has been seen to
// bind; the worktree's directory itself is restored for any of them.
var worktreeMetadataFiles = map[string]bool{"config.worktree": true, "commondir": true, "config": true}

func staleSource(payload []byte) string {
	var call struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(payload, &call) != nil {
		return ""
	}
	match := staleSourcePath.FindStringSubmatch(call.Error)
	if match == nil {
		return ""
	}
	return match[1]
}

// restoreWorktreeSource puts back path when it is a removed worktree's
// metadata directory, or one of its known files, under a git directory that
// still exists. It reports whether path exists afterwards.
func restoreWorktreeSource(path string) bool {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	dir, file := path, ""
	if worktreeMetadataFiles[filepath.Base(path)] {
		dir, file = filepath.Dir(path), filepath.Base(path)
	}
	worktrees := filepath.Dir(dir)
	gitDir := filepath.Dir(worktrees)
	if filepath.Base(worktrees) != "worktrees" || filepath.Base(gitDir) != ".git" {
		return false
	}
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	if file != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return false
		}
		_ = f.Close()
	}
	_, err := os.Stat(path)
	return err == nil
}

func staleSeenBefore(flag string, now time.Time) bool {
	info, err := os.Stat(flag)
	return err == nil && now.Sub(info.ModTime()) < staleOnceLifetime
}

func spawned(configDir, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	path := filepath.Join(configDir, "state.db")
	if _, err := os.Stat(path); err != nil {
		return false
	}
	st, err := store.Open(path)
	if err != nil {
		return false
	}
	defer st.Close()
	sess, err := st.Get(sessionID)
	return err == nil && store.SpawnerOf(sess) != ""
}

// RestartStaleSandboxes restarts every spawned session whose sandbox stayed
// stale, once its turn has ended, and reports how many it restarted. A
// working session is left until it rests: the note tells it to end the turn,
// and killing it midway would lose the step it is on. A single failure that
// never came back is dropped once it is old.
func (s *Sessions) RestartStaleSandboxes(now time.Time) (int, error) {
	manager := hooks.NewManager(s.configDir)
	flags, err := filepath.Glob(filepath.Join(manager.Dir(), "*.status"+hooks.SandboxStaleSuffix))
	if err != nil || len(flags) == 0 {
		return 0, err
	}
	var persistent []string
	for _, flag := range flags {
		raw, err := os.ReadFile(flag)
		switch {
		case err != nil:
		case strings.TrimSpace(string(raw)) == stalePersistent:
			persistent = append(persistent, flag)
		case !staleSeenBefore(flag, now):
			_ = removeFlag(flag)
		}
	}
	if len(persistent) == 0 {
		return 0, nil
	}
	runtime, err := s.open()
	if err != nil {
		return 0, err
	}
	defer runtime.store.Close()
	restarted := 0
	var errs []error
	for _, flag := range persistent {
		id := strings.TrimSuffix(filepath.Base(flag), ".status"+hooks.SandboxStaleSuffix)
		target, err := runtime.store.Get(id)
		if err != nil || store.SpawnerOf(target) == "" || target.Archived || !runtime.driver.Exists(id) {
			// Gone, filed away, or a person's own session: nothing for the
			// board to do.
			_ = removeFlag(flag)
			continue
		}
		if target.Status == status.Working {
			continue
		}
		if err := removeFlag(flag); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := runtime.store.SetAgentLaunchedAt(target.ID, now); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.endSession(runtime, target, store.EndKilled); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := s.relaunch(runtime, target, StaleSandboxResume); err != nil {
			errs = append(errs, err)
			continue
		}
		restarted++
	}
	return restarted, errors.Join(errs...)
}

func removeFlag(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
