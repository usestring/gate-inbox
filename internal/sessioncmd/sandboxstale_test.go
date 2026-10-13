package sessioncmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
)

// bwrapFailure is a PostToolUseFailure payload as Claude Code sends it for a
// Bash call bwrap refused.
var bwrapFailure = []byte(`{"hook_event_name":"PostToolUseFailure","tool_name":"Bash",` +
	`"tool_input":{"command":"git status --short"},` +
	`"error":"Exit code 1\nbwrap: Can't get type of source /r/.git/worktrees/gone/commondir: No such file or directory"}`)

func staleFlag(t *testing.T, h *sessionHarness, id string) string {
	t.Helper()
	manager := hooks.NewManager(h.sessions.configDir)
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return manager.StatusFile(id) + hooks.SandboxStaleSuffix
}

// The first stale-sandbox failure is retried in the sandbox; only a second
// one sends the child to end its turn for a restart.
func TestAStaleSandboxIsRetriedBeforeItIsRestarted(t *testing.T) {
	h, child := grantHarness(t)
	flag := staleFlag(t, h, child.ID)
	now := time.Now()
	first := SandboxStaleHook(h.sessions.configDir, child.ID, flag, bwrapFailure, now)
	if !strings.Contains(first, "Retry the same command inside the sandbox") {
		t.Fatalf("first note = %s", first)
	}
	if n, err := h.sessions.RestartStaleSandboxes(now); err != nil || n != 0 {
		t.Fatalf("one failure restarted %d, %v; want a retry first", n, err)
	}
	second := SandboxStaleHook(h.sessions.configDir, child.ID, flag, bwrapFailure, now.Add(time.Minute))
	if !strings.Contains(second, "End your turn now") || !strings.Contains(second, `"hookEventName":"PostToolUseFailure"`) {
		t.Fatalf("second note = %s", second)
	}
	if top := SandboxStaleHook(h.sessions.configDir, h.caller.ID, staleFlag(t, h, h.caller.ID), bwrapFailure, now); !strings.Contains(top, "Retry") {
		t.Fatalf("top-level first note = %s", top)
	}
}

func TestAPersistentlyStaleSandboxRestartsTheChildOnceItRests(t *testing.T) {
	h, child := grantHarness(t)
	if err := h.store.SetAgentSessionID(child.ID, "conv-child"); err != nil {
		t.Fatal(err)
	}
	flag := staleFlag(t, h, child.ID)
	now := time.Now()
	SandboxStaleHook(h.sessions.configDir, child.ID, flag, bwrapFailure, now)
	SandboxStaleHook(h.sessions.configDir, child.ID, flag, bwrapFailure, now)

	if err := h.store.UpdateStatus(child.ID, status.Working); err != nil {
		t.Fatal(err)
	}
	if n, err := h.sessions.RestartStaleSandboxes(now); err != nil || n != 0 {
		t.Fatalf("working child: restarted %d, %v; want it left alone mid-turn", n, err)
	}
	if err := h.store.UpdateStatus(child.ID, status.Finished); err != nil {
		t.Fatal(err)
	}
	if n, err := h.sessions.RestartStaleSandboxes(now); err != nil || n != 1 {
		t.Fatalf("resting child: restarted %d, %v; want 1", n, err)
	}
	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Fatalf("flag still there after the restart: %v", err)
	}
	if !h.driver.Exists(child.ID) {
		t.Fatal("the child did not come back")
	}
}

func TestAStaleSandboxNeverRestartsAPersonsOwnSession(t *testing.T) {
	h, _ := grantHarness(t)
	flag := staleFlag(t, h, h.caller.ID)
	now := time.Now()
	SandboxStaleHook(h.sessions.configDir, h.caller.ID, flag, bwrapFailure, now)
	if note := SandboxStaleHook(h.sessions.configDir, h.caller.ID, flag, bwrapFailure, now); !strings.Contains(note, "tell your user to restart") {
		t.Fatalf("top-level note = %s", note)
	}
	if n, err := h.sessions.RestartStaleSandboxes(now); err != nil || n != 0 {
		t.Fatalf("restarted %d, %v; want a top-level session left alone", n, err)
	}
	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Fatalf("flag kept for a session the board will never restart: %v", err)
	}
}

