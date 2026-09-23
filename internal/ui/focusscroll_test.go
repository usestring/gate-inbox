// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/store"
)

// sgrMouseReportRe matches one SGR mouse report as cat echoes it back to the
// pane: "[<button;col;row" plus a terminator, M for a press or motion and m
// for a release. The coordinates vary with the cell, so they match as digits,
// and the terminator is what tells a press from a release of the same button.
func sgrMouseReportRe(button int, release bool) *regexp.Regexp {
	term := "M"
	if release {
		term = "m"
	}
	return regexp.MustCompile(fmt.Sprintf(`\[<%d;\d+;\d+%s`, button, term))
}

// focusedWithHistory focuses a session whose pane has more output than
// fits on screen, so scrolling has somewhere to go.
//
// It used to wait here for a control-mode mirror to come up, because the
// scroll queries rode that mirror's pipe. They ride the pooled per-server
// client now and fall back to a fork, so there is nothing to wait for: the
// pane is readable the moment it exists.
func focusedWithHistory(t testing.TB, name string) (*Model, string) {
	t.Helper()
	m := buildModel(t)
	createSession(t, m, name, t.TempDir(), "")
	m.selectSessionRow(t, name)
	sess := m.rows[m.cursor].sess

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("did not enter focus: %q", m.errBar.text)
	}

	command := `i=1; while [ "$i" -le 120 ]; do printf 'history-line-%03d\n' "$i"; i=$((i+1)); done`
	if err := m.tmux.SendText(sess.ID, command); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	// Let the pane finish painting so the history is really there.
	deadline := time.Now().Add(10 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sess.ID)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if strings.Contains(pane, "history-line-120") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never printed the history: %q", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
	m.frame()
	// Seed the live frame the way the scroll path fetches one, and the
	// history depth the wheel clamps against.
	seedLive(t, m, sess.ID)
	m.pane.history = paneHistorySize(t, m, sess.ID)
	return m, sess.ID
}

// paneHistorySize asks tmux for the pane's history depth, which the model
// otherwise learns from a capture's pane facts.
func paneHistorySize(t testing.TB, m *Model, sessID string) int {
	t.Helper()
	out, err := m.tmux.PaneState(sessID, "#{history_size}")
	if err != nil {
		t.Fatalf("history query: %v", err)
	}
	size, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("history size %q: %v", out, err)
	}
	return size
}

// seedLive pulls the pane's current bottom into the model's preview.
func seedLive(t testing.TB, m *Model, sessID string) {
	t.Helper()
	cmd := m.focusRegionCmd(sessID, 0)
	if cmd == nil {
		t.Fatal("no live region command")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("live region capture returned nothing")
	}
	updated, _ := m.Update(msg)
	*m = *updated.(*Model)
}

// The wheel walks the focused pane back through its scrollback and back
// down again, and the view holds still while scrolled.
func TestFocusWheelScrollsHistory(t *testing.T) {
	m, sessID := focusedWithHistory(t, "scroller")

	live := m.preview
	if !strings.Contains(live, "history-line-120") {
		t.Fatalf("live preview missing the newest line: %q", live)
	}

	// Wheel up until an older line comes into view.
	var scrolled string
	for i := 0; i < 10; i++ {
		before := m.focusScroll
		cmd := m.scrollFocus(-1)
		if m.focusScroll == before {
			t.Fatalf("wheel up did not move at offset %d", m.focusScroll)
		}
		// A notch the cache answers carries no command and has already put
		// its frame up.
		if cmd != nil {
			updated, _ := m.Update(cmd())
			*m = *updated.(*Model)
		}
		scrolled = m.preview
		if !strings.Contains(scrolled, "history-line-120") {
			break
		}
	}
	if m.focusScroll == 0 {
		t.Fatal("wheel up never moved the pane back")
	}
	if strings.Contains(scrolled, "history-line-120") {
		t.Fatalf("scrolling never reached older output:\n%s", scrolled)
	}
	if !m.scrolledBack() {
		t.Fatal("pane does not report itself scrolled")
	}

	// A live-bottom capture landing mid-read must not yank the view back
	// down to it.
	updated, _ := m.Update(previewMsg{sessID: sessID, at: time.Now(), preview: "LIVE-FRAME\n"})
	*m = *updated.(*Model)
	if m.preview != scrolled {
		t.Fatal("a live frame overwrote the scrolled view")
	}

	// Wheel down all the way returns to the live bottom.
	for i := 0; i < 20 && m.focusScroll > 0; i++ {
		cmd := m.scrollFocus(1)
		if cmd == nil {
			continue
		}
		updated, _ := m.Update(cmd())
		*m = *updated.(*Model)
	}
	if m.scrolledBack() {
		t.Fatalf("wheel down left the pane scrolled at %d", m.focusScroll)
	}
	if !strings.Contains(m.preview, "history-line-120") {
		t.Fatalf("bottom does not show the newest line:\n%s", m.preview)
	}
}

