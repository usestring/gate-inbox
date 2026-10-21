package sessioncmd

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
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
	if restoreStaleSource(staleSource(payload), now) {
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
// 2026-10-12 04:25), and most of the rest a .mcp.json in a subdirectory.
// Putting the path back fixes the session in place, and every other session
// that bound the same path, with no restart.
//
// What is put back has to outlive two deleters, or the sessions break again
// whenever another one exits. Claude Code's own sandbox mounts /dev/null over
// a deny path that does not exist, which makes bwrap create an empty file
// there; Claude Code unlinks every empty file it so placed when its last
// sandboxed command ends or it exits, whoever has bound the path since. And
// this board's SessionEnd hook runs `git worktree prune`, which drops any
// worktree entry without a gitdir file. So every file written here has
// content, and a worktree placeholder carries a lock file, which prune
// honours. Git does not otherwise see the placeholder: an entry with no
// gitdir file is left out of `git worktree list` and holds no branch.
// Placeholders older than placeholderLifetime are swept the next time one is
// written, so they cannot pile up into Claude Code's deny list.
//
// Lock files and checkout files are never recreated; an empty config.lock
// would block every git config write.

const staleRestoredNote = "Gate Inbox: that Bash call failed because a path Claude Code had bound into this " +
	"session's sandbox (a removed worktree's metadata or a placeholder .mcp.json) was deleted. Gate Inbox has put a " +
	"placeholder back, so the sandbox works again. Retry the same command inside the sandbox now; do not switch to " +
	"dangerouslyDisableSandbox, and do not ask anyone for approval over it."

var staleSourcePath = regexp.MustCompile(`(?m)^bwrap: ` + staleRefusal + ` (.+?): No such file or directory`)

// worktreeMetadataFiles are the per-worktree files bwrap has been seen to
// bind, with the content a placeholder holds; the worktree's directory
// itself is restored for any of them.
var worktreeMetadataFiles = map[string]string{
	"commondir":       "../..\n",
	"config.worktree": placeholderConfig,
	"config":          placeholderConfig,
}

const placeholderConfig = "# Gate Inbox placeholder for a removed worktree\n"

// Claude Code binds both of these for every registered worktree, so both are
// written at once rather than each costing its own failed call.
var worktreePlaceholderFiles = []string{"commondir", "config.worktree"}

// placeholderLock marks a worktree entry as this hook's placeholder.
const placeholderLock = "gate-inbox sandbox placeholder: a removed worktree that running Claude Code sandboxes still bind\n"

// placeholderLifetime is how long a worktree placeholder is kept.
const placeholderLifetime = 72 * time.Hour

// mcpPlaceholder is a .mcp.json that declares no servers.
const mcpPlaceholder = "{\"mcpServers\":{}}\n"

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

// restoreStaleSource puts back path when it is a removed worktree's metadata
// or a subdirectory's .mcp.json. It reports whether path exists afterwards.
func restoreStaleSource(path string, now time.Time) bool {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	if filepath.Base(path) == ".mcp.json" {
		return restoreMCPConfig(path)
	}
	return restoreWorktreeSource(path, now)
}

// restoreWorktreeSource puts back path when it is a removed worktree's
// metadata directory, or one of its known files, under a git directory that
// still exists. A live worktree's entry, one with a gitdir file, is never
// written to.
func restoreWorktreeSource(path string, now time.Time) bool {
	dir := path
	if _, ok := worktreeMetadataFiles[filepath.Base(path)]; ok {
		dir = filepath.Dir(path)
	}
	worktrees := filepath.Dir(dir)
	gitDir := filepath.Dir(worktrees)
	if filepath.Base(worktrees) != "worktrees" || filepath.Base(gitDir) != ".git" {
		return false
	}
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		return false
	}
	if _, err := os.Lstat(filepath.Join(dir, "gitdir")); err == nil {
		_, err := os.Stat(path)
		return err == nil
	}
	sweepPlaceholders(worktrees, now)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	files := map[string]string{"locked": placeholderLock}
	for _, name := range worktreePlaceholderFiles {
		files[name] = worktreeMetadataFiles[name]
	}
	if content, ok := worktreeMetadataFiles[filepath.Base(path)]; ok {
		files[filepath.Base(path)] = content
	}
	for name, content := range files {
		if err := fillPlaceholder(filepath.Join(dir, name), content); err != nil {
			return false
		}
	}
	_, err := os.Stat(path)
	return err == nil
}

