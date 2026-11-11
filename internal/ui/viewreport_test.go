package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension"
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
