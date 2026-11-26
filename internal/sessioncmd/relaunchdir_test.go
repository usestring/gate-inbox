package sessioncmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/status"
)

// A running session whose working directory was removed (a worktree
// cleaned up under it) is refused an account switch before anything is
// touched. The restart a switch makes ends the pane before the relaunch
// checks the directory, so going ahead would leave the session dead and its
// row pointed at an account it never ran on.
func TestASwitchRefusesARunningSessionWhoseDirectoryIsGone(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	h.sessions.newPacer = unpaced(t.TempDir())
	dir := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker", Tool: "claude", Directory: dir})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	_, err = h.sessions.SwitchAccount(h.caller.ID, created.ID, "alice1")
	if row, _ := h.store.Get(created.ID); row.Account != "" {
		t.Errorf("a refused switch re-pointed the row to %q", row.Account)
	}
	if !h.driver.Exists(created.ID) {
		t.Fatal("a refused switch ended the session")
	}
	if !errors.Is(err, errDirectoryGone) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("SwitchAccount = %v, want it refused naming the missing directory", err)
	}
	if _, err := h.sessions.BoardSwitchAccount(created.ID, "alice1", true); !errors.Is(err, errDirectoryGone) {
		t.Fatalf("BoardSwitchAccount = %v, want it refused too", err)
	}
	if !h.driver.Exists(created.ID) {
		t.Error("a refused board switch ended the session")
	}
}

// A dead row whose directory is gone is still only re-pointed: nothing is
// ended, and its revive reports the directory itself.
func TestASwitchStillRepointsADeadSessionWhoseDirectoryIsGone(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	dir := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker", Tool: "claude", Directory: dir})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.sessions.Kill(h.caller.ID, created.ID, extension.KillByCLI); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	switched, err := h.sessions.SwitchAccount(h.caller.ID, created.ID, "alice1")
	if err != nil || switched.Account != "ALICE1" || switched.Running {
		t.Fatalf("SwitchAccount on a dead row = %+v, %v; want it re-pointed only", switched, err)
	}
}

// A stale-sandbox restart of a child whose directory is gone is not made:
// the child keeps running, its flag is dropped so no later pass tries
// again, and the refusal is reported once.
func TestAStaleSandboxRestartLeavesAChildWhoseDirectoryIsGone(t *testing.T) {
	h, child := grantHarness(t)
	flag := staleFlag(t, h, child.ID)
	now := time.Now()
	SandboxStaleHook(h.sessions.configDir, child.ID, flag, bwrapFailure, now)
	SandboxStaleHook(h.sessions.configDir, child.ID, flag, bwrapFailure, now)
	if err := h.store.UpdateStatus(child.ID, status.Finished); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(child.Cwd); err != nil {
		t.Fatal(err)
	}
	n, err := h.sessions.RestartStaleSandboxes(now)
	if !h.driver.Exists(child.ID) {
		t.Fatal("the child was ended")
	}
	if n != 0 || !errors.Is(err, errDirectoryGone) {
		t.Fatalf("restarted %d, %v; want none and the missing directory reported", n, err)
	}
	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Fatalf("flag still there: %v", err)
	}
	if n, err := h.sessions.RestartStaleSandboxes(now); n != 0 || err != nil {
		t.Fatalf("second pass restarted %d, %v; want nothing", n, err)
	}
}
