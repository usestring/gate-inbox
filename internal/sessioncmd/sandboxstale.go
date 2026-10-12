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
var staleSandboxLine = regexp.MustCompile(`(?m)^bwrap: Can't get type of source `)

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
	return staleSandboxLine.MatchString(call.Error) && !strings.Contains(call.ToolInput.Command, "get type of source")
}

// SandboxStaleHook records a stale-sandbox failure in flag and returns the
// PostToolUseFailure note for it, or nothing for any other failure.
func SandboxStaleHook(configDir, sessionID, flag string, payload []byte, now time.Time) string {
	if !staleSandboxFailure(payload) {
		return ""
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
	out, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":     "PostToolUseFailure",
		"additionalContext": note,
	}})
	if err != nil {
		return ""
	}
	return string(out)
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
		if err != nil || store.SpawnerOf(target) == "" || !runtime.driver.Exists(id) {
			// Gone, or a person's own session: nothing for the board to do.
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