// A capture scheduled before the preview reflows must not blank the bottom
// of the resized viewport when its reply arrives afterwards.
func TestFocusScrollRecapturesAfterPreviewResize(t *testing.T) {
	m, _ := focusedWithHistory(t, "reflow")
	cmd := m.scrollFocus(-1)
	if cmd == nil {
		t.Fatal("wheel up produced no capture")
	}
	stale := cmd()
	if stale == nil {
		t.Fatal("scroll capture returned nothing")
	}
	oldRows := m.previewPaneHeight()
	m.height += 8
	if m.previewPaneHeight() == oldRows {
		t.Fatal("test setup did not change preview height")
	}
	m.resizeNow(t)

	updated, recapture := m.Update(stale)
	m = updated.(*Model)
	if recapture == nil {
		t.Fatal("stale geometry capture was accepted")
	}
	updated, _ = m.Update(recapture())
	m = updated.(*Model)
	if got, want := len(paneExact(m.preview, m.previewPaneHeight(), m.previewPaneWidth())), m.previewPaneHeight(); got != want {
		t.Fatalf("scroll frame has %d rows, want %d", got, want)
	}
	if !strings.Contains(m.preview, "history-line-") {
		t.Fatalf("recaptured frame lost history:\n%s", m.preview)
	}
}

// Deep history must keep the requested pane-sized frame intact. This covers
// the control-pipe capture path beyond the shallow history used by the wheel
// smoke test above.
func TestFocusScrollKeepsDeepHistoryFrame(t *testing.T) {
	m, sessID := focusedWithHistory(t, "deep-history")
	command := `i=121; while [ "$i" -le 1000 ]; do printf 'history-line-%04d\n' "$i"; i=$((i+1)); done`
	if err := m.tmux.SendText(sessID, command); err != nil {
		t.Fatalf("send deep history: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sessID)
		if err != nil {
			t.Fatalf("capture deep history: %v", err)
		}
		if strings.Contains(pane, "history-line-1000") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never printed deep history: %q", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
	m.pane.history = paneHistorySize(t, m, sessID)
	if m.pane.history < 820 {
		t.Skipf("tmux history is only %d lines", m.pane.history)
	}
	m.focusScroll = 807
	cmd := m.focusRegionCmd(sessID, m.focusScroll)
	msg := cmd()
	if msg == nil {
		t.Fatal("deep capture returned nothing")
	}
	updated, _ := m.Update(msg)
	m = updated.(*Model)
	if got, want := len(paneExact(m.preview, m.previewPaneHeight(), m.previewPaneWidth())), m.previewPaneHeight(); got != want {
		t.Fatalf("deep frame has %d rows, want %d", got, want)
	}
	if !strings.Contains(m.preview, "history-line-") {
		t.Fatalf("deep frame lost history:\n%s", m.preview)
	}
}

// Typing while scrolled snaps back to the live bottom: keystrokes land
// there, so the view must follow them.
func TestTypingResumesLiveView(t *testing.T) {
	m, _ := focusedWithHistory(t, "typeback")
	for i := 0; i < 5; i++ {
		if cmd := m.scrollFocus(-1); cmd != nil {
			updated, _ := m.Update(cmd())
			*m = *updated.(*Model)
		}
	}
	if !m.scrolledBack() {
		t.Skip("pane had no history to scroll")
	}

	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 'z', Text: "z"})
	*m = *updated.(*Model)
	if m.scrolledBack() {
		t.Fatal("typing left the view scrolled back")
	}
	if cmd == nil {
		t.Fatal("typing while scrolled fetched no live frame")
	}
}

