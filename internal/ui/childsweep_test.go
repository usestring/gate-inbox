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

// finishedFanOut is a parent with one child resting finished on a live pane,
// with the sweep's grace down to nothing so a delivered notice counts at once.
func finishedFanOut(t *testing.T) (*Model, store.Session, store.Session) {
	t.Helper()
	m := buildModel(t)
	m.cfg.Children.FinishedGrace.Duration = time.Nanosecond
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
	if err := m.store.UpdateStatus(kid.ID, status.Finished); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)
	kid, _ = m.sessionByID(kid.ID)
	return m, parent, kid
}

// relayRest queues the board's rest notice from kid to parent and, when
// delivered, types it in.
func relayRest(t *testing.T, m *Model, parent, kid store.Session, delivered bool) int64 {
	t.Helper()
	id, _, err := m.store.Enqueue(store.InboxMessage{
		SessionID: parent.ID, SenderID: kid.ID, SenderName: kid.Name,
		Body: kid.Name + " has finished its turn.", Subject: store.ChildRestSubject(kid.ID),
		SentAt: time.Now(),
	}, store.DefaultInboxLimits)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if delivered {
		if err := m.store.MarkDelivered(id, time.Now()); err != nil {
			t.Fatalf("MarkDelivered: %v", err)
		}
	}
	return id
}

// A finished child whose parent has taken in the finish and left it alone
// past the grace goes through x's own teardown: its pane ends, its screen is
// kept, and its row waits in the archived view where u restores it. Until the
// notice is delivered it stays, however long it has rested.
func TestTheChildSweepRetiresAFinishedChildOnceItsParentTookItIn(t *testing.T) {
	m, parent, kid := finishedFanOut(t)
	notice := relayRest(t, m, parent, kid, false)
	runChildSweep(t, m)
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived || !m.tmux.Exists(kid.ID) {
		t.Fatalf("the sweep retired a child whose notice was never delivered: %+v, %v", row, err)
	}

	if err := m.store.MarkDelivered(notice, time.Now()); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	loadStoredRows(t, m)
	runChildSweep(t, m)
	row, err := m.store.Get(kid.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !row.Archived || row.ArchivedAt.IsZero() {
		t.Fatalf("the sweep left a finished child its parent took in: %+v", row)
	}
	if m.tmux.Exists(kid.ID) {
		t.Fatal("the sweep filed the child with its pane still up")
	}
	if row.Status != status.Dead {
		t.Fatalf("status = %q, want dead as x leaves it", row.Status)
	}
	if local, _ := m.sessionByID(kid.ID); !local.Archived {
		t.Fatal("the row stayed on the active list after the sweep filed it")
	}
	if parentRow, err := m.store.Get(parent.ID); err != nil || parentRow.Archived || !m.tmux.Exists(parent.ID) {
		t.Fatalf("the sweep touched the parent: %+v, %v", parentRow, err)
	}
	if err := m.store.SetArchived(kid.ID, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

// A child with a session of its own still live stays -- working, or finished
// with nobody having taken that finish in. Once the pane under it has exited,
// the child goes and files that row along with it.
func TestTheChildSweepWaitsOnAChildsOwnFanOutAndThenTakesIt(t *testing.T) {
	m, parent, kid := finishedFanOut(t)
	grandkid := store.Session{
		ID: "gkid0001", Name: "grandkid", Tool: kid.Tool, Cwd: kid.Cwd, Group: kid.Group,
		Status: status.Working, ParentID: parent.ID, SpawnedBy: kid.ID,
	}
	if err := m.tmux.Create(grandkid.ID, grandkid.Cwd, "cat", nil, 80, 24); err != nil {
		t.Fatalf("create pane: %v", err)
	}
	t.Cleanup(func() { _ = m.tmux.Kill(grandkid.ID) })
	if err := m.store.CreateSession(grandkid); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	relayRest(t, m, parent, kid, true)
	loadStoredRows(t, m)
	runChildSweep(t, m)
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived {
		t.Fatalf("the sweep retired a child whose own spawn is working: %+v, %v", row, err)
	}

	if err := m.store.UpdateStatus(grandkid.ID, status.Finished); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)
	runChildSweep(t, m)
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived {
		t.Fatalf("the sweep retired a child over a live finished spawn: %+v, %v", row, err)
	}

	if err := m.tmux.Kill(grandkid.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := m.store.UpdateStatus(grandkid.ID, status.Dead); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	loadStoredRows(t, m)
	runChildSweep(t, m)
	for _, id := range []string{kid.ID, grandkid.ID} {
		row, err := m.store.Get(id)
		if err != nil || !row.Archived {
			t.Fatalf("%s was not filed with its fan-out: %+v, %v", id, row, err)
		}
		if m.tmux.Exists(id) {
			t.Fatalf("%s was filed with its pane still up", id)
		}
	}
}

func TestTheChildSweepHonoursKeepAndTheBoardWideOptOut(t *testing.T) {
	m, parent, kid := finishedFanOut(t)
	relayRest(t, m, parent, kid, true)
	m.cfg.Children.KeepFinished = true
	runChildSweep(t, m)
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived {
		t.Fatalf("keep_finished did not stop the sweep: %+v, %v", row, err)
	}
	m.cfg.Children.KeepFinished = false
	if err := m.store.SetKeepChild(kid.ID, true); err != nil {
		t.Fatalf("SetKeepChild: %v", err)
	}
	runChildSweep(t, m)
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived {
		t.Fatalf("a child spawned with keep was swept: %+v, %v", row, err)
	}
	if !m.tmux.Exists(kid.ID) {
		t.Fatal("a kept child's pane was ended")
	}
}

// A child sent a follow-up between the sweep reading it and the answer
// landing is working again, and the landing must not end it.
func TestTheChildSweepRechecksTheRowBeforeEndingIt(t *testing.T) {
	m, parent, kid := finishedFanOut(t)
	relayRest(t, m, parent, kid, true)
	answer := armChildSweep(t, m)()
	msg, ok := answer.(childSweptMsg)
	if !ok || len(msg.retire) != 1 {
		t.Fatalf("the sweep answered %#v, want one child to retire", answer)
	}
	for i := range m.sessions {
		if m.sessions[i].ID == kid.ID {
			m.sessions[i].Status = status.Working
		}
	}
	m.applyChildSweep(msg)
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived || !m.tmux.Exists(kid.ID) {
		t.Fatalf("the landing ended a child that had gone back to work: %+v, %v", row, err)
	}
}
