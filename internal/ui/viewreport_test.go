package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/snippets"
)

func (r *recordingObserver) viewEvents() []extension.ViewEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]extension.ViewEvent(nil), r.views...)
}

func launchNamed(t *testing.T, m *Model, name string) {
	t.Helper()
	m.openForm()
	m.form.name.SetValue(name)
	m.form.dir.SetValue(t.TempDir())
	m.form.toolIndex = toolIndexOf(t, m, "claude-hooked")
	pickGroup(t, m, "")
	_, cmd := m.submitForm()
	m.applyCmd(t, cmd)
	m.leaveFocusForFixture(t)
}

// Extensions hear where the operator is once, then each key that moves the
// cursor, then going into a focused session and back to the list.
func TestBoardReportsWhereTheOperatorIs(t *testing.T) {
	m := buildModel(t)
	launchNamed(t, m, "first")
	launchNamed(t, m, "second")
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.selectSessionRow(t, "first")
	first := m.cursorSessionID()

	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m.enterFocusOn(t, "first")
	m.Update(nil)
	m.applyCmd(t, m.leaveFocus())
	m.Update(nil)

	got := observer.viewEvents()
	type seen struct {
		kind    extension.ViewKind
		place   extension.ViewPlace
		session string
		rows    int
	}
	var kinds []seen
	for _, e := range got {
		if e.At.IsZero() {
			t.Fatalf("event without a time: %+v", e)
		}
		kinds = append(kinds, seen{e.Kind, e.Place, e.SessionID, e.Rows})
	}
	if len(kinds) != 5 {
		t.Fatalf("events = %+v", kinds)
	}
	if kinds[0].kind != extension.ViewChanged || kinds[0].place != extension.ViewList {
		t.Errorf("first event = %+v, want where the operator starts", kinds[0])
	}
	if kinds[1].kind != extension.ViewCursor || kinds[1].rows <= 0 || kinds[1].session == first {
		t.Errorf("down = %+v", kinds[1])
	}
	if kinds[2].kind != extension.ViewCursor || kinds[2].rows >= 0 || kinds[2].session != first {
		t.Errorf("up = %+v", kinds[2])
	}
	if kinds[3] != (seen{extension.ViewChanged, extension.ViewFocus, first, 0}) {
		t.Errorf("focus = %+v", kinds[3])
	}
	if kinds[4] != (seen{extension.ViewChanged, extension.ViewList, "", 0}) {
		t.Errorf("leave = %+v", kinds[4])
	}
}

// Extensions hear each action the operator asks for, by key, snippet or
// palette, with whether triage or a search was on, and nothing for the keys
// that move the cursor or are typed into the search.
func TestBoardReportsTheActionsTheOperatorRuns(t *testing.T) {
	m := buildModel(t)
	launchNamed(t, m, "first")
	m.snips = snippets.Set{Snippets: snippets.Defaults()}
	observer := &recordingObserver{}
	m.ObserveBoard(observer)
	m.selectSessionRow(t, "first")
	first := m.cursorSessionID()

	m.Update(runeKey("ctrl+f"))
	m.Update(runeKey("x"))
	m.Update(namedKey(tea.KeyEscape))
	m.applyCmd(t, m.clearSearch())
	m.selectSessionRow(t, "first")
	m.openQuickMode()
	m.Update(runeKey("y"))
	m.quick.active = false
	m.triage = true
	m.Update(nil)
	_, cmd := m.runQuickAction(quickActionEntry{action: keymap.Mute})
	m.applyCmd(t, cmd)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	type seen struct {
		action, snippet string
		via             extension.ViewVia
		triage, filter  bool
		session         string
	}
	var actions []seen
	filtered, triaged := false, false
	for _, e := range observer.viewEvents() {
		switch e.Kind {
		case extension.ViewAction:
			actions = append(actions, seen{e.Action, e.Snippet, e.Via, e.Triage, e.Filtered, e.SessionID})
		case extension.ViewChanged:
			filtered = filtered || e.Filtered
			triaged = triaged || e.Triage
		}
	}
	if len(actions) == 3 {
		actions[2].session = "" // triage lists only what waits, so the row may be gone
	}
	want := []seen{
		{"search", "", extension.ViaKey, false, false, first},
		{extension.ActionSnippet, "y", extension.ViaMenu, false, false, first},
		{"mute", "", extension.ViaPalette, true, false, ""},
	}
	if len(actions) != len(want) {
		t.Fatalf("actions = %+v, want %+v", actions, want)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Errorf("action %d = %+v, want %+v", i, actions[i], want[i])
		}
	}
	if !filtered || !triaged {
		t.Errorf("ViewChanged reported search %v and triage %v, want both", filtered, triaged)
	}
}
