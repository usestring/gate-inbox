package ui

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

// fakeTmuxDriver is a driver whose tmux is a script. A real tmux cannot be
// asked to stall, and stalling is the case: the server is up and busy inside
// somebody else's command, so the manager's own call sits in the queue with
// nothing bounding how long it waits.
//
// The binary is found the way the real one is, off PATH at construction, so
// the driver under test is the production one rather than a stub of it.
func fakeTmuxDriver(t *testing.T, body string) *tmux.Driver {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	driver, err := tmux.NewWithSocket("gi-scan-timeout-test")
	if err != nil {
		t.Fatalf("NewWithSocket: %v", err)
	}
	t.Cleanup(driver.CloseCaptureClients)
	return driver
}

// hangingOn sleeps out every command aimed at one socket, and answers each
// other one the way a machine with no tmux server running answers.
func hangingOn(socket string) string {
	return `case " $* " in
  *" -L ` + socket + ` "*) sleep 30; exit 0 ;;
esac
echo "no server running on $2" >&2
exit 1
`
}

// borrowedRow puts a borrowed pane on the board: a row the manager did not
// create, pointed at a pane on somebody else's server, which is the only kind
// of row a poll pass deletes on its own.
func borrowedRow(t *testing.T, st *store.Store, driver *tmux.Driver, socket string) store.Session {
	t.Helper()
	now := time.Now()
	sess := store.Session{
		ID: "borrowed", Name: "borrowed", Tool: "claude", Cwd: t.TempDir(),
		Status: "idle", TmuxSocket: socket, TmuxPaneID: "%7",
		CreatedAt: now, LastStatusAt: now,
	}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := driver.Adopt(sess.ID, tmux.Target{Socket: socket, Name: sess.TmuxPaneID}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	return sess
}

func rowGone(t *testing.T, st *store.Store, id string) bool {
	t.Helper()
	_, err := st.Get(id)
	if err == nil {
		return false
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get %s: %v", id, err)
	}
	return true
}

// TestAPassWhoseScanTimedOutPrunesNothing is the safety property the deadline
// had to be added without breaking. A pass deletes an adopted row on the word
// of scan.Gone, and Gone means one thing: that server answered, and the pane
// was not on it. A scan killed by its deadline answered nothing at all, and
// must not be able to say it.
//
// The control half is what keeps this honest -- the same row, the same
// driver, the same number of passes, against a server that does answer -- so
// a test that stopped pruning altogether would fail here rather than pass
// twice over.
func TestAPassWhoseScanTimedOutPrunesNothing(t *testing.T) {
	const borrowedSocket = "borrowed-server"

	t.Run("a server that answers prunes the row", func(t *testing.T) {
		m := buildModel(t)
		p := m.poller
		p.tmux = fakeTmuxDriver(t, hangingOn("nothing-hangs-here"))
		sess := borrowedRow(t, m.store, p.tmux, borrowedSocket)
		// One pass's worth of evidence is already in: a row goes on the
		// pass that completes the streak, so this is the pass that deletes.
		p.goneAdopted[sess.ID] = adoptedGonePasses - 1

		var stat passStat
		p.refreshPass(&stat)

		if !rowGone(t, m.store, sess.ID) {
			t.Fatal("a server that answered and did not name the pane left the row in place; the rest of this test proves nothing")
		}
	})

	t.Run("a server that never answers keeps it", func(t *testing.T) {
		m := buildModel(t)
		p := m.poller
		p.tmux = fakeTmuxDriver(t, hangingOn(borrowedSocket))
		sess := borrowedRow(t, m.store, p.tmux, borrowedSocket)
		p.goneAdopted[sess.ID] = adoptedGonePasses - 1

		var stat passStat
		start := time.Now()
		msg := p.refreshPass(&stat)
		elapsed := time.Since(start)

		failed, isErr := msg.(errMsg)
		if !isErr {
			t.Fatalf("a pass whose scan timed out returned %T, want the error that abandons it", msg)
		}
		if !errors.Is(failed.err, tmux.ErrTimeout) {
			t.Errorf("pass error = %v, want %v", failed.err, tmux.ErrTimeout)
		}
		if rowGone(t, m.store, sess.ID) {
			t.Fatal("a pass that never reached the server deleted the row anyway")
		}
		// The fake sleeps thirty seconds; the pass is what used to wait it
		// out, holding its lock for the whole stall. Doubling the budget
		// keeps that answer intact on a loaded machine.
		if elapsed > 2*tmux.ScanTimeout {
			t.Errorf("pass took %s, want it abandoned near %s", elapsed.Round(time.Millisecond), tmux.ScanTimeout)
		}
	})
}