func TestAnOldSingleFailureIsForgotten(t *testing.T) {
	h, child := grantHarness(t)
	flag := staleFlag(t, h, child.ID)
	SandboxStaleHook(h.sessions.configDir, child.ID, flag, bwrapFailure, time.Now())
	if n, err := h.sessions.RestartStaleSandboxes(time.Now().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("restarted %d, %v", n, err)
	}
	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Fatalf("an hour-old single failure is still remembered: %v", err)
	}
}

// Only bwrap's own refusal counts: a command whose text names the message, or
// any other failure, leaves no flag.
func TestOnlyBwrapsRefusalMarksASandboxStale(t *testing.T) {
	h, child := grantHarness(t)
	flag := staleFlag(t, h, child.ID)
	for name, payload := range map[string]string{
		"grep for the message": `{"tool_name":"Bash","tool_input":{"command":"grep \"bwrap: Can't get type of source\" log"},` +
			`"error":"Exit code 1\nbwrap: Can't get type of source /x: No such file or directory"}`,
		"ordinary failure": `{"tool_name":"Bash","tool_input":{"command":"ls /nope"},"error":"Exit code 2\nls: cannot access '/nope'"}`,
		"not json":         `bwrap: Can't get type of source /x`,
	} {
		if note := SandboxStaleHook(h.sessions.configDir, child.ID, flag, []byte(payload), time.Now()); note != "" {
			t.Fatalf("%s: note = %s", name, note)
		}
		if _, err := os.Stat(flag); !os.IsNotExist(err) {
			t.Fatalf("%s: left a flag", name)
		}
	}
}

// A removed worktree's metadata is put back where bwrap expects it, so the
// session is fixed in place: no retry count, no restart.
func TestAStaleWorktreeSourceIsRestoredInPlace(t *testing.T) {
	h, child := grantHarness(t)
	flag := staleFlag(t, h, child.ID)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git", "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{
		filepath.Join(repo, ".git", "worktrees", "gone", "config.worktree"),
		filepath.Join(repo, ".git", "worktrees", "gone2"),
	} {
		payload, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": "ls"},
			"error": "Exit code 1\nbwrap: Can't get type of source " + missing + ": No such file or directory"})
		for range 2 {
			note := SandboxStaleHook(h.sessions.configDir, child.ID, flag, payload, time.Now())
			if !strings.Contains(note, "put a placeholder back") {
				t.Fatalf("note = %s", note)
			}
		}
		if _, err := os.Stat(missing); err != nil {
			t.Fatalf("%s not restored: %v", missing, err)
		}
	}
	if _, err := os.Stat(flag); err == nil {
		t.Fatal("a restored source counted toward a restart")
	}
}

