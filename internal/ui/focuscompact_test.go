package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A burst of sizes resizes the sessions once, for the size that held.
func TestResizeWaitsForTheSizeToSettle(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "sized", t.TempDir(), "")
	id := m.sessionRows()[0].ID
	m.resizeNow(t)
	before, _ := windowSize(t, id)

	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 150, Height: 45})
	*m = *updated.(*Model)
	if cmd == nil {
		t.Fatal("a resize armed no settle timer")
	}
	if m.pane.resizing {
		t.Fatal("the sessions were resized before the size settled")
	}
	stale := m.resizeSeq
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	*m = *updated.(*Model)
	// The first size's timer fires after the second size arrived: dropped.
	updated, cmd = m.Update(resizeSettleMsg{seq: stale})
	*m = *updated.(*Model)
	if cmd != nil || m.pane.resizing {
		t.Fatal("a stale settle timer resized the sessions")
	}
	if w, _ := windowSize(t, id); w != before {
		t.Fatalf("session was resized to %d before any size settled", w)
	}
	// The current size's timer does the work.
	updated, cmd = m.Update(resizeSettleMsg{seq: m.resizeSeq})
	*m = *updated.(*Model)
	if cmd == nil {
		t.Fatal("the settled size resized nothing")
	}
	updated, _ = m.Update(cmd())
	*m = *updated.(*Model)
	m.settleResize(t)
	wantW, wantH := m.previewPaneWidth(), m.previewPaneHeight()
	if w, h := windowSize(t, id); w != wantW || h != wantH {
		t.Fatalf("after settling, window = %dx%d, want %dx%d", w, h, wantW, wantH)
	}
}
