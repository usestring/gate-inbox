package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// A long-lived envecho, so the helper outlives both board runs on its own.
const sleeperTool = `
[tools.envecho]
command = "sh -c 'printf %s \"$NOOP_LAUNCH\" > \"$NOOP_OUT\"; sleep 120' --"
default_status = "idle"
activity_cutoff = "(?m)^\\$ "
`

// A board killed outright mid-hold runs no extension stop, so nothing
// aborts the hold then. The next start does, before any extension runs:
// the fresh session's pane is ended and its row deleted, and the session it
// was held to replace runs on untouched.
func TestBoardStartAbortsAHoldAKilledBoardLeft(t *testing.T) {
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) is needed to give the board a terminal")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the board polls a tmux server")
	}
	bin := buildFixture(t)
	socket := tmuxtest.NewSocket("orphan")
	env := fixtureHome(t, "tmux_socket = \""+socket+"\"\n"+sleeperTool)
	home := envValue(env, "GATE_INBOX_HOME")
	socketPath := filepath.Join(envValue(env, "TMUX_TMPDIR"), "tmux-"+strconv.Itoa(os.Getuid()), socket)
	t.Cleanup(func() { killTestServer(t, envValue(env, "TMUX_TMPDIR"), socket) })
	seedSessions(t, filepath.Join(home, "state.db"))
	data := filepath.Join(home, "extensions", "noop")

	_, exited, out := startFixtureBoard(t, script, bin, append(env, "NOOP_SCENARIO=orphan-hold"))
	orphan := waitForFile(t, filepath.Join(data, "orphan.txt"), " held-under ", exited, out)
	var fresh, helper string
	if _, err := fmt.Sscanf(orphan, "%s held-under %s", &fresh, &helper); err != nil {
		t.Fatalf("orphan.txt = %q: %v", orphan, err)
	}
	if holds := heldReplacements(t, home); len(holds) != 1 || holds[0].FreshID != fresh || holds[0].OldID != helper || holds[0].Owner != "noop" {
		t.Fatalf("holds = %+v, want %s held under %s by noop", holds, fresh, helper)
	}
	if panes := tmuxSessions(t, socketPath); !strings.Contains(panes, fresh) || !strings.Contains(panes, helper) {
		t.Fatalf("tmux sessions = %q, want both the helper and its held replacement running", panes)
	}

	started := waitForFile(t, filepath.Join(data, "started.txt"), "", exited, out)
	var pid int
	if _, err := fmt.Sscan(started, &pid); err != nil {
		t.Fatalf("started.txt = %q: %v", started, err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill the board: %v", err)
	}
	<-exited
	if _, err := os.Stat(filepath.Join(data, "stopped.txt")); err == nil {
		t.Fatal("the killed board stopped its extension; the test needs a board that could not")
	}
	if holds := heldReplacements(t, home); len(holds) != 1 {
		t.Fatalf("holds after the kill = %+v, want the orphan still on record", holds)
	}

	if err := os.Remove(filepath.Join(data, "started.txt")); err != nil {
		t.Fatal(err)
	}
	_, exited, out = startFixtureBoard(t, script, bin, append(env, "NOOP_SCENARIO=quiet"))
	restarted := waitForFile(t, filepath.Join(data, "started.txt"), "", exited, out)
	if _, err := fmt.Sscan(restarted, &pid); err != nil {
		t.Fatalf("started.txt = %q: %v", restarted, err)
	}
	// The sweep runs before extensions start, so it is done by now.
	if panes := tmuxSessions(t, socketPath); strings.Contains(panes, fresh) || !strings.Contains(panes, helper) {
		t.Fatalf("tmux sessions = %q, want the held replacement ended and the helper running", panes)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("signal the board: %v", err)
	}
	<-exited

	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Get(fresh); err == nil {
		t.Fatalf("the orphaned replacement %s outlived the board restart", fresh)
	}
	old, err := st.Get(helper)
	if err != nil || old.Status == "dead" || old.ParentID != "c41d0001" {
		t.Fatalf("helper = %+v, %v; want it running in its seat", old, err)
	}
	if holds, err := st.HeldReplacements(); err != nil || len(holds) != 0 {
		t.Fatalf("holds = %+v, %v; want none after the restart", holds, err)
	}
}

// startFixtureBoard runs the fixture's board under script(1), for a
// terminal, and returns it, a channel closed once it exits, and its output.
func startFixtureBoard(t *testing.T, script, bin string, env []string) (*exec.Cmd, <-chan struct{}, *strings.Builder) {
	t.Helper()
	board := exec.Command(script, "-qec", bin, "/dev/null")
	if runtime.GOOS != "linux" {
		board = exec.Command(script, "-q", "/dev/null", bin)
	}
	board.Env = env
	out := &strings.Builder{}
	board.Stdout, board.Stderr = out, out
	if err := board.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		board.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		board.Process.Kill()
		<-exited
	})
	return board, exited, out
}

func heldReplacements(t *testing.T, home string) []store.HeldReplacement {
	t.Helper()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	holds, err := st.HeldReplacements()
	if err != nil {
		t.Fatal(err)
	}
	return holds
}

// tmuxSessions names every session on the test server at path, which is
// addressed by its full path so it can never resolve to another server.
func tmuxSessions(t *testing.T, path string) string {
	t.Helper()
	if !tmuxtest.Owns(filepath.Base(path)) {
		t.Fatalf("refusing to read the tmux server on %q: not a test socket", path)
	}
	out, err := exec.Command("tmux", "-S", path, "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		t.Fatalf("list tmux sessions: %v", err)
	}
	return string(out)
}
