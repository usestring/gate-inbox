package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// hibernatedRow is a board session whose pane hibernate stopped: the row
// dead in the store and in m.sessions, its id on the hibernated set.
func hibernatedRow(t *testing.T, m *Model, name string) store.Session {
	t.Helper()
	createSession(t, m, name, t.TempDir(), "")
	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if err := m.store.UpdateStatus(sess.ID, status.Dead); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if err := m.store.AddHibernated([]string{sess.ID}); err != nil {
		t.Fatalf("AddHibernated: %v", err)
	}
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].Status = status.Dead
		}
	}
	m.hibernated = []string{sess.ID}
	m.rebuildRows()
	return m.sessionRows()[0]
}

// Focusing a hibernated session is asking for it back. It wakes on its
// conversation and focus lands on its pane, rather than on the dead-row
// hint that sent people to revive it by hand.
func TestFocusingAHibernatedSessionWakesIt(t *testing.T) {
	m := buildModel(t)
	sess := hibernatedRow(t, m, "sleeper")

	m.focusSelected()

	if !m.tmux.Exists(sess.ID) {
		t.Fatalf("focus did not wake the hibernated session; err bar %q", m.errBar.text)
	}
	if m.mode != modeFocus {
		t.Fatalf("mode = %v, want focus on the woken pane", m.mode)
	}
	if still, _ := m.store.IsHibernated(sess.ID); still {
		t.Fatal("the woken session is still marked hibernated")
	}
	if got := m.shownStatus(m.sessionRows()[0]); got == status.Dead {
		t.Fatal("the woken row reads dead until the next refresh")
	}
}

// Attaching wakes it the same way.
func TestAttachingAHibernatedSessionWakesIt(t *testing.T) {
	m := buildModel(t)
	sess := hibernatedRow(t, m, "sleeper")

	_, cmd := m.attachSelected()

	if !m.tmux.Exists(sess.ID) || cmd == nil {
		t.Fatalf("attach did not wake and attach the hibernated session (cmd %v); err bar %q", cmd != nil, m.errBar.text)
	}
}

// A row that really died is not woken by focus: it still gets the hint.
func TestFocusingADeadSessionStillGetsTheHint(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "gone", t.TempDir(), "")
	sess := m.sessionRows()[0]
	if err := m.tmux.Kill(sess.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}

	m.focusSelected()

	if m.tmux.Exists(sess.ID) {
		t.Fatal("focus revived a session hibernate never stopped")
	}
	if m.errBar.text != m.deadSessionHint() {
		t.Fatalf("err bar = %q, want the dead-row hint", m.errBar.text)
	}
}

// The board draws a hibernated row as hibernating, never dead, and triage
// ranks it below everything that could use the operator. A dead row beside
// it keeps its dead mark.
func TestAHibernatedRowShowsHibernatingAndTriageDemotesIt(t *testing.T) {
	m := &Model{hibernated: []string{"sleep001"}}
	sleeping := store.Session{ID: "sleep001", Status: status.Dead}
	gone := store.Session{ID: "gone0001", Status: status.Dead}

	if got := m.shownStatus(sleeping); got != status.Hibernating {
		t.Fatalf("shownStatus = %s, want hibernating", got)
	}
	if got := m.shownStatus(gone); got != status.Dead {
		t.Fatalf("shownStatus of a dead row = %s, want dead", got)
	}
	if statusGlyph(status.Hibernating) == statusGlyph(status.Dead) {
		t.Fatal("a hibernated row wears the dead mark")
	}
	if got := m.triageStateOf(sleeping); got != status.Hibernating {
		t.Fatalf("triage state = %s, want hibernating", got)
	}
	if requiresInput(m.triageStateOf(sleeping)) {
		t.Fatal("a hibernated row counts as needing the operator")
	}
	if triageRank(status.Hibernating) <= triageRank(status.Starting) {
		t.Fatal("triage ranks a hibernated row above a starting one")
	}
}
