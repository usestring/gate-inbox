package ui

import (
	"github.com/usestring/gate-inbox/internal/keymap"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// jumpKey builds the presses the status jumps are bound to, which key()
// cannot: it makes a plain rune, and every jump but tab carries a modifier.
// The binding is looked up by the string bubbletea reports, so each press is
// checked against the name the table is keyed by before it is used -- a
// modifier that stopped arriving would otherwise read as a jump that stopped
// finding anything.
func jumpKey(t *testing.T, name string) tea.KeyPressMsg {
	t.Helper()
	var msg tea.KeyPressMsg
	switch name {
	case "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	default:
		msg = tea.KeyPressMsg{Code: []rune(name)[len(name)-1], Mod: tea.ModAlt}
	}
	if got := msg.String(); got != name {
		t.Fatalf("press reads as %q, want %q: the table is keyed by this string", got, name)
	}
	return msg
}

// jumpFor is the table entry a key press lands on, resolved the way the
// handler resolves it: the press names an action on the list, and the action
// names the jump. Going through the key map is the point -- a jump is a
// rebindable binding like any other, so a test that read the table by key
// name would pass over a family the map no longer reaches.
func (m *Model) jumpFor(t *testing.T, name string) statusJump {
	t.Helper()
	action, bound := m.action(keymap.ContextList, jumpKey(t, name))
	if !bound {
		t.Fatalf("no action bound to %q", name)
	}
	jump, ok := statusJumps[action]
	if !ok {
		t.Fatalf("%q is %s, which is not a status jump", name, action)
	}
	return jump
}

// jumpTo is the name of the session a jump walks to next, or "" for a walk
// with nothing to find. It starts from the key press rather than from the
// table entry, so every case below exercises the whole lookup: the press,
// the string it reports as, and the jump that string names.
func (m *Model) jumpTo(t *testing.T, name string) string {
	t.Helper()
	index, ok := m.nextJumpRow(m.jumpFor(t, name), map[string]bool{})
	if !ok {
		return ""
	}
	return m.rows[index].sess.Name
}

// The whole point of the family: one key per state, landing on the next
// session wearing that mark rather than on whatever is next in the tree.
func TestEachStatusJumpWalksToItsOwnState(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "old-block")

	for _, tc := range []struct{ key, want string }{
		{"alt+w", "new-block"},
		{"alt+f", "reviewme"},
		{"alt+e", "crashed"},
		{"alt+i", "napping"},
		{"alt+k", "grinder"},
	} {
		if got := m.jumpTo(t, tc.key); got != tc.want {
			t.Errorf("%s from old-block = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// A jump wraps, so a drain started halfway down the list still reaches the
// rows above the cursor rather than stopping at the bottom.
func TestAStatusJumpWrapsPastTheEndOfTheList(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "new-block")

	if got := m.jumpTo(t, "alt+w"); got != "old-block" {
		t.Fatalf("alt+w from the last waiting session = %q, want old-block from the top", got)
	}
}

// The row under the cursor is the one place a jump never stops. Pressing the
// key on a finished session is a request for a different one, and the
// session just answered reads as finished for the poll or so it takes the
// pane to change.
func TestAStatusJumpNeverStopsOnTheRowItStartsOn(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "reviewme")

	if got := m.jumpTo(t, "alt+f"); got != "" {
		t.Fatalf("alt+f on the only finished session = %q, want nowhere to go", got)
	}
}

// Errored and dead wear the same mark in the status column, so the key named
// after that mark answers for both. A dead pane refuses to be entered, and
// the walk carries on to the next candidate rather than ending there.
func TestTheErroredJumpAnswersForDeadPanesToo(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "crashed")

	if got := m.jumpTo(t, "alt+e"); got != "gone" {
		t.Fatalf("alt+e past the errored session = %q, want the dead one", got)
	}
}

// tab is the drain: what needs a person, in either direction, whatever the
// individual state.
func TestTabWalksEverythingThatNeedsAPerson(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "crashed")

	if got := m.jumpTo(t, "tab"); got != "reviewme" {
		t.Fatalf("tab = %q, want the finished session below crashed", got)
	}
	if got := m.jumpTo(t, "shift+tab"); got != "new-block" {
		t.Fatalf("shift+tab = %q, want the waiting session above crashed", got)
	}
}

