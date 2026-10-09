package sessioncmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/git"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// countingSessions is h.sessions with every tmux call it makes logged, so a
// test can count the processes a board read forks rather than assert about
// its shape. The shim execs whichever tmux was first on PATH, so the test
// server keeps the isolation tmuxtest put in front of it.
func countingSessions(t *testing.T, h *sessionHarness) (*Sessions, func() []string) {
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
	return sessions, func() []string {
		raw, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		var calls []string
		for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
			if line != "" {
				calls = append(calls, line)
			}
		}
		return calls
	}
}

// A board-wide listing is what an extension runs on every poll pass, so its
// cost per running row is the cost of the whole board every two seconds. The
// pane directory each row reports comes off the one listing that proves the
// pane alive: one tmux call for the page, however many rows are running.
func TestBoardListReadsEveryDirectoryInOneTmuxCall(t *testing.T) {
	h := newSessionHarness(t)
	const children = 6
	moved := t.TempDir()
	want := map[string]string{h.caller.ID: h.caller.Cwd}
	for i := range children {
		id := fmt.Sprintf("dirs%03d", i)
		command := "cat %s; sleep 60"
		if i == 0 {
			// A pane that moved since launch reports where it is now,
			// not the directory its row was filed with.
			command = "cd " + moved + " && cat %s; sleep 60"
		}
		child := childRunning(t, h, h.caller.ID, id, id, "ready\n", command)
		want[child.ID] = child.Cwd
		if i == 0 {
			want[child.ID] = moved
		}
	}
	sessions, calls := countingSessions(t, h)
	list, err := sessions.BoardList(ListOptions{})
	if err != nil {
		t.Fatalf("BoardList: %v", err)
	}
	got := calls()
	if len(list.Sessions) != children+1 {
		t.Fatalf("listed %d sessions, want %d", len(list.Sessions), children+1)
	}
	for _, sess := range list.Sessions {
		if !sess.Running {
			t.Errorf("%s listed as not running", sess.ID)
		}
		if resolved(t, sess.Directory) != resolved(t, want[sess.ID]) {
			t.Errorf("%s directory = %q, want %q", sess.ID, sess.Directory, want[sess.ID])
		}
	}
	if len(got) != 1 {
		t.Fatalf("BoardList over %d running sessions forked tmux %d times, want 1:\n%s", children+1, len(got), strings.Join(got, "\n"))
	}
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return real
}
