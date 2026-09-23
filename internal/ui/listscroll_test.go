package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// listWithHistory is focusedWithHistory backed out to the list: the same
// session, with the same scrollback behind it, previewed rather than
// focused. Backing out of focus is how an operator gets here, and it is
// also what clears the offset the focused run left behind.
func listWithHistory(t testing.TB, name string) (*Model, string) {
	t.Helper()
	m, sessID := focusedWithHistory(t, name)
	// These fixtures exercise the native terminal transport, not transcript scrolling.
	m.conversation = nil
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	*m = *updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("did not leave focus: mode = %v", m.mode)
	}
	if m.focusScroll != 0 {
		t.Fatalf("leaving focus kept a scroll offset of %d", m.focusScroll)
	}
	seedLive(t, m, sessID)
	// The box the notch is hit-tested against is written while painting.
	m.frame()
	if !m.pane.box.ok {
		t.Fatal("the list painted no preview box to aim at")
	}
	m.pane.history = paneHistorySize(t, m, sessID)
	return m, sessID
}

// previewCell is a terminal cell in the middle of the painted preview.
func previewCell(m *Model) (x, y int) {
	return m.pane.box.x + m.pane.box.width/2, m.pane.box.y + m.pane.box.height/2
}

func wheel(up bool, x, y int) tea.MouseWheelMsg {
	button := tea.MouseWheelDown
	if up {
		button = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
}

// A notch over the preview walks the previewed pane back through its
// history without focusing it, and what lands on screen is what tmux holds
// at that offset -- the same guarantee the focused wheel carries.
func TestWheelOverThePreviewScrollsTheUnfocusedPane(t *testing.T) {
	m, sessID := listWithHistory(t, "listscroll")
	rows := m.previewPaneHeight()

	live := m.preview
	if !strings.Contains(live, "history-line-120") {
		t.Fatalf("preview is not at the live bottom: %q", firstLines(live))
	}

	x, y := previewCell(m)
	updated, cmd := m.handleMouse(wheel(true, x, y))
	m = updated.(*Model)
	if m.focusScroll == 0 {
		t.Fatal("a notch over the preview did not scroll it")
	}
	if m.mode != modeList {
		t.Fatal("scrolling the preview focused the session")
	}
	if cmd == nil {
		t.Fatal("the notch scheduled no read; the frame on screen would be the old one")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	assertFrameMatchesTmux(t, m, sessID, rows)

	// And back down to the live bottom.
	for i := 0; i < 60 && m.focusScroll > 0; i++ {
		updated, cmd = m.handleMouse(wheel(false, x, y))
		m = updated.(*Model)
		if cmd != nil {
			updated, _ = m.Update(cmd())
			m = updated.(*Model)
		}
	}
	if m.focusScroll != 0 {
		t.Fatalf("wheeling down left the preview %d lines back", m.focusScroll)
	}
	assertFrameMatchesTmux(t, m, sessID, rows)
}

// The rail keeps swallowing the wheel: a notch there would move the session
// cursor, silently retargeting every keystroke that follows (#110).
func TestWheelOverTheRailLeavesThePreviewAlone(t *testing.T) {
	m, _ := listWithHistory(t, "railnotch")
	_, y := previewCell(m)
	before := m.cursor

	updated, cmd := m.handleMouse(wheel(true, 0, y))
	m = updated.(*Model)
	if m.focusScroll != 0 {
		t.Fatalf("a notch over the rail scrolled the preview to %d", m.focusScroll)
	}
	if m.cursor != before {
		t.Fatalf("a notch over the rail moved the cursor to %d", m.cursor)
	}
	if cmd != nil {
		t.Fatal("a notch over the rail scheduled work")
	}
}

// A scrolled preview holds still: the poll pass keeps capturing the live
// bottom, and a frame of it landing mid-read would yank the view away from
// what the operator is reading.
func TestScrolledPreviewHoldsStillUnderALiveFrame(t *testing.T) {
	m, _ := listWithHistory(t, "listhold")
	x, y := previewCell(m)
	updated, cmd := m.handleMouse(wheel(true, x, y))
	m = updated.(*Model)
	if cmd != nil {
		updated, _ = m.Update(cmd())
		m = updated.(*Model)
	}
	scrolled := m.preview
	if m.focusScroll == 0 {
		t.Fatal("the preview never scrolled")
	}

	m.setPreview("a live bottom frame\n")
	if m.preview != scrolled {
		t.Fatal("a live frame painted over the scrolled preview")
	}
}

// Moving the cursor returns the board to a live bottom, and drops the pane
// facts with it: the next row's notch must not route on the last row's
// answers about mouse ownership and history depth.
func TestMovingTheCursorReturnsThePreviewToItsLiveBottom(t *testing.T) {
	m, sessID := listWithHistory(t, "listmover")
	// A second row for the cursor to land on. Creating it moves the cursor
	// and drops the first row's frame and facts, so the preview is put back
	// the way a poll pass would once the cursor returns.
	createSession(t, m, "listneighbour", t.TempDir(), "")
	m.selectSessionRow(t, "listmover")
	seedLive(t, m, sessID)
	m.frame()
	m.pane.forID = sessID
	m.pane.history = paneHistorySize(t, m, sessID)

	x, y := previewCell(m)
	updated, _ := m.handleMouse(wheel(true, x, y))
	m = updated.(*Model)
	if m.focusScroll == 0 {
		t.Fatal("the preview never scrolled")
	}

	before := m.cursor
	m.moveCursor(1)
	if m.cursor == before {
		m.moveCursor(-1)
	}
	if m.cursor == before {
		t.Fatal("the cursor had nowhere to go; the reset is untested")
	}
	if m.focusScroll != 0 {
		t.Fatalf("the new row inherited a scroll offset of %d", m.focusScroll)
	}
	if m.pane.forID != "" || m.pane.history != 0 || m.pane.mouse {
		t.Fatalf("the new row inherited pane facts: %+v", m.pane)
	}
}

// A pane whose application owns the mouse scrolls itself, so the manager
// only learns what the notch did by looking. Off focus the next scheduled
// look is up to previewIntervalCalm away -- the cadence of exactly the idle
// agent somebody scrolls back through -- so the notch has to chase its own
// repaint and buy the preview the faster cadence while it does.
func TestForwardedNotchChasesItsRepaint(t *testing.T) {
	m, _ := listWithHistory(t, "listforward")
	// What tmux reports for an agent CLI drawing on the alternate screen:
	// it owns the mouse and keeps no history to walk.
	m.pane.mouse = true
	m.pane.sgr = true
	m.pane.history = 0
	m.focusActiveAt = time.Time{}

	x, y := previewCell(m)
	updated, cmd := m.handleMouse(wheel(true, x, y))
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("a forwarded notch scheduled no look; the frame would wait out the tick")
	}
	if m.focusScroll != 0 {
		t.Fatalf("a pane that scrolls itself must not also be walked back: offset %d", m.focusScroll)
	}
	if !m.focusWheeling() {
		t.Fatal("the notch did not mark the pane as under the wheel")
	}
	if m.focusActive() {
		t.Fatal("the notch marked the pane as typed into; under focus that buys the keystroke cadence")
	}
	if got := m.previewInterval(); got != previewIntervalLive {
		t.Fatalf("preview cadence after a notch = %v, want %v", got, previewIntervalLive)
	}
	// The chase reports a real frame of the real pane, or nothing when the
	// pane never repainted -- never a guess.
	if msg := cmd(); msg != nil {
		if _, ok := msg.(previewMsg); !ok {
			t.Fatalf("chase returned %T, want previewMsg", msg)
		}
	}
}

// The wheel is not a gesture every client has: a tablet trackpad and any
// terminal that sends a swipe as arrow keys leave the manager no notch to
// route, and plain arrows are the list's own navigation. Alt-modified, the
// same keys focus mode scrolls on reach the preview.
func TestAltArrowsScrollTheListPreview(t *testing.T) {
	m, sessID := listWithHistory(t, "listaltkeys")
	rows := m.previewPaneHeight()
	before := m.cursor

	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
	m = updated.(*Model)
	if m.cursor != before {
		t.Fatalf("alt+up moved the list cursor to %d", m.cursor)
	}
	if m.focusScroll == 0 {
		t.Fatal("alt+up did not scroll the preview")
	}
	if cmd == nil {
		t.Fatal("alt+up scheduled no read")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	assertFrameMatchesTmux(t, m, sessID, rows)

	updated, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModAlt})
	m = updated.(*Model)
	if cmd != nil {
		updated, _ = m.Update(cmd())
		m = updated.(*Model)
	}
	if m.focusScroll != 0 {
		t.Fatalf("alt+end left the preview %d lines back", m.focusScroll)
	}
}

// The process tree is the expensive half of a capture, so a capture that was
// not asked for one must not walk it.
func TestPreviewCaptureSkipsTheProcessTree(t *testing.T) {
	m, sessID := listWithHistory(t, "listnoproc")
	sess, ok := m.selected()
	if !ok || sess.ID != sessID {
		t.Fatal("no session selected")
	}
	msg, ok := m.previewCmd(sess, m.previewGen, false)().(previewMsg)
	if !ok {
		t.Fatal("capture returned no preview")
	}
	if msg.procOK {
		t.Fatal("a text-only capture walked the process tree")
	}
	if msg.preview == "" {
		t.Fatal("a text-only capture returned no text")
	}
}

// altUp is the list's preview-scroll key, which every tool answers to.
func altUp() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt} }
