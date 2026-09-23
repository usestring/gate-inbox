package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
)

// queuedKillFleet is a triage queue deep enough to tell the head of it from
// the row under the one being killed: ask and zap both wait, ask for longer,
// and broke's error sits below them both. Killing zap therefore has one
// answer if the drain resumes at the head (ask) and a different one if it
// walks on from where the killed row stood (broke).
func queuedKillFleet(t *testing.T) *Model {
	t.Helper()
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"ask":   status.Waiting,
		"broke": status.Errored,
		"zap":   status.Waiting,
	})
	m.rebuildRows()
	return m
}

func confirmKill(t *testing.T, m *Model) *Model {
	t.Helper()
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	return updated.(*Model)
}

// A kill is not a handover. The operator answered nothing, so there is no
// place in the queue to carry on from, and the drain goes back to the work
// that has been waiting longest rather than to whatever fell under the row
// that just left.
func TestKillingInsideTriageResumesAtTheQueueHead(t *testing.T) {
	m := queuedKillFleet(t)
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "zap")

	updated, _ := m.handleKey(ctrlX())
	m = confirmKill(t, updated.(*Model))

	if m.mode != modeFocus {
		t.Fatalf("the kill dropped out of the drain into mode %v: %s", m.mode, m.errBar.text)
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("the drain resumed on %q, want the head of the queue", got)
	}
}

// The same request made from the list. In triage the board is one queue
// being drained, so taking a session off it carries on into the queue
// wherever the key was pressed.
func TestKillingFromTheListInTriageEntersTheQueue(t *testing.T) {
	m := queuedKillFleet(t)
	m.triage = true
	m.rebuildRows()
	m.selectSessionRow(t, "zap")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = confirmKill(t, updated.(*Model))

	if m.mode != modeFocus {
		t.Fatalf("a kill in triage left mode %v rather than the next session: %s", m.mode, m.errBar.text)
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("the drain resumed on %q, want the head of the queue", got)
	}
}

// Outside triage the list keeps its old answer: a kill files the row away
// and leaves the operator on the list, entering nothing.
func TestKillingFromTheListOutsideTriageStaysOnTheList(t *testing.T) {
	m := queuedKillFleet(t)
	m.selectSessionRow(t, "zap")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = confirmKill(t, updated.(*Model))

	if m.mode != modeList {
		t.Fatalf("a kill outside triage left mode %v rather than the list", m.mode)
	}
}

// Asking for the queue is asking for the work at the head of it, so turning
// triage on opens that session rather than a list to press enter on.
func TestTurningTriageOnEntersTheHeadOfTheQueue(t *testing.T) {
	m := queuedKillFleet(t)
	m.applyCmd(t, m.toggleTriage())

	if m.mode != modeFocus {
		t.Fatalf("triage opened in mode %v rather than in its first session: %s", m.mode, m.errBar.text)
	}
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("triage opened %q, want the head of the queue", got)
	}
}

// A queue with nothing waiting and nothing idle has no head to enter, and
// triage still has to be reachable as a plain reordering of the board. An
// idle session would be entered; see TestTriageHeadFallsBackToIdle.
func TestTurningTriageOnWithNothingWaitingEntersNothing(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{
		"busy":  status.Working,
		"other": status.Working,
	})
	m.applyCmd(t, m.toggleTriage())

	if m.mode != modeList {
		t.Fatalf("triage entered mode %v with nothing on its queue", m.mode)
	}
	if !m.triage {
		t.Fatal("triage did not turn on")
	}
}
