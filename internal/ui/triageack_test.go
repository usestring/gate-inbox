package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// runStoreCmd drives a command and whatever it batches, failing on the error
// message a deferred store write reports rather than dropping it.
func runStoreCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, one := range msg {
			runStoreCmd(t, one)
		}
	case errMsg:
		t.Fatalf("store write failed: %v", msg.err)
	}
}

func storedSession(t *testing.T, m *Model, name string) store.Session {
	t.Helper()
	for _, sess := range m.sessions {
		if sess.Name != name {
			continue
		}
		stored, err := m.store.Get(sess.ID)
		if err != nil {
			t.Fatalf("get %q: %v", name, err)
		}
		return stored
	}
	t.Fatalf("no session named %q", name)
	return store.Session{}
}

func TestTriageHoldsTheFinishedAlertUntilTheOperatorLeaves(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":  status.Waiting,
		"done": status.Finished,
	})
	m.triage = true
	m.rebuildRows()

	m.enterFocusOn(t, "done")
	if got := storedSession(t, m, "done"); got.Status != status.Finished || got.Acked {
		t.Fatalf("being handed the session already spent its alert: status %q acked %v", got.Status, got.Acked)
	}

	updated, cmd := m.handleFocusKey(ctrlBackslash())
	m = updated.(*Model)
	runStoreCmd(t, cmd)
	if got := storedSession(t, m, "done"); got.Status != status.Idle || !got.Acked {
		t.Fatalf("leaving left status %q acked %v, want idle and acked", got.Status, got.Acked)
	}
}

// The hold is only good for the turn it was raised by. A poll that reports
// the session working again -- the operator answered and the agent picked it
// up -- drops it, so whatever that turn ends on is raised as its own alert
// instead of being acknowledged by the visit that preceded it.
func TestTriageDropsTheHoldOnceTheSessionWorksAgain(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"done": status.Finished})
	m.triage = true
	m.rebuildRows()

	m.enterFocusOn(t, "done")
	id := storedSession(t, m, "done").ID
	if m.heldAckID != id {
		t.Fatalf("entering from the queue held %q, want %q", m.heldAckID, id)
	}

	for i := range m.sessions {
		if m.sessions[i].ID == id {
			m.sessions[i].Status = status.Working
		}
	}
	m.dropHeldAckOnNewTurn()
	if m.heldAckID != "" {
		t.Fatalf("the hold survived the session going back to work")
	}

	// The second turn ends: leaving now must leave that alert standing.
	if err := m.store.UpdateStatus(id, status.Finished); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	updated, cmd := m.handleFocusKey(ctrlBackslash())
	m = updated.(*Model)
	runStoreCmd(t, cmd)
	if got := storedSession(t, m, "done"); got.Status != status.Finished || got.Acked {
		t.Fatalf("the new turn end was swallowed: status %q acked %v", got.Status, got.Acked)
	}
}

// A session that leaves the board while it is being read has nothing left to
// acknowledge, and the hold must not outlive it into the next session.
func TestTriageDropsTheHoldWhenTheSessionLeavesTheBoard(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"done": status.Finished})
	m.triage = true
	m.rebuildRows()

	m.enterFocusOn(t, "done")
	m.sessions = nil
	m.dropHeldAckOnNewTurn()
	if m.heldAckID != "" {
		t.Fatalf("the hold outlived the session it was taken on")
	}
}

func TestFocusOutsideTriageAcknowledgesOnTheWayIn(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"done": status.Finished})
	m.rebuildRows()

	m.enterFocusOn(t, "done")
	if got := storedSession(t, m, "done"); got.Status != status.Idle || !got.Acked {
		t.Fatalf("entering left status %q acked %v, want idle and acked", got.Status, got.Acked)
	}
}
