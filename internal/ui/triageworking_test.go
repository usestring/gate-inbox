package ui

import (
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/status"
)

func TestTriageIgnoresPollResultsOlderThanTheWorkingStatus(t *testing.T) {
	for _, previous := range []string{status.Waiting, status.Finished} {
		t.Run(previous, func(t *testing.T) {
			m := smartFleet(t,
				smartSess("recent-turn", previous, time.Minute),
				smartSess("older-question", status.Waiting, time.Hour),
			)
			older := refreshMsg{sessions: slices.Clone(m.sessions), listedAt: smartNow}
			newer := refreshMsg{sessions: slices.Clone(m.sessions), listedAt: smartNow.Add(time.Second)}
			newer.sessions[0].Status = status.Working
			newer.sessions[0].LastStatusAt = newer.listedAt

			// A background result can wait to reach the UI while an explicit
			// refresh has already reported the submitted turn back at work.
			m.Update(newer)
			m.Update(older)
			if got := drainOrder(m); !slices.Equal(got, []string{"older-question"}) {
				t.Fatalf("a delayed poll put the working turn back in triage: %v", got)
			}

			finished := refreshMsg{sessions: slices.Clone(newer.sessions), listedAt: newer.listedAt.Add(time.Second)}
			finished.sessions[0].Status = status.Finished
			finished.sessions[0].LastStatusAt = finished.listedAt
			m.Update(finished)
			if got := drainOrder(m); !slices.Equal(got, []string{"older-question", "recent-turn"}) {
				t.Fatalf("the next fresh finish did not rejoin triage in state order: %v", got)
			}
		})
	}
}

// pollReporting applies a poll pass in which the named session reads st and
// every other row is as it was.
func pollReporting(t *testing.T, m *Model, name, st string) {
	t.Helper()
	sessions := slices.Clone(m.sessions)
	found := false
	for i := range sessions {
		if sessions[i].Name == name {
			sessions[i].Status = st
			sessions[i].LastStatusAt = time.Now()
			found = true
		}
	}
	if !found {
		t.Fatalf("session %q is not on the board", name)
	}
	updated, cmd := m.Update(refreshMsg{sessions: sessions, listedAt: time.Now()})
	*m = *updated.(*Model)
	runStoreCmd(t, cmd)
}

// The answer check after the key is the fast path, not the only one. An answer it never
// saw land -- a late Notification hook read as a fresh dialog, a call that
// outran the window -- left the operator stuck on a session the next poll
// plainly showed back at work. The poll's own transition is the evidence.
func TestTriageAdvancesWhenTheFocusedSessionGoesBackToWork(t *testing.T) {
	m := drainOnDialog(t, true)
	askID := focusedID(t, m)
	m = pressDialogKey(m)
	logHookEvent(t, m, askID, "waiting Notification")
	lookForLanding(t, m)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("precondition: the refused answer moved to %q", got)
	}

	pollReporting(t, m, "ask", status.Working)
	if m.mode != modeFocus {
		t.Fatalf("the transition dropped out of the queue: mode %v %s", m.mode, m.errBar.text)
	}
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("after the focused session went back to work, focused %q want %q", got, "next")
	}

	// The session it left is still at work on the next pass, which is no
	// transition: the operator stays on the session they were handed.
	pollReporting(t, m, "ask", status.Working)
	if got := focusedName(t, m); got != "next" {
		t.Fatalf("a second working pass moved focus to %q", got)
	}
}

// A session that was already working when the operator went into it has not
// changed under them, and only a change moves them on.
func TestTriageStaysOnASessionThatWasAlreadyWorking(t *testing.T) {
	m := buildModel(t)
	m.autoProceed = true
	liveTriageFleet(t, m, map[string]string{
		"busy": status.Working,
		"next": status.Waiting,
	})
	m.triage = true
	m.rebuildRows()
	m.enterFocusOn(t, "busy")

	pollReporting(t, m, "busy", status.Working)
	if got := focusedName(t, m); got != "busy" {
		t.Fatalf("a session that stayed working moved focus to %q", got)
	}
}

// Outside a drain there is no queue to advance along, and with auto-proceed
// off the operator asked to stop on each session.
func TestWorkingTransitionAdvancesOnlyAnAutoProceedingDrain(t *testing.T) {
	for _, c := range []struct {
		name         string
		auto, triage bool
	}{
		{"auto proceed off", false, true},
		{"triage off", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := buildModel(t)
			m.autoProceed = true
			liveTriageFleet(t, m, map[string]string{
				"ask":  status.Waiting,
				"next": status.Waiting,
			})
			m.triage = true
			m.rebuildRows()
			m.enterFocusOn(t, "ask")
			m.autoProceed, m.triage = c.auto, c.triage

			pollReporting(t, m, "ask", status.Working)
			if got := focusedName(t, m); got != "ask" {
				t.Fatalf("focus moved to %q", got)
			}
		})
	}
}

// A key after the answer that answers nothing says the operator is not done
// with the session, the way it does for an answer the check is still watching.
func TestTriageStaysWhenTheOperatorKeepsWorkingAfterTheAnswer(t *testing.T) {
	m := drainOnDialog(t, true)
	askID := focusedID(t, m)
	m = pressDialogKey(m)
	logHookEvent(t, m, askID, "waiting Notification")
	lookForLanding(t, m)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(*Model)

	pollReporting(t, m, "ask", status.Working)
	if got := focusedName(t, m); got != "ask" {
		t.Fatalf("a session the operator was still working in moved focus to %q", got)
	}
}