// A lock file, a path outside .git/worktrees, or a git directory that is gone
// is never created; those keep the retry-then-restart path.
func TestStaleSourcesThatAreNotWorktreeMetadataAreLeftAlone(t *testing.T) {
	repo := t.TempDir()
	_ = os.MkdirAll(filepath.Join(repo, ".git", "worktrees"), 0o755)
	for _, path := range []string{
		filepath.Join(repo, ".git", "config.lock"),
		filepath.Join(repo, ".git", "worktrees", "gone", ".mcp.json"),
		filepath.Join(repo, "absent", ".mcp.json"),
		filepath.Join(repo, ".mcp.json"),
		filepath.Join(repo, ".git", "worktrees", "gone", "index.lock"),
		filepath.Join(t.TempDir(), "absent", ".git", "worktrees", "gone", "commondir"),
		"relative/.git/worktrees/gone/commondir",
	} {
		if restoreStaleSource(path, time.Now()) {
			t.Errorf("restored %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "config.lock")); err == nil {
		t.Fatal("created config.lock")
	}
}

// A placeholder has to survive what deletes paths when another session exits:
// Claude Code unlinks the empty files its sandbox placed, and the SessionEnd
// hook runs `git worktree prune`. So every file has content and the entry is
// locked.
func TestARestoredWorktreePlaceholderSurvivesPruneAndCleanup(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "i")
	missing := filepath.Join(repo, ".git", "worktrees", "gone", "commondir")
	if !restoreStaleSource(missing, time.Now()) {
		t.Fatal("not restored")
	}
	git("worktree", "prune")
	for _, name := range []string{"commondir", "config.worktree", "locked"} {
		info, err := os.Stat(filepath.Join(repo, ".git", "worktrees", "gone", name))
		if err != nil || info.Size() == 0 {
			t.Fatalf("%s: missing or empty after prune (%v)", name, err)
		}
	}
	if out := git("worktree", "list", "--porcelain"); strings.Contains(out, "gone") {
		t.Fatalf("git worktree list shows the placeholder\n%s", out)
	}
}

// An empty, read-only file bwrap left is filled in, and a live worktree's
// entry is never written to.
func TestAnEmptyPlaceholderIsFilledAndALiveEntryIsLeftAlone(t *testing.T) {
	repo := t.TempDir()
	gone := filepath.Join(repo, ".git", "worktrees", "gone")
	live := filepath.Join(repo, ".git", "worktrees", "live")
	for _, dir := range []string{gone, live} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(gone, "config.worktree"), nil, 0o444)
	_ = os.WriteFile(filepath.Join(live, "gitdir"), []byte("/x/.git\n"), 0o644)
	if !restoreStaleSource(filepath.Join(gone, "config.worktree"), time.Now()) {
		t.Fatal("not restored")
	}
	if info, _ := os.Stat(filepath.Join(gone, "config.worktree")); info.Size() == 0 {
		t.Fatal("empty file left empty")
	}
	if restoreStaleSource(filepath.Join(live, "commondir"), time.Now()) {
		t.Fatal("reported a missing file in a live entry as restored")
	}
	if _, err := os.Stat(filepath.Join(live, "locked")); err == nil {
		t.Fatal("locked a live worktree")
	}
}

// Old placeholders are swept when a new one is written; anything else under
// worktrees/ is not.
func TestOldPlaceholdersAreSwept(t *testing.T) {
	repo := t.TempDir()
	worktrees := filepath.Join(repo, ".git", "worktrees")
	_ = os.MkdirAll(worktrees, 0o755)
	now := time.Now()
	if !restoreStaleSource(filepath.Join(worktrees, "old", "commondir"), now) {
		t.Fatal("not restored")
	}
	userLocked := filepath.Join(worktrees, "mine")
	_ = os.MkdirAll(userLocked, 0o755)
	_ = os.WriteFile(filepath.Join(userLocked, "locked"), []byte("on a usb disk\n"), 0o644)
	old := now.Add(-placeholderLifetime - time.Hour)
	for _, lock := range []string{filepath.Join(worktrees, "old", "locked"), filepath.Join(userLocked, "locked")} {
		_ = os.Chtimes(lock, old, old)
	}
	if !restoreStaleSource(filepath.Join(worktrees, "new", "commondir"), now) {
		t.Fatal("not restored")
	}
	if _, err := os.Stat(filepath.Join(worktrees, "old")); err == nil {
		t.Fatal("old placeholder kept")
	}
	if _, err := os.Stat(userLocked); err != nil {
		t.Fatal("swept an entry this hook did not write")
	}
}

// A subdirectory's .mcp.json comes back declaring no servers, and stays out
// of the checkout's untracked files.
func TestAStaleMCPConfigIsRestoredAndExcluded(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "-C", repo, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	sub := filepath.Join(repo, "sub")
	_ = os.MkdirAll(sub, 0o755)
	path := filepath.Join(sub, ".mcp.json")
	for range 2 {
		if !restoreStaleSource(path, time.Now()) {
			t.Fatal("not restored")
		}
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != mcpPlaceholder {
		t.Fatalf(".mcp.json = %q", raw)
	}
	exclude, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if strings.Count(string(exclude), "/sub/.mcp.json\n") != 1 {
		t.Fatalf("exclude = %q", exclude)
	}
	out, _ := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if len(out) != 0 {
		t.Fatalf("checkout dirty: %s", out)
	}
}
