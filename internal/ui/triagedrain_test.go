package ui

import (
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The drain, end to end, over real panes: a tmux-backed session whose status
// comes from the agent's own hook file, a real poll pass deriving it, real
// keystrokes forwarded into the pane, and the store read back afterwards.
// The unit tests beside this one pin the acknowledgement itself; these are
// about what the operator sees while they work in the session.

// liveHookedSession creates a real session on the claude-hooked tool, which
// is the one whose status comes from a hook file the way a real claude's
// does, so a test can raise a turn end the way the agent raises it.
func liveHookedSession(t *testing.T, m *Model, name string) store.Session {
	t.Helper()
	m.openForm()
	m.form.name.SetValue(name)
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = 1 // claude-hooked
	pickGroup(t, m, "")
	_, cmd := m.submitForm()
	if m.mode != modeFocus {
		t.Fatalf("creating %q left mode %v: %s", name, m.mode, m.errBar.text)
	}
	m.applyCmd(t, cmd)
	m.leaveFocusForFixture(t)
	for _, sess := range m.sessionRows() {
		if sess.Name == name {
			// A pane with nothing painted on it is a session still booting as
			// far as the poller is concerned, and it holds every session at
			// starting for the grace window. A real agent has printed its
			// answer by the time its turn ends, so the fixture paints one.
			if err := m.tmux.SendText(sess.ID, "here is the result"); err != nil {
				t.Fatalf("paint %q: %v", name, err)
			}
			waitForPaneText(t, m, sess.ID, "here is the result")
			return sess
		}
	}
	t.Fatalf("session %q is not on the board", name)
	return store.Session{}
}

// requireStatus is the precondition every drain test starts from: the poll
// pass has read the hook file and the board agrees the session needs a
// person. Without it a fixture that never left "starting" fails later, in
// an assertion about something else.
func requireStatus(t *testing.T, m *Model, name, want string) {
	t.Helper()
	for attempt := 0; attempt < 25; attempt++ {
		if got := storedSession(t, m, name); got.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
		m.applyCmd(t, m.refreshCmd())
	}
	t.Fatalf("%s never reached %q (stuck at %q)", name, want, storedSession(t, m, name).Status)
}

// typeInto forwards characters into the focused pane through Update, the
// path a real keystroke takes.
func (m *Model) typeInto(t *testing.T, text string) {
	t.Helper()
	for _, r := range text {
		updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		*m = *updated.(*Model)
	}
	if m.errBar.text != "" {
		t.Fatalf("typing set an error: %s", m.errBar.text)
	}
}

func TestTriageDrainHoldsTheRowStillWhileTheOperatorAnswersIt(t *testing.T) {
	m := buildModel(t)
	first := liveHookedSession(t, m, "reviewme")
	second := liveHookedSession(t, m, "alsodone")
	writeHookStatus(t, m, first.ID, status.Finished)
	writeHookStatus(t, m, second.ID, status.Finished)

	m.triage = true
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "reviewme", status.Finished)
	requireStatus(t, m, "alsodone", status.Finished)
	// One more pass, so the queue is settled before it is read: the pass
	// that writes a new status patches the row it holds in memory but reads
	// the time it was written on the next list, and the queue's tiebreak is
	// that time. Everything after this point is the operator's own doing.
	m.applyCmd(t, m.refreshCmd())
	// Two turns that ended within the same poll pass are ordered by which
	// one the pass wrote first, so the drain is walked from whichever row
	// the queue actually put at its head.
	queue := sessionNames(m)
	if len(queue) != 2 {
		t.Fatalf("queue = %v want both sessions", queue)
	}
	head, next := queue[0], queue[1]

	m.selectSessionRow(t, head)
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("entering the head of the queue left mode %v: %s", m.mode, m.errBar.text)
	}

	// The operator reads the turn and starts typing an answer. Two poll
	// passes around the typing is what the reported bug needed: the first
	// wrote the acknowledgement through, and the row went idle and dropped
	// down the queue while they were still in it.
	m.applyCmd(t, m.refreshCmd())
	m.typeInto(t, "looks right, ship it")
	m.applyCmd(t, m.refreshCmd())

	if got := storedSession(t, m, head); got.Status != status.Finished || got.Acked {
		t.Fatalf("typing changed the state: status %q acked %v", got.Status, got.Acked)
	}
	if got := sessionNames(m); !slices.Equal(got, queue) {
		t.Fatalf("the queue reordered under the operator: %v want %v", got, queue)
	}
	if got := focusedName(t, m); got != head {
		t.Fatalf("the cursor moved to %q while typing", got)
	}

	// Handing it over is the act that says it has been dealt with.
	updated, cmd := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	runStoreCmd(t, cmd)
	if got := focusedName(t, m); got != next {
		t.Fatalf("the drain advanced to %q want %q", got, next)
	}
	m.applyCmd(t, m.refreshCmd())
	if got := storedSession(t, m, head); got.Status != status.Idle || !got.Acked {
		t.Fatalf("the handed-over session kept status %q acked %v, want idle and acked", got.Status, got.Acked)
	}
	// The session now being worked in holds its own alert, so the queue is
	// just as still one hop later.
	if got := storedSession(t, m, next); got.Status != status.Finished || got.Acked {
		t.Fatalf("the next session spent its alert on arrival: status %q acked %v", got.Status, got.Acked)
	}
}

