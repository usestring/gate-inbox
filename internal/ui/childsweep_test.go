package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The sweep files what has exited and ends nothing. On 2026-09-11 it killed
// eight working children of one parent seconds after each delivered an
// interim update and came to rest, and re-killed the ones the parent
// revived. A finished child with a live pane is an agent between turns, and
// what a parent wants ended it ends itself.
func TestTheChildSweepLeavesALivePaneAndFilesAnExitedOne(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "parent", dir, "")
	createSession(t, m, "kid", dir, "")
	loadStoredRows(t, m)
	var parent, kid store.Session
	for _, sess := range m.sessions {
		switch sess.Name {
		case "parent":
			parent = sess
		case "kid":
			kid = sess
		}
	}
	if err := m.store.PlaceSession(kid.ID, parent.Group, parent.ID); err != nil {
		t.Fatalf("PlaceSession: %v", err)
	}
	id, _, err := m.store.Enqueue(store.InboxMessage{
		SessionID: parent.ID, SenderID: kid.ID, SenderName: kid.Name,
		Body: "interim: halfway there", SentAt: time.Now(),
	}, store.DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := m.store.MarkDelivered(id, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if err := m.store.UpdateStatus(kid.ID, status.Finished); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)

	runChildSweep(t, m)
	if !m.tmux.Exists(kid.ID) {
		t.Fatal("the sweep killed a finished child whose pane was live")
	}
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived {
		t.Fatalf("the sweep filed a child whose pane was live: %+v, %v", row, err)
	}

	// The pane exits, the poller reads it as dead, and the delivered report
	// is then what makes it done.
	if err := m.tmux.Kill(kid.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := m.store.UpdateStatus(kid.ID, status.Dead); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)
	runChildSweep(t, m)
	row, err := m.store.Get(kid.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !row.Archived {
		t.Fatalf("the sweep left an exited child that reported: %+v", row)
	}
	if row.Status != status.Dead {
		t.Fatalf("filing rewrote the status to %q", row.Status)
	}
}

// runChildSweep arms one sweep, runs its command the way Bubble Tea would,
// and lands the answer. The sweep asks tmux and the store off the event loop
// now, so a test that only called it would be asserting on a question nobody
// had answered yet.
func runChildSweep(t *testing.T, m *Model) {
	t.Helper()
	answer := armChildSweep(t, m)()
	msg, ok := answer.(childSweptMsg)
	if !ok {
		t.Fatalf("the sweep answered with %T, want childSweptMsg", answer)
	}
	m.applyChildSweep(msg)
}