// Scrolling stops at the top of the history instead of walking into
// empty regions forever.
//
// The wheel is driven to a standstill by watching the offset, never by
// watching for a command: the first notch that returns nothing is the notch
// that could not move, and reading it as "no capture needed" is what a
// cached block used to make ambiguous.
func TestScrollStopsAtHistoryTop(t *testing.T) {
	m, _ := focusedWithHistory(t, "topstop")
	limit := m.pane.history
	if limit == 0 {
		t.Skip("pane reported no history")
	}
	notches, silent := 0, 0
	for i := 0; i < 500; i++ {
		before := m.focusScroll
		cmd := m.scrollFocus(-1)
		if m.focusScroll == before {
			break
		}
		notches++
		if cmd == nil {
			silent++
			continue
		}
		// Land the read the way the event loop does. One read is out at a
		// time -- that is what keeps a flick from forking a capture per notch
		// -- so a test that never lands one would suppress every notch after
		// the first and prove nothing about where scrolling stops.
		updated, follow := m.Update(cmd())
		*m = *updated.(*Model)
		for guard := 0; follow != nil && guard < 4; guard++ {
			msg := follow()
			if msg == nil {
				break
			}
			updated, follow = m.Update(msg)
			*m = *updated.(*Model)
		}
	}
	if m.focusScroll > limit {
		t.Fatalf("scrolled %d lines past a history of %d", m.focusScroll, limit)
	}
	if m.focusScroll != limit {
		t.Fatalf("scrolling stopped at %d, want the history top %d", m.focusScroll, limit)
	}
	if notches == 0 {
		t.Fatal("the wheel never moved off the live bottom")
	}
	// Every notch that moves the view reads the pane, once the read before it
	// has landed. There used to be a cache of scrollback blocks that answered
	// some of them from memory, and the only thing that ever invalidated it
	// was the mirror announcing a repaint. With the mirror gone a remembered
	// frame cannot be kept honest, so a notch that read nothing would be
	// painting a guess. A notch suppressed while a read is still out is not
	// that: nothing is painted for it at all until the read it is waiting on
	// comes back and re-reads for where the wheel ended.
	if silent != 0 {
		t.Fatalf("%d of %d notches moved the view without reading the pane", silent, notches)
	}
}

// The caret belongs to the live pane; a scrolled view must not paint it.
func TestNoCaretWhileScrolled(t *testing.T) {
	m := paneAt(t, "one", "two")
	m.pane.cursor = paneCursor{x: 1, y: 0, ok: true}
	m.cursorOn = true
	if _, _, ok := m.cursorCell(2); !ok {
		t.Fatal("caret missing on the live view")
	}
	m.focusScroll = 6
	if _, _, ok := m.cursorCell(2); ok {
		t.Fatal("caret drawn on a scrolled-back view")
	}
}

// The preview box changes height for reasons other than a terminal
// resize; a pane left at the old height paints a dead band under its
// output, so a refresh has to re-assert the geometry.
func TestRefreshReassertsPaneHeight(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "sizer", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())
	sess := m.rows[m.cursor].sess

	if got, want := windowHeight(t, sess.ID), m.previewPaneHeight(); got != want {
		t.Fatalf("initial pane height = %d, want %d", got, want)
	}

	// A shorter frame with no size message: the header or the status line
	// taking a row moves the box the same way.
	m.height -= 4
	shrunk := m.previewPaneHeight()
	m.applyCmd(t, m.refreshCmd())
	if got := windowHeight(t, sess.ID); got != shrunk {
		t.Fatalf("pane height after the box shrank = %d, want %d", got, shrunk)
	}

	m.height += 4
	grown := m.previewPaneHeight()
	m.applyCmd(t, m.refreshCmd())
	if got := windowHeight(t, sess.ID); got != grown {
		t.Fatalf("pane height after the box grew = %d, want %d", got, grown)
	}
}

