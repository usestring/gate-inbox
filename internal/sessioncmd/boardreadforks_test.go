package sessioncmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/git"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// loggedTmux is h.sessions with every tmux process it forks written down, so
// a test counts what a board read costs rather than asserting its shape. The
// shim execs the tmux first on PATH, which keeps tmuxtest's isolation.
func loggedTmux(t *testing.T, h *sessionHarness) (*Sessions, func() []string) {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\n{ printf '%s\\n' \"$*\"; } >> " + log + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o700); err != nil {
		t.Fatalf("shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	driver, err := tmux.NewWithSocket(h.driver.SocketName())
	if err != nil {
		t.Fatalf("tmux driver: %v", err)
	}
	sessions := newSessions(h.sessions.configDir, MCPVocabulary(), func(string) (*tmux.Driver, error) { return driver, nil }, git.New)
	sessions.newPacer = h.sessions.newPacer
	return sessions, func() []string {
		raw, _ := os.ReadFile(log)
		_ = os.Remove(log)
		var calls []string
		for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
			if line != "" {
				calls = append(calls, line)
			}
		}
		return calls
	}
}

// A board extension reads the sessions it watches on every poll pass, so a
// fork a read can save is saved once per session per pass. Liveness and the
// pane's directory come back from one tmux call; a read of the pane adds only
// the capture.
func TestBoardGetAndReadForkOnceForLiveness(t *testing.T) {
	h := newSessionHarness(t)
	moved := t.TempDir()
	live := childRunning(t, h, "", "fork001", "live", "ready\n", "cd "+moved+" && cat %s; sleep 60")
	dead := childShowing(t, h, "", "fork002", "dead", "ready\n")
	if err := h.driver.Kill(dead.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	sessions, calls := loggedTmux(t, h)

	got, err := sessions.BoardGet(live.ID)
	if err != nil {
		t.Fatalf("BoardGet: %v", err)
	}
	if !got.Running || resolvedDir(got.Directory) != resolvedDir(moved) {
		t.Fatalf("BoardGet = running %v in %q, want running in %q", got.Running, got.Directory, moved)
	}
	if forks := calls(); len(forks) != 1 {
		t.Errorf("BoardGet of a live session forked tmux %d times, want 1:\n%s", len(forks), strings.Join(forks, "\n"))
	}

	read, err := sessions.BoardRead(live.ID)
	if err != nil {
		t.Fatalf("BoardRead: %v", err)
	}
	if !read.Live || !strings.Contains(read.Text, "ready") || resolvedDir(read.Session.Directory) != resolvedDir(moved) {
		t.Fatalf("BoardRead = live %v, text %q, dir %q; want the live pane in %q", read.Live, read.Text, read.Session.Directory, moved)
	}
	if forks := calls(); len(forks) != 2 {
		t.Errorf("BoardRead of a live session forked tmux %d times, want 2:\n%s", len(forks), strings.Join(forks, "\n"))
	}

	gone, err := sessions.BoardGet(dead.ID)
	if err != nil {
		t.Fatalf("BoardGet dead: %v", err)
	}
	if gone.Running || gone.Directory != dead.Cwd {
		t.Fatalf("BoardGet of a dead session = running %v in %q, want stopped in %q", gone.Running, gone.Directory, dead.Cwd)
	}
	if forks := calls(); len(forks) != 1 {
		t.Errorf("BoardGet of a dead session forked tmux %d times, want 1:\n%s", len(forks), strings.Join(forks, "\n"))
	}
}

func resolvedDir(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// A dead child nobody filed away is read on every pass the same way, and
// forks no tmux either.
func TestBoardGetOfADeadSessionForksNoTmux(t *testing.T) {
	h := newSessionHarness(t)
	gone := childShowing(t, h, "", "fork005", "gone", "ready\n")
	if err := h.driver.Kill(gone.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if err := h.store.UpdateStatus(gone.ID, status.Dead); err != nil {
		t.Fatalf("status: %v", err)
	}
	sessions, calls := loggedTmux(t, h)

	for range 3 {
		got, err := sessions.BoardGet(gone.ID)
		if err != nil {
			t.Fatalf("BoardGet: %v", err)
		}
		if got.Running || got.Directory != gone.Cwd {
			t.Fatalf("BoardGet = running %v in %q; want stopped in %q", got.Running, got.Directory, gone.Cwd)
		}
	}
	if forks := calls(); len(forks) != 0 {
		t.Errorf("BoardGet of a dead session forked tmux %d times, want 0:\n%s", len(forks), strings.Join(forks, "\n"))
	}
}

// An extension that keeps an archived id on record reads it on every pass.
// The row is dead and its pane is gone, so a read forks no tmux at all: each
// fork was a "can't find window" failure and a WARN in the board's log.
func TestBoardGetOfAnArchivedDeadSessionForksNoTmux(t *testing.T) {
	h := newSessionHarness(t)
	filed := childShowing(t, h, "", "fork003", "filed", "ready\n")
	if err := h.driver.Kill(filed.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if err := h.store.UpdateStatus(filed.ID, status.Dead); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := h.store.SetArchived(filed.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	sessions, calls := loggedTmux(t, h)

	for range 3 {
		got, err := sessions.BoardGet(filed.ID)
		if err != nil {
			t.Fatalf("BoardGet: %v", err)
		}
		if got.Running || !got.Archived || got.Directory != filed.Cwd {
			t.Fatalf("BoardGet = running %v, archived %v in %q; want archived and stopped in %q", got.Running, got.Archived, got.Directory, filed.Cwd)
		}
	}
	if forks := calls(); len(forks) != 0 {
		t.Errorf("BoardGet of an archived dead session forked tmux %d times, want 0:\n%s", len(forks), strings.Join(forks, "\n"))
	}
}

// Archiving a group files its sessions without ending them, so an archived
// row that is not dead may still have a pane, and a read still asks tmux.
func TestBoardGetOfAnArchivedLiveSessionStillAsksTmux(t *testing.T) {
	h := newSessionHarness(t)
	filed := childShowing(t, h, "", "fork004", "filed", "ready\n")
	if err := h.store.SetArchived(filed.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	sessions, calls := loggedTmux(t, h)

	got, err := sessions.BoardGet(filed.ID)
	if err != nil {
		t.Fatalf("BoardGet: %v", err)
	}
	if !got.Running {
		t.Fatalf("BoardGet of an archived session whose pane is up = not running")
	}
	if forks := calls(); len(forks) != 1 {
		t.Errorf("BoardGet of an archived live session forked tmux %d times, want 1:\n%s", len(forks), strings.Join(forks, "\n"))
	}
}
