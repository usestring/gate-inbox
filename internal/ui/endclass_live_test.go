package ui

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestEndsAreClassifiedFromWhatARealPaneLeavesBehind runs agents that end the
// ways an operator's do -- quit, crash, pane closed in tmux -- on a server of
// its own, then takes that server away the way a reboot does, and reads each
// verdict from the evidence the launch script and the server actually leave.
func TestEndsAreClassifiedFromWhatARealPaneLeavesBehind(t *testing.T) {
	socket := newTestSocket()
	t.Cleanup(func() { tmuxtest.KillServer(socket) })
	driver, err := tmux.NewWithSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	manager := hooks.NewManager(tmuxtest.ScratchDir(t))
	// The launch creates this; the script only writes into it.
	if err := os.MkdirAll(manager.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// A server already up before any of the agents, as the operator's is.
	tmuxOut(t, socket, "new-session", "-d", "-s", "anchor", "sleep 600")

	launched := time.Now()
	start := func(id, command string) store.Session {
		t.Helper()
		env := map[string]string{hooks.EnvExitFile: manager.ExitFile(id)}
		if err := driver.Create(id, t.TempDir(), command, env, 80, 24); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		return store.Session{ID: id, Name: id, Tool: "claude", Status: status.Dead, AgentLaunchedAt: launched}
	}
	quit := start("quit", "sh -c 'exit 0'")
	crashed := start("crashed", "sh -c 'exit 3'")
	closed := start("closed", "sleep 600")

	waitFor(t, "the quit and the crash to be recorded", func() bool {
		_, _, quitOK := manager.ReadExit(quit.ID)
		_, _, crashOK := manager.ReadExit(crashed.ID)
		return quitOK && crashOK
	})
	if out, err := exec.Command("tmux", "-L", socket, "kill-session", "-t", "gi_"+closed.ID).CombinedOutput(); err != nil {
		t.Fatalf("close the pane: %v: %s", err, out)
	}

	ev := endEvidence{exit: manager.ReadExit, serverKnown: true}
	ev.serverStarted, ev.serverUp = driver.ServerStarted()
	if !ev.serverUp {
		t.Fatal("the server should read as up")
	}
	want := map[string]endVerdict{quit.ID: endByOperator, crashed.ID: endDied, closed.ID: endByOperator}
	for _, sess := range []store.Session{quit, crashed, closed} {
		if got := classifyEnd(sess, ev); got.verdict != want[sess.ID] {
			t.Errorf("%s: %v (%s), want %v", sess.ID, got.verdict, got.why, want[sess.ID])
		}
	}

	// The reboot: the server goes, and the board opens before anything has
	// started a new one.
	tmuxtest.KillServer(socket)
	ev.serverStarted, ev.serverUp = driver.ServerStarted()
	if ev.serverUp {
		t.Fatal("the server should read as gone")
	}
	sleeper := store.Session{ID: "sleeper", Name: "sleeper", Tool: "claude", Status: status.Dead, AgentLaunchedAt: launched}
	if got := classifyEnd(sleeper, ev); got.verdict != endDied {
		t.Errorf("an agent on a server that is gone: %v (%s), want died", got.verdict, got.why)
	}
	// What the agents themselves recorded outlives the server.
	if got := classifyEnd(quit, ev); got.verdict != endByOperator {
		t.Errorf("a quit agent after the reboot: %v (%s)", got.verdict, got.why)
	}

	// A new server, started after the agents launched: the one they ran on
	// is not this one.
	time.Sleep(serverClockSlack + time.Second)
	tmuxOut(t, socket, "new-session", "-d", "-s", "anchor", "sleep 600")
	ev.serverStarted, ev.serverUp = driver.ServerStarted()
	if got := classifyEnd(sleeper, ev); got.verdict != endDied {
		t.Errorf("an agent from before the server restarted: %v (%s), want died", got.verdict, got.why)
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