// windowHeight is the tmux window height a session is currently pinned to.
func windowHeight(t *testing.T, id string) int {
	t.Helper()
	out, err := tmuxCmd("display-message", "-p", "-t", "gi_"+id, "#{window_height}").CombinedOutput()
	if err != nil {
		t.Fatalf("display-message: %v: %s", err, out)
	}
	height, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse height %q: %v", out, err)
	}
	return height
}

// Focusing must leave the preview box where it was: a pane resized on the
// way in makes an agent drawing on the normal screen redraw its whole
// transcript, which throws the view up its history and back.
func TestFocusKeepsPaneHeight(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "focused", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())
	sess := m.rows[m.cursor].sess

	for _, width := range []int{100, 240} {
		m.width = width
		m.applyCmd(t, m.refreshCmd())
		listed := m.previewPaneHeight()

		m.focusSelected()
		if got := m.previewPaneHeight(); got != listed {
			t.Fatalf("width %d: focused box = %d rows, want %d", width, got, listed)
		}
		m.applyCmd(t, m.refreshCmd())
		if got := windowHeight(t, sess.ID); got != listed {
			t.Fatalf("width %d: focused pane = %d rows, want %d", width, got, listed)
		}
		// An agent claiming the mouse adds a key to the focused tier.
		m.pane.mouse = true
		if got := m.previewPaneHeight(); got != listed {
			t.Fatalf("width %d: focused box with mouse = %d rows, want %d", width, got, listed)
		}
		m.applyCmd(t, m.refreshCmd())
		if got := windowHeight(t, sess.ID); got != listed {
			t.Fatalf("width %d: focused pane with mouse = %d rows, want %d", width, got, listed)
		}
		m.pane.mouse = false
		m.applyCmd(t, m.leaveFocus())
	}
}

