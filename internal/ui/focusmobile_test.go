package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// alt+↑ walks a plain pane back through its history and alt+end returns it
// to the live bottom, for the operator whose terminal has no wheel.
func TestFocusAltArrowsScrollHistory(t *testing.T) {
	m, _ := focusedWithHistory(t, "keyscroller")
	if !strings.Contains(m.preview, "history-line-120") {
		t.Fatalf("live preview missing the newest line: %q", m.preview)
	}
	scrolled := m.preview
	for i := 0; i < 20 && strings.Contains(scrolled, "history-line-120"); i++ {
		before := m.focusScroll
		updated, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
		*m = *updated.(*Model)
		if m.focusScroll <= before {
			t.Fatalf("alt+up did not move the pane back from %d", before)
		}
		if cmd != nil {
			updated, _ = m.Update(cmd())
			*m = *updated.(*Model)
		}
		scrolled = m.preview
	}
	if strings.Contains(scrolled, "history-line-120") {
		t.Fatalf("alt+up never reached older output:\n%s", scrolled)
	}
	if !strings.Contains(ansi.Strip(m.frame()), "lines back") {
		t.Fatal("the focus rule does not say how far back the pane is")
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModAlt})
	*m = *updated.(*Model)
	if m.focusScroll != 0 {
		t.Fatalf("alt+end left the pane %d lines back", m.focusScroll)
	}
	if cmd != nil {
		updated, _ = m.Update(cmd())
		*m = *updated.(*Model)
	}
	if !strings.Contains(m.preview, "history-line-120") {
		t.Fatalf("bottom does not show the newest line:\n%s", m.preview)
	}
}

// alt+pgup moves a whole pane at a time.
func TestFocusAltPageScrollsAPane(t *testing.T) {
	m, _ := focusedWithHistory(t, "pager")
	rows := m.previewPaneHeight()
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModAlt})
	*m = *updated.(*Model)
	if want := rows - 1; m.focusScroll != want {
		t.Fatalf("alt+pgup moved %d lines on a %d-row pane, want %d", m.focusScroll, rows, want)
	}
}

// One wheel notch and one alt+arrow each move a fixed few lines whatever
// the terminal's height: a step that scaled with the pane made a flick on
// a tall screen jump most of the history.
func TestFocusScrollStepIsFixedAcrossPaneHeights(t *testing.T) {
	m, _ := focusedWithHistory(t, "stepper")
	for _, height := range []int{24, 80, 200} {
		m.width, m.height = 120, height
		m.pane.geom = nil
		m.focusScroll = 0
		// Each height starts with no read out. Nothing drains the replies
		// here -- this is about the step, not the scheduling -- and one read
		// at a time is what keeps a burst from forking a capture per notch.
		m.focusReading = false
		m.pane.history = 1000
		if cmd := m.scrollFocus(-1); cmd == nil {
			t.Fatalf("a wheel notch on a %d-row terminal did not scroll", height)
		}
		if m.focusScroll != focusScrollStep {
			t.Fatalf("a wheel notch moved %d lines on a %d-row terminal, want %d", m.focusScroll, height, focusScrollStep)
		}
		updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
		*m = *updated.(*Model)
		if m.focusScroll != 2*focusScrollStep {
			t.Fatalf("alt+up moved %d lines on a %d-row terminal, want %d", m.focusScroll-focusScrollStep, height, focusScrollStep)
		}
	}
}

// A wheel notch on the chrome around a mouse-owning pane still reaches the
// pane, at its nearest cell.
func TestNearestPaneCellPullsThePointerInside(t *testing.T) {
	m := &Model{}
	m.pane.box = paneBox{x: 10, y: 5, width: 40, height: 10, ok: true}
	cases := []struct{ x, y, row, col int }{
		{0, 0, 0, 0},
		{25, 0, 0, 15},
		{200, 200, 9, 39},
		{12, 7, 2, 2},
	}
	for _, c := range cases {
		row, col, ok := m.nearestPaneCell(c.x, c.y)
		if !ok || row != c.row || col != c.col {
			t.Errorf("(%d,%d) -> row %d col %d ok %v, want row %d col %d", c.x, c.y, row, col, ok, c.row, c.col)
		}
	}
	if _, _, ok := m.paneCell(0, 0); ok {
		t.Fatal("paneCell itself must still refuse a pointer outside the box")
	}
}

// A resize while scrolled back re-reads the region at the new size and
// keeps the offset inside the history the pane has.
func TestResizeReclampsAndRereadsAScrolledPane(t *testing.T) {
	m, _ := focusedWithHistory(t, "resizer")
	m.focusScroll = m.pane.history + 50
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height - 5})
	*m = *updated.(*Model)
	if m.focusScroll != m.pane.history {
		t.Fatalf("offset %d after the resize, history is %d", m.focusScroll, m.pane.history)
	}
	if cmd == nil {
		t.Fatal("a resize of a scrolled pane produced no re-read")
	}
	// A pane whose history shrank under the offset is pulled back too.
	m.focusScroll = 40
	var facts paneFacts
	facts.historySize = 10
	m.storePaneState(m.rows[m.cursor].sess.ID, facts)
	if m.focusScroll != 10 {
		t.Fatalf("offset %d after the history shrank to 10", m.focusScroll)
	}
}