// A muted row is one the operator has said is not theirs this pass, and tab
// walks that queue -- but a key named after a state answers for the state.
// Somebody asking for the next finished session is asking about the mark on
// the row, not about their queue.
func TestOnlyTheAttentionWalkSkipsAMutedRow(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "crashed")
	for _, sess := range m.sessions {
		if sess.Name == "reviewme" {
			m.mute(sess)
		}
	}

	if got := m.jumpTo(t, "tab"); got == "reviewme" {
		t.Fatal("tab handed back a muted row")
	}
	if got := m.jumpTo(t, "alt+f"); got != "reviewme" {
		t.Fatalf("alt+f = %q, want the muted finished session it names", got)
	}
}

// A fold is a browsing convenience, so a jump that reported nothing while a
// waiting session sat inside a folded group would be lying about the fleet
// rather than about the view. The fold opens only when the rows on screen
// have nothing.
func TestAStatusJumpOpensAFoldToReachASession(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.collapsed["beta"] = true
	m.rebuildRows()
	m.selectSessionRow(t, "old-block")

	if got := m.jumpTo(t, "alt+w"); got != "" {
		t.Fatalf("the folded group left %q on screen; this test needs it hidden", got)
	}
	sess, ok := m.foldedJumpTarget(m.jumpFor(t, "alt+w"), nil)
	if !ok {
		t.Fatal("the waiting session inside the folded group was not found")
	}
	if sess.Name != "new-block" {
		t.Fatalf("folded target = %q, want new-block", sess.Name)
	}
	index, shown := m.revealSession(sess)
	if !shown {
		t.Fatal("revealing the session did not put it on a row")
	}
	if got := m.rows[index].sess.Name; got != "new-block" {
		t.Fatalf("revealed row = %q, want new-block", got)
	}
	if m.collapsed["beta"] {
		t.Fatal("the group is still folded over the row the jump landed on")
	}
}

// A search is a narrowing the operator typed, and a jump is not a request to
// leave it: the fold pass is what could walk outside the query, so it is the
// half that stands down.
func TestAStatusJumpStaysInsideASearch(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.search = "block"
	m.rebuildRows()

	if _, ok := m.foldedJumpTarget(m.jumpFor(t, "alt+f"), nil); ok {
		t.Fatal("the jump reached for a session the search had filtered away")
	}
}

// The key press has to reach the walk from the list's own handler, and a
// walk that entered nothing has to say why. Nothing here has a live pane, so
// every candidate refuses -- and the refusal's own message is the useful one.
func TestTheJumpKeyIsRoutedAndLandsOnTheRow(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "old-block")

	updated, _ := m.handleKey(jumpKey(t, "alt+f"))
	m = updated.(*Model)
	if got := m.rows[m.cursor].sess.Name; got != "reviewme" {
		t.Fatalf("alt+f left the cursor on %q, want reviewme", got)
	}
	if m.errBar.text != m.deadSessionHint() {
		t.Fatalf("error bar = %q, want the reason the pane refused", m.errBar.text)
	}
}

// A state nothing is in says so, in the words the status column uses, rather
// than reading as a key that did nothing.
func TestAJumpWithNothingToFindSaysSo(t *testing.T) {
	m := buildModel(t)
	seedTriageFleet(t, m)
	m.selectSessionRow(t, "reviewme")

	updated, _ := m.handleKey(jumpKey(t, "alt+f"))
	m = updated.(*Model)
	if m.errBar.text != "nothing listed is finished" {
		t.Fatalf("error bar = %q, want the empty-walk message", m.errBar.text)
	}
}

// The table is the documentation as well as the binding, so a state that
// gains a jump has to gain a label with it.
func TestEveryStatusJumpIsLabelledAndDirected(t *testing.T) {
	for name, jump := range statusJumps {
		if jump.label == "" {
			t.Errorf("%s has no label for its empty-walk message", name)
		}
		if jump.delta != 1 && jump.delta != -1 {
			t.Errorf("%s walks by %d, want one row at a time", name, jump.delta)
		}
		if jump.wants == nil {
			t.Errorf("%s looks for nothing", name)
		}
	}
	// Every state a session can rest in has a key. Starting and dead are the
	// two that ride with another one rather than owning a key of their own,
	// and TestTheErroredJumpAnswersForDeadPanesToo holds the pairing.
	m := buildModel(t)
	for _, state := range []string{status.Waiting, status.Finished, status.Errored, status.Idle, status.Working} {
		found := false
		for _, jump := range statusJumps {
			if jump.wants(m, store.Session{Status: state}) {
				found = true
			}
		}
		if !found {
			t.Errorf("no jump reaches a %s session", state)
		}
	}
}
