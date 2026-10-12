package sessioncmd

import (
	"encoding/json"
	"os"
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
			if !strings.Contains(note, "put an empty placeholder back") {
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
		filepath.Join(repo, "sub", ".mcp.json"),
		filepath.Join(repo, ".git", "worktrees", "gone", "index.lock"),
		filepath.Join(t.TempDir(), "absent", ".git", "worktrees", "gone", "commondir"),
		"relative/.git/worktrees/gone/commondir",
	} {
		if restoreWorktreeSource(path) {
			t.Errorf("restored %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "config.lock")); err == nil {
		t.Fatal("created config.lock")
	}
}