// restoreMCPConfig writes a .mcp.json that declares no servers into a
// directory of a git checkout, outside its .git directory, after keeping it
// out of that checkout's untracked files. The path comes from a command's
// output, so a directory outside any checkout is left alone.
func restoreMCPConfig(path string) bool {
	dir := filepath.Dir(path)
	if strings.Contains(dir+string(filepath.Separator), string(filepath.Separator)+".git"+string(filepath.Separator)) {
		return false
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	if !excludeUntracked(dir, ".mcp.json") {
		return false
	}
	return fillPlaceholder(path, mcpPlaceholder) == nil
}

// fillPlaceholder writes content to path unless it already holds something.
// It replaces rather than writes in place: an empty file left by bwrap is
// read-only.
func fillPlaceholder(path, content string) error {
	if info, err := os.Lstat(path); err == nil && (info.Size() > 0 || !info.Mode().IsRegular()) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gate-inbox-placeholder-*")
	if err != nil {
		return err
	}
	_, werr := tmp.WriteString(content)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), 0o644)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// excludeUntracked adds dir/name to the info/exclude of the checkout holding
// dir, so the placeholder does not make that checkout look dirty, and reports
// whether dir is in a checkout whose exclude file now lists it.
func excludeUntracked(dir, name string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-prefix", "--git-path", "info/exclude").Output()
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 2 {
		return false
	}
	pattern := "/" + lines[0] + name
	exclude := lines[1]
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(dir, exclude)
	}
	raw, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == pattern {
			return true
		}
	}
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		pattern = "\n" + pattern
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return false
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	_, werr := f.WriteString(pattern + "\n")
	return errors.Join(werr, f.Close()) == nil
}

// sweepPlaceholders removes this hook's worktree placeholders older than
// placeholderLifetime. Claude Code lists every entry under worktrees/ into
// one sandbox argument, so they must not accumulate.
func sweepPlaceholders(worktrees string, now time.Time) {
	entries, err := os.ReadDir(worktrees)
	if err != nil {
		return
	}
	for _, entry := range entries {
		dir := filepath.Join(worktrees, entry.Name())
		if _, err := os.Lstat(filepath.Join(dir, "gitdir")); err == nil {
			continue
		}
		lock := filepath.Join(dir, "locked")
		info, err := os.Stat(lock)
		if err != nil || now.Sub(info.ModTime()) < placeholderLifetime {
			continue
		}
		if raw, err := os.ReadFile(lock); err == nil && string(raw) == placeholderLock {
			_ = os.RemoveAll(dir)
		}
	}
}

// AwaitingStaleRestart reports whether the session whose hook status file is
// statusFile has ended its turn only so the board can restart it on a fresh
// sandbox: its flag says the failure persisted, and RestartStaleSandboxes
// removes the flag as it restarts the session.
func AwaitingStaleRestart(statusFile string) bool {
	raw, err := os.ReadFile(statusFile + hooks.SandboxStaleSuffix)
	return err == nil && strings.TrimSpace(string(raw)) == stalePersistent
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
		// A busy relaunch queue leaves the flag for the next pass: the
		// session waits at its prompt, still stale, rather than piling
		// another launch onto the machine.
		slot, err := s.pace(runtime, target.ID, false)
		if err != nil {
			continue
		}
		if err := removeFlag(flag); err != nil {
			slot.Cancel()
			errs = append(errs, err)
			continue
		}
		if err := runtime.store.SetAgentLaunchedAt(target.ID, now); err != nil {
			slot.Cancel()
			errs = append(errs, err)
			continue
		}
		if err := s.endSession(runtime, target, store.EndKilled); err != nil {
			slot.Cancel()
			errs = append(errs, err)
			continue
		}
		if _, err := s.relaunch(runtime, target, StaleSandboxResume, slot); err != nil {
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
