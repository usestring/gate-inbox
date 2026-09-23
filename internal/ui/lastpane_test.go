package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// focusRow puts the cursor on a named session and focuses it, the two keys
// an operator presses to arrive in a pane.
func focusRow(t testing.TB, m *Model, name string) {
	t.Helper()
	m.selectSessionRow(t, name)
	_, cmd := m.focusSelected()
	if m.mode != modeFocus {
		t.Fatalf("focusing %q left mode %v, err = %q", name, m.mode, m.errBar.text)
	}
	m.applyCmd(t, cmd)
}

// The pair swaps: l from B lands on A, and l again comes back to B. An
// operator crossing between two sessions presses one key each way rather
// than walking a history that only goes one direction.
func TestLastPaneSwapsBetweenTheTwoSessions(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	createSession(t, m, "beta", dir, "")

	focusRow(t, m, "alpha")
	m.leaveFocusForFixture(t)
	focusRow(t, m, "beta")
	m.leaveFocusForFixture(t)

	back, cmd := m.focusLastPane()
	m.applyCmd(t, cmd)
	if got := lastPaneName(t, back.(*Model)); got != "alpha" {
		t.Fatalf("l from beta focused %q, want alpha (err %q)", got, m.errBar.text)
	}
	m.leaveFocusForFixture(t)

	forth, cmd := m.focusLastPane()
	m.applyCmd(t, cmd)
	if got := lastPaneName(t, forth.(*Model)); got != "beta" {
		t.Errorf("l again focused %q, want beta (err %q)", got, m.errBar.text)
	}
}

// Re-entering the session already focused does not make it its own
// predecessor. Without this, leaving A and going straight back into it would
// set the pair to A and A, and the key would stop crossing anywhere.
func TestRefocusingTheSameSessionKeepsThePair(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	createSession(t, m, "beta", dir, "")

	focusRow(t, m, "alpha")
	m.leaveFocusForFixture(t)
	focusRow(t, m, "beta")
	m.leaveFocusForFixture(t)
	focusRow(t, m, "beta")
	m.leaveFocusForFixture(t)

	back, cmd := m.focusLastPane()
	m.applyCmd(t, cmd)
	if got := lastPaneName(t, back.(*Model)); got != "alpha" {
		t.Errorf("after re-entering beta, l focused %q, want alpha (err %q)", got, m.errBar.text)
	}
}

// A refused focus is not a session the operator was on, so it must not
// displace the one they actually came from.
func TestARefusedFocusDoesNotEnterThePair(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	createSession(t, m, "beta", dir, "")

	focusRow(t, m, "alpha")
	m.leaveFocusForFixture(t)
	focusRow(t, m, "beta")
	m.leaveFocusForFixture(t)

	// Archived rows refuse focus, and t is what puts one back on the board
	// to aim at.
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	m.applyCmd(t, m.confirmAnswer(t))
	m.showArchived = true
	loadStoredRows(t, m)
	m.selectSessionRow(t, "alpha")
	if _, cmd := m.focusSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode == modeFocus {
		t.Fatal("an archived row accepted focus; the fixture no longer refuses")
	}
	if m.prevFocusID != m.sessionID(t, "alpha") {
		t.Error("a refused focus rewrote the pair")
	}
}

// The pair is held as session ids, so a rail rebuilt under the operator --
// a poll reordering it, a fold, a filter -- still sends l to the same agent
// rather than to whoever now sits at that row.
func TestLastPaneSurvivesTheRowsBeingRebuilt(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	createSession(t, m, "beta", dir, "")
	createSession(t, m, "gamma", dir, "")

	focusRow(t, m, "alpha")
	m.leaveFocusForFixture(t)
	focusRow(t, m, "gamma")
	m.leaveFocusForFixture(t)

	m.selectSessionRow(t, "alpha")
	if _, cmd := m.reorderSelected(1); cmd != nil {
		m.applyCmd(t, cmd)
	}
	m.rebuildRows()

	back, cmd := m.focusLastPane()
	m.applyCmd(t, cmd)
	if got := lastPaneName(t, back.(*Model)); got != "alpha" {
		t.Errorf("after a reorder, l focused %q, want alpha (err %q)", got, m.errBar.text)
	}
}

// Nothing to go back to is a refusal, not a jump to whatever row is nearest:
// the next key an operator presses goes into a pane, so landing in the wrong
// one is worse than landing nowhere.
func TestLastPaneRefusesWithNothingBehindIt(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")

	if _, cmd := m.focusLastPane(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode == modeFocus {
		t.Fatal("l focused a session with no previous one")
	}
	if !strings.Contains(m.errBar.text, "before") {
		t.Errorf("bar said %q; it should say there is nothing behind the key", m.errBar.text)
	}
}

// A previous session that has left the board -- archived, or gone -- is out
// of the pair, and l says so instead of guessing.
func TestLastPaneRefusesWhenThePreviousSessionIsGone(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	createSession(t, m, "beta", dir, "")

	focusRow(t, m, "alpha")
	m.leaveFocusForFixture(t)
	focusRow(t, m, "beta")
	m.leaveFocusForFixture(t)

	m.selectSessionRow(t, "alpha")
	if _, cmd := m.archiveSelected(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	m.applyCmd(t, m.confirmAnswer(t))

	if _, cmd := m.focusLastPane(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if m.mode == modeFocus {
		t.Fatal("l focused an archived session")
	}
	if !strings.Contains(m.errBar.text, "not on the board") {
		t.Errorf("bar said %q; it should say the session has left the board", m.errBar.text)
	}
}

// The footer names the key only once there is a pair to swap between, so it
// never advertises one that would refuse.
func TestTheFooterNamesLastPaneOnlyOnceItWorks(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	createSession(t, m, "alpha", dir, "")
	if legendHasPair(m.viewLegend(), "l") {
		t.Error("the footer named l with nothing behind it")
	}
	createSession(t, m, "beta", dir, "")
	focusRow(t, m, "alpha")
	m.leaveFocusForFixture(t)
	focusRow(t, m, "beta")
	m.leaveFocusForFixture(t)
	if !legendHasPair(m.viewLegend(), "l") {
		t.Error("the footer did not name l once a pair existed")
	}
}

func legendHasPair(section legendSection, key string) bool {
	for _, pair := range section.pairs {
		if pair[0] == key {
			return true
		}
	}
	return false
}

// lastPaneName is the name of the session the model is focused on.
func lastPaneName(t testing.TB, m *Model) string {
	t.Helper()
	if m.mode != modeFocus {
		return ""
	}
	sess, ok := m.selected()
	if !ok {
		t.Fatal("focus mode with no selected session")
	}
	return sess.Name
}

// sessionID resolves a fixture session's id from its name.
func (m *Model) sessionID(t testing.TB, name string) string {
	t.Helper()
	for _, sess := range m.sessions {
		if sess.Name == name {
			return sess.ID
		}
	}
	t.Fatalf("no session named %q", name)
	return ""
}

// confirmAnswer says yes to the dialog on screen, ticking its box first when
// it has one.
func (m *Model) confirmAnswer(t testing.TB) tea.Cmd {
	t.Helper()
	if m.mode != modeConfirmDelete {
		t.Fatalf("no confirmation on screen; mode is %v", m.mode)
	}
	if m.confirm.ack != "" {
		m.handleConfirmKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	}
	_, cmd := m.handleConfirmKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	return cmd
}
