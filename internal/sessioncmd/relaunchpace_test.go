package sessioncmd

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/usestring/gate-inbox/internal/launchpace"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// unpaced is a relaunch queue that never holds a launch back.
func unpaced(dir string) func(*runtime) *launchpace.Pacer {
	return func(*runtime) *launchpace.Pacer {
		return launchpace.New(dir, launchpace.Limits{MinGap: time.Nanosecond}, func(string, time.Time) bool { return true })
	}
}

// While another session is relaunching, a board extension's switch is
// refused before the row is touched: the session keeps running on the
// account it is on, and the extension asks again later.
func TestABusyRelaunchQueueRefusesABoardSwitchUntouched(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	created, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "worker", Tool: "claude"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	busy := launchpace.New(h.sessions.configDir, launchpace.Limits{}, nil)
	busy.Record("someone-else")
	h.sessions.newPacer = func(*runtime) *launchpace.Pacer { return busy }

	if _, err := h.sessions.BoardSwitchAccount(created.ID, "alice1"); !errors.Is(err, launchpace.ErrBusy) {
		t.Fatalf("BoardSwitchAccount with the queue busy = %v, want ErrBusy", err)
	}
	if row, _ := h.store.Get(created.ID); row.Account != "" {
		t.Errorf("a refused switch re-pointed the row to %q", row.Account)
	}
	if !h.driver.Exists(created.ID) {
		t.Error("a refused switch ended the session")
	}
}

// A relaunch has rested only once its own launch is stamped and its pane
// shows it resting; the row's status alone, which a relaunch writes before
// the CLI draws anything, does not count.
func TestARelaunchRestsOnlyOnItsOwnLaunch(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	runtime, err := h.sessions.open()
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.store.Close()
	since := time.Now()

	stale := store.Session{ID: uuid.NewString()[:8], Name: "stale", Tool: "claude", Cwd: h.caller.Cwd, Status: status.Idle, AgentLaunchedAt: since.Add(-time.Minute)}
	dead := store.Session{ID: uuid.NewString()[:8], Name: "dead", Tool: "claude", Cwd: h.caller.Cwd, Status: status.Dead, AgentLaunchedAt: since.Add(time.Second)}
	gone := store.Session{ID: uuid.NewString()[:8], Name: "gone", Tool: "claude", Cwd: h.caller.Cwd, Status: status.Idle, AgentLaunchedAt: since.Add(time.Second)}
	for _, sess := range []store.Session{stale, dead, gone} {
		if err := h.store.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
		if err := h.store.SetAgentLaunchedAt(sess.ID, sess.AgentLaunchedAt); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.relaunchRested(stale.ID, since) {
		t.Error("an idle row whose launch predates the slot read as rested")
	}
	if !runtime.relaunchRested(dead.ID, since) {
		t.Error("a dead row was still waited on")
	}
	if !runtime.relaunchRested(gone.ID, since) {
		t.Error("a row with no pane was still waited on")
	}
	if !runtime.relaunchRested("no-such-row", since) {
		t.Error("a deleted row was still waited on")
	}
}