// focusedMouseApp focuses a session whose tool claims the mouse, reading the
// pane's facts through Update until the claim lands, so the pane state the
// wheel routes on is populated.
//
// The claim used to arrive on a control-mode mirror's pushes. It rides the
// focused capture now -- the same round trip that fetches the frame -- so
// this pumps that capture instead of waiting on a client.
func focusedMouseApp(t *testing.T, tool, name string) (*Model, store.Session) {
	t.Helper()
	m := buildModel(t)
	if err := m.spawnSession(tool, name, t.TempDir(), "", "", true); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectSessionRow(t, name)
	sess := m.rows[m.cursor].sess

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	m.frame()

	deadline := time.Now().Add(10 * time.Second)
	for !m.pane.mouse {
		if time.Now().After(deadline) {
			t.Skip("pane never reported mouse tracking on this host")
		}
		cmd := m.focusCaptureCmd(sess.ID, m.previewGen, false, captureGate{force: true})
		if cmd == nil {
			t.Fatal("the focused session produced no capture command")
		}
		if msg := cmd(); msg != nil {
			updated, _ := m.Update(msg)
			*m = *updated.(*Model)
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.frame()
	return m, sess
}

// Ordinary clicks still select and copy inside Gate Inbox. Holding Alt is
// the deliberate handoff gesture for an agent that owns the mouse.
func TestAltClickReachesMouseTrackingApp(t *testing.T) {
	m, sess := focusedMouseApp(t, "mouse-tool", "clickapp")

	m.handleFocusMouse(tea.MouseClickMsg{Button: tea.MouseLeft, Mod: tea.ModAlt, X: m.pane.box.x + 2, Y: m.pane.box.y + 1})
	if m.sel.active {
		t.Fatal("Alt-click on a mouse-tracking pane started a selection")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sess.ID)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if press := strings.Index(pane, "[<0;"); press >= 0 {
			move := strings.Index(pane, "[<35;")
			if move < 0 || move > press {
				t.Fatalf("all-motion pointer move did not lead the press: %q", pane)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Alt-click report never reached the pane: %q", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAltClickReleaseOutsidePaneReachesMouseTrackingApp(t *testing.T) {
	m, sess := focusedMouseApp(t, "mouse-tool", "outside-release")
	m.handleFocusMouse(tea.MouseClickMsg{Button: tea.MouseLeft, Mod: tea.ModAlt, X: m.pane.box.x + 2, Y: m.pane.box.y + 1})
	m.handleFocusMouse(tea.MouseReleaseMsg{Button: tea.MouseNone,
		X: m.pane.box.x + m.pane.box.width, Y: m.pane.box.y + 1})

	deadline := time.Now().Add(5 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sess.ID)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		// Distinguish the press from the release by its SGR terminator
		// (M press, m release), not by a second generic "[<0;" fragment.
		if sgrMouseReportRe(leftButton, false).MatchString(pane) &&
			sgrMouseReportRe(leftButton, true).MatchString(pane) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("outside release never reached the pane: %q", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// An application that turns on mouse tracking owns the wheel: agent CLIs
// run on the alternate screen, where tmux keeps no scrollback at all, and
// scroll themselves when they receive the event.
func TestWheelReachesMouseTrackingApp(t *testing.T) {
	m, sess := focusedMouseApp(t, "mouse-tool", "wheelapp")

	// A wheel notch over the pane now goes to the app, not to tmux history.
	before := m.focusScroll
	m.wheelFocus(true, m.pane.box.x+2, m.pane.box.y+1)
	if m.focusScroll != before {
		t.Fatal("wheel scrolled tmux history while the app owned the mouse")
	}
	if !m.pane.sgr {
		t.Fatal("pane asked for SGR reports but the model did not read it")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sess.ID)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		// cat echoes the control bytes, so the wheel report shows up as
		// text with the escape rendered as ^[.
		if strings.Contains(pane, "[<64;") {
			// The app tracks all motion, so the notch has to arrive with
			// the pointer already reported at that cell.
			move := strings.Index(pane, "[<35;")
			if move < 0 {
				t.Fatalf("no pointer move ahead of the wheel report: %q", pane)
			}
			if move > strings.Index(pane, "[<64;") {
				t.Fatalf("pointer move landed after the wheel report: %q", pane)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("wheel report never reached the pane: %q", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Every mouse report the manager forwards has to reach the poller, or the
// repaint it provokes is read as the agent working and scrolling a session
// changes its status.
func TestForwardedMouseInputTellsThePoller(t *testing.T) {
	for _, tc := range []struct {
		name    string
		gesture func(m *Model, x, y int)
	}{
		{name: "wheel", gesture: func(m *Model, x, y int) {
			m.handleFocusMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: x, Y: y})
		}},
		{name: "click", gesture: func(m *Model, x, y int) {
			m.handleFocusMouse(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: y})
			m.handleFocusMouse(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: x, Y: y})
		}},
		{name: "alt-drag", gesture: func(m *Model, x, y int) {
			m.handleFocusMouse(tea.MouseClickMsg{Button: tea.MouseLeft, Mod: tea.ModAlt, X: x, Y: y})
			m.handleFocusMouse(tea.MouseReleaseMsg{Button: tea.MouseNone, X: x, Y: y})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, sess := focusedMouseApp(t, "mouse-tool", "echo-"+tc.name)
			if m.poller.operatorEcho(sess.ID) {
				t.Fatal("an untouched session should carry no forwarded-mouse stamp")
			}
			tc.gesture(m, m.pane.box.x+2, m.pane.box.y+1)
			if !m.poller.operatorEcho(sess.ID) {
				t.Fatalf("%s did not tell the poller it forwarded input", tc.name)
			}
		})
	}
}

// A pane whose app owns the wheel must not stay parked on a scrollback
// offset: the wheel goes to the app from then on, so nothing would walk
// the offset back down and the view would hold a stale frame for good.
func TestAppMouseClearsScrollback(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "sticky", t.TempDir(), "")
	m.selectSessionRow(t, "sticky")
	sess := m.rows[m.cursor].sess
	m.mode = modeFocus
	m.focusScroll = 9
	if !m.scrolledBack() {
		t.Fatal("setup did not leave the view scrolled back")
	}

	updated, _ := m.Update(previewMsg{
		sessID:  sess.ID,
		at:      time.Now(),
		preview: "LIVE-FRAME\n",
		facts:   paneFacts{paneMouse: true},
		factsOK: true,
	})
	m = updated.(*Model)
	if m.focusScroll != 0 {
		t.Fatalf("focusScroll = %d, want the app-owned pane back at the bottom", m.focusScroll)
	}
	if m.preview != "LIVE-FRAME\n" {
		t.Fatalf("preview = %q, want the live frame", m.preview)
	}
}

// The poll pass is the slowest source of frames there is, and it owes a
// scrolled-back pane the same stillness every faster one does.
func TestPolledFrameHoldsScrolledView(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "polled", t.TempDir(), "")
	m.selectSessionRow(t, "polled")
	m.mode = modeFocus
	m.preview = "SCROLLED-FRAME\n"
	m.focusScroll = 6

	m.setPreview("LIVE-FRAME\n")
	if m.preview != "SCROLLED-FRAME\n" {
		t.Fatalf("preview = %q, want the scrolled frame held", m.preview)
	}

	m.focusScroll = 0
	m.setPreview("LIVE-FRAME\n")
	if m.preview != "LIVE-FRAME\n" {
		t.Fatalf("preview = %q, want the live frame back at the bottom", m.preview)
	}
}

// An app that claims the mouse without asking for SGR reads the original
// encoding, and reports in the newer one would reach it as text.
func TestWheelFallsBackToX10Reports(t *testing.T) {
	m, sess := focusedMouseApp(t, "x10-tool", "x10app")
	if m.pane.sgr {
		t.Fatal("a pane that never asked for SGR reported it")
	}

	m.wheelFocus(true, m.pane.box.x+2, m.pane.box.y+1)
	deadline := time.Now().Add(5 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sess.ID)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		// cat echoes the bytes back, so an X10 report shows up as [M and
		// its three coordinate characters, with no SGR report anywhere.
		if strings.Contains(pane, "[M") {
			if strings.Contains(pane, "[<") {
				t.Fatalf("SGR report reached a pane that never asked for it: %q", pane)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("X10 wheel report never reached the pane: %q", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMouseReportEncodings(t *testing.T) {
	if got, want := sgrMouse(motionButton, 0, 0), "\x1b[<35;1;1M"; got != want {
		t.Errorf("motion = %q, want %q", got, want)
	}
	if got, want := sgrMouse(wheelUpButton, 0, 0), "\x1b[<64;1;1M"; got != want {
		t.Errorf("wheel up = %q, want %q", got, want)
	}
	if got, want := sgrMouse(wheelDownButton, 11, 4), "\x1b[<65;12;5M"; got != want {
		t.Errorf("wheel down = %q, want %q", got, want)
	}
	if got, want := hexBytes("\x1b[<64;1;1M"), "1b 5b 3c 36 34 3b 31 3b 31 4d"; got != want {
		t.Errorf("hexBytes = %q, want %q", got, want)
	}

	got, ok := x10Mouse(wheelUpButton, 0, 0)
	if !ok || got != "\x1b[M`!!" {
		t.Errorf("x10 wheel up = %q (ok=%v), want %q", got, ok, "\x1b[M`!!")
	}
	if got, ok := x10Mouse(motionButton, 11, 4); !ok || got != "\x1b[MC,%" {
		t.Errorf("x10 motion = %q (ok=%v), want %q", got, ok, "\x1b[MC,%")
	}
	// Past the cell the encoding can name, a report would land on the
	// wrong column, so there is none to send.
	if _, ok := x10Mouse(wheelUpButton, x10Limit, 4); ok {
		t.Error("x10 named a column the encoding cannot carry")
	}
	if _, ok := x10Mouse(wheelUpButton, 4, x10Limit); ok {
		t.Error("x10 named a row the encoding cannot carry")
	}
}

// Entering focus on a pane that has gone quiet keeps the pane state already
// held for it. The read that refreshes it is a command still in flight, and
// a quiet pane has nothing new to say in any case, so a reset on entry would
// route the wheel as a plain pane with no history — dead until the agent
// next paints, which is exactly the shape of scrolling a finished agent's
// pane.
func TestFocusReentryKeepsPaneStateOnQuietPane(t *testing.T) {
	m, sess := focusedMouseApp(t, "mouse-tool", "quietapp")

	// Out to the list and back in, with the pane painting nothing in
	// between — checking on an agent whose turn has ended.
	m.leaveFocus()
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("did not re-enter focus: %q", m.errBar.text)
	}

	if !m.pane.mouse {
		t.Fatal("re-entering focus dropped the pane's mouse claim")
	}
	if !m.pane.sgr {
		t.Fatal("re-entering focus dropped the pane's SGR encoding")
	}
	m.frame()

	// The wheel still reaches the app, with no pushed capture in between.
	m.wheelFocus(true, m.pane.box.x+2, m.pane.box.y+1)
	deadline := time.Now().Add(5 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sess.ID)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if strings.Contains(pane, "[<64;") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("wheel report never reached the quiet pane: %q", pane)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A cache stamped by another session's capture still resets, serving
	// client or not: this session's first capture may not have landed.
	m.leaveFocus()
	m.pane.forID = "someone-else"
	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.pane.mouse {
		t.Fatal("another session's cached flags survived focus entry")
	}
}

// The wheel reports the pane's own row, which is the painted row plus
// whatever the panel dropped off the top of a taller capture.
func TestWheelReportUsesPaneRow(t *testing.T) {
	m := paneAt(t, "one", "two")
	m.pane.sgr = true
	m.preview = "a\nb\nc\nd\none\ntwo\n"

	if got, want := m.paneRowOffset(m.pane.box.height), 4; got != want {
		t.Fatalf("row offset = %d, want %d", got, want)
	}
	report, ok := m.wheelReport(true, 3, 1+m.paneRowOffset(m.pane.box.height))
	if !ok {
		t.Fatal("no wheel report for a pane inside the encoding's range")
	}
	if want := "\x1b[<64;4;6M"; report != want {
		t.Fatalf("report = %q, want %q", report, want)
	}
}

// A wheel notch over an adopted pane whose application owns the mouse has to
// reach that pane on its own server, by whichever path SendRawAt finds there.
// The pane state that routing decision reads is posed here rather than waited
// for.
func TestWheelReachesAnAdoptedMouseTrackingPane(t *testing.T) {
	m, socket, pane := adoptedFocus(t, `printf '\033[?1003h\033[?1006h' && cat`)
	// The pane box the wheel routes on is measured off a painted frame, and
	// the frame needs a capture, so the poll capture stands in.
	m.applyCmd(t, m.refreshCmd())
	m.frame()
	m.pane.mouse = true
	m.pane.sgr = true

	m.wheelFocus(true, m.pane.box.x+2, m.pane.box.y+1)
	if m.errBar.text != "" {
		t.Fatalf("wheel set err: %q", m.errBar.text)
	}
	report := sgrMouseReportRe(wheelUpButton, false)
	foreignPaneContains(t, socket, pane, report.MatchString)
}

// A pane whose control client never came up still scrolls. The pipe is the
// first thing every region capture tries, adopted or not, and the fork is
// what is left when the query declines -- without the fallback the view
// never moves off the live bottom however far the wheel turns.
func TestFocusScrollReadsAnAdoptedPane(t *testing.T) {
	m, socket, pane := adoptedFocus(t, `i=1; while [ "$i" -le 200 ]; do printf 'adopted-line-%03d\n' "$i"; i=$((i+1)); done; cat`)
	foreignPaneContains(t, socket, pane, func(out string) bool {
		return strings.Contains(out, "adopted-line-200")
	})
	// The preview box the region is sized from is measured off a painted
	// frame, which an adopted session gets from the poll, not from a push.
	m.applyCmd(t, m.refreshCmd())
	m.frame()
	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected in focus")
	}
	live, isScroll := m.focusRegionCmd(sess.ID, 0)().(focusScrollMsg)
	if !isScroll {
		t.Fatal("the live region capture returned no scroll message")
	}
	if !live.ok {
		t.Fatal("the live region capture on an adopted pane failed")
	}
	if !strings.Contains(live.block, "adopted-line-200") {
		t.Fatalf("the live region is missing the newest line:\n%q", live.block)
	}

	m.focusScroll = m.previewPaneHeight()
	msg := m.requestFocusRegion(sess.ID)()
	scrolled, isScroll := msg.(focusScrollMsg)
	if !isScroll {
		t.Fatal("the scrolled region capture returned no scroll message")
	}
	if !scrolled.ok || strings.TrimSpace(scrolled.block) == "" {
		t.Fatalf("scrolling back an adopted pane returned nothing: ok=%v block=%q", scrolled.ok, scrolled.block)
	}

	updated, _ := m.Update(msg)
	*m = *updated.(*Model)
	if !strings.Contains(m.preview, "adopted-line-") {
		t.Fatalf("the scrolled frame carried no history:\n%q", m.preview)
	}
	if strings.Contains(m.preview, "adopted-line-200") {
		t.Fatalf("the scrolled frame still shows the live bottom:\n%q", m.preview)
	}
}

// A notch landing on the tool's own composer is walked up to the transcript:
// opencode routes the wheel by hover and swallows one over its ┃ input box,
// which on a phone-sized pane is where every swipe lands, while a tool that
// scrolls wherever the notch lands keeps working. Measured live against
// opencode 1.18.30: wheel over the messages scrolls, over the composer does
// nothing at all.
func TestForwardedWheelLeavesTheComposer(t *testing.T) {
	m := &Model{engine: liveEngine(t)}
	m.preview = "message one\nmessage two\n\n┃ typed text\n┃\n╹────\n"
	if got := m.wheelTargetRow("opencode", 4); got != 1 {
		t.Fatalf("notch on the empty composer row landed on row %d, want 1", got)
	}
	if got := m.wheelTargetRow("opencode", 3); got != 1 {
		t.Fatalf("notch on the typed composer row landed on row %d, want 1", got)
	}
	if got := m.wheelTargetRow("opencode", 1); got != 1 {
		t.Fatalf("notch over the transcript moved to row %d, want 1", got)
	}
	if got := m.wheelTargetRow("opencode", 0); got != 0 {
		t.Fatalf("notch on the first row moved to row %d, want 0", got)
	}
	// The composer's ╹ border is neither an input line nor blank, so a notch
	// exactly on it stays where it is: same as today, no worse.
	if got := m.wheelTargetRow("opencode", 5); got != 5 {
		t.Fatalf("notch on the composer border moved to row %d, want 5", got)
	}
	if got := m.wheelTargetRow("no-such-tool", 3); got != 3 {
		t.Fatalf("notch for an unknown tool moved to row %d, want 3", got)
	}
	if got := m.wheelTargetRow("opencode", 99); got != 99 {
		t.Fatalf("notch past the capture moved to row %d, want 99", got)
	}
	bare := &Model{}
	if got := bare.wheelTargetRow("opencode", 3); got != 3 {
		t.Fatalf("notch with no engine moved to row %d, want 3", got)
	}
}