// Leaving the drain rather than walking it is the same promise: the session
// the operator sat in is dealt with, and the one they never reached is not.
func TestTriageDrainLeavingSpendsOnlyTheSessionEntered(t *testing.T) {
	m := buildModel(t)
	entered := liveHookedSession(t, m, "entered")
	untouched := liveHookedSession(t, m, "untouched")
	writeHookStatus(t, m, entered.ID, status.Finished)
	writeHookStatus(t, m, untouched.ID, status.Finished)

	m.triage = true
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "entered", status.Finished)
	requireStatus(t, m, "untouched", status.Finished)
	m.selectSessionRow(t, "entered")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)

	updated, cmd := m.handleFocusKey(ctrlBackslash())
	m = updated.(*Model)
	runStoreCmd(t, cmd)
	if m.mode != modeList {
		t.Fatalf("ctrl+backslash left mode %v", m.mode)
	}
	m.applyCmd(t, m.refreshCmd())
	if got := storedSession(t, m, "entered"); got.Status != status.Idle || !got.Acked {
		t.Fatalf("the session left behind kept status %q acked %v", got.Status, got.Acked)
	}
	if got := storedSession(t, m, "untouched"); got.Status != status.Finished || got.Acked {
		t.Fatalf("a session never entered lost its alert: status %q acked %v", got.Status, got.Acked)
	}
}

// A session that answers, works and ends a second turn while the operator is
// reading it has raised an alert they have not seen. Leaving must leave that
// one standing, and the row must be back in the queue rather than muted out
// of the rest of the drain.
func TestTriageDrainKeepsATurnRaisedDuringTheVisit(t *testing.T) {
	m := buildModel(t)
	sess := liveHookedSession(t, m, "second-wind")
	writeHookStatus(t, m, sess.ID, status.Finished)

	m.triage = true
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "second-wind", status.Finished)
	m.selectSessionRow(t, "second-wind")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)

	m.typeInto(t, "one more thing")
	writeHookStatus(t, m, sess.ID, status.Working)
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "second-wind", status.Working)
	writeHookStatus(t, m, sess.ID, status.Finished)
	m.applyCmd(t, m.refreshCmd())

	updated, cmd := m.handleFocusKey(ctrlBackslash())
	m = updated.(*Model)
	runStoreCmd(t, cmd)
	m.applyCmd(t, m.refreshCmd())
	if got := storedSession(t, m, "second-wind"); got.Status != status.Finished || got.Acked {
		t.Fatalf("the new turn end was swallowed on the way out: status %q acked %v", got.Status, got.Acked)
	}
}

// A session that dies while the operator is in it must not leave the manager
// reporting a store error at them on the way back to the list.
func TestTriageDrainSurvivesTheSessionDyingUnderIt(t *testing.T) {
	m := buildModel(t)
	sess := liveHookedSession(t, m, "doomed")
	writeHookStatus(t, m, sess.ID, status.Finished)

	m.triage = true
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "doomed", status.Finished)
	m.selectSessionRow(t, "doomed")
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(*Model)

	if err := m.store.Delete(sess.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	updated, cmd := m.handleFocusKey(ctrlBackslash())
	m = updated.(*Model)
	runStoreCmd(t, cmd)
	if m.errBar.text != "" {
		t.Fatalf("leaving a dead session complained: %s", m.errBar.text)
	}
}
