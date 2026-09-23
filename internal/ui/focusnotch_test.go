package ui

import (
	"strings"
	"testing"
	"time"
)

// Every wheel notch paints the frame tmux would answer with.
//
// There used to be a cache of scrollback blocks behind these tests, read
// ahead in the direction of travel, and most of what they asserted was about
// keeping it honest: that a served block matched a direct capture, that it
// was dropped when the pane wrote, that it was dropped when a resize
// reflowed the pane. The cache is gone -- it existed to dodge a ~5ms forked
// capture per notch, and over the pooled control pipe the same read costs
// tens of microseconds -- and so is the mirror whose paint events were the
// only thing that could ever invalidate it.
//
// What survives is the invariant all of it was in service of, now asserted
// against every notch rather than against the cached ones: what is on screen
// is what the pane actually holds at that offset. It is also the regression
// test against reintroducing a cache without an invalidation signal to match.
func TestEveryNotchPaintsTheFrameTmuxWouldAnswer(t *testing.T) {
	m, sessID := focusedWithHistory(t, "notchtruth")
	deepen(t, m, sessID)
	rows := m.previewPaneHeight()

	notches := 0
	for i := 0; i < 60 && m.focusScroll < m.pane.history-rows; i++ {
		before := m.focusScroll
		cmd := m.scrollFocus(-1)
		if m.focusScroll == before {
			break
		}
		if cmd == nil {
			t.Fatal("a notch was answered from memory; nothing here can tell a remembered frame that the pane has moved under it")
		}
		updated, _ := m.Update(cmd())
		*m = *updated.(*Model)
		notches++
		assertFrameMatchesTmux(t, m, sessID, rows)
	}
	if notches == 0 {
		t.Fatal("the wheel never moved; the comparison proves nothing")
	}

	// And back down through the same history.
	for i := 0; i < 60 && m.focusScroll > 0; i++ {
		cmd := m.scrollFocus(1)
		if cmd != nil {
			updated, _ := m.Update(cmd())
			*m = *updated.(*Model)
		}
		assertFrameMatchesTmux(t, m, sessID, rows)
	}
	t.Logf("%d notches, each read from the pane", notches)
}

// The same comparison on an adopted pane, which is what the board is mostly
// made of: the read rides the pane's own server, over the pooled client the
// manager attaches to its own anchor session there rather than to the
// operator's session.
func TestFocusScrollOnAnAdoptedPaneCapturesEveryNotch(t *testing.T) {
	m := adoptedWithHistory(t)
	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected")
	}
	rows := m.previewPaneHeight()
	notches := 0
	for i := 0; i < 40 && m.focusScroll < m.pane.history-rows; i++ {
		before := m.focusScroll
		cmd := m.scrollFocus(-1)
		if m.focusScroll == before {
			break
		}
		if cmd == nil {
			t.Fatal("a notch on an adopted pane was answered from memory")
		}
		updated, _ := m.Update(cmd())
		*m = *updated.(*Model)
		notches++
		assertFrameMatchesTmux(t, m, sess.ID, rows)
	}
	if notches == 0 {
		t.Fatal("the wheel never moved; the comparison proves nothing")
	}
	t.Logf("%d notches, each a capture against the pane's own server", notches)
}

// A pane that writes moves every history line up under the number it was
// read by. This is the case the deleted cache got wrong for a while, and the
// reason it needed the mirror's paint events: a notch taken after the write
// has to show where those lines are now.
func TestANotchAfterThePaneWritesShowsWhereTheLinesAreNow(t *testing.T) {
	m, sessID := focusedWithHistory(t, "notchstale")
	deepen(t, m, sessID)
	rows := m.previewPaneHeight()

	// Go deep enough that the notch after the write is entirely history: a
	// frame still touching the live screen is a different case, covered by
	// the live preview rather than by a region read.
	for m.focusScroll < 2*rows {
		before := m.focusScroll
		if cmd := m.scrollFocus(-1); cmd != nil {
			updated, _ := m.Update(cmd())
			*m = *updated.(*Model)
		}
		if m.focusScroll == before {
			t.Skip("pane has too little history to scroll clear of the live screen")
		}
	}

	// Move the pane's history under the frame on screen.
	before := paneHistorySize(t, m, sessID)
	if err := m.tmux.SendText(sessID, `printf 'moved-%d\n' 1 2 3 4 5 6 7 8 9 10`); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for paneHistorySize(t, m, sessID) == before {
		if time.Now().After(deadline) {
			t.Fatal("the pane never wrote")
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitPaneQuiet(t, m, sessID)

	if cmd := m.scrollFocus(-1); cmd != nil {
		updated, _ := m.Update(cmd())
		*m = *updated.(*Model)
	}
	assertFrameMatchesTmux(t, m, sessID, rows)
}

// An adopted pane keeps its own window size until the first resize pass
// reaches it, and that pass pins it to the preview panel. The panel has not
// moved, so nothing about the manager's own measurements changes -- but the
// pane reflowed, and every line it holds moved with it. The frame after the
// pin still has to be the frame the pane now holds.
func TestANotchAfterThePinReflowsTheePaneMatchesTmux(t *testing.T) {
	m := adoptedWithHistory(t)
	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected")
	}
	rows, width := m.previewPaneHeight(), m.previewPaneWidth()
	// Forget the pin the setup refresh already wrote, so the pass below is
	// the first one this pane has seen -- which is the state an adopted pane
	// is in until a refresh reaches it.
	delete(m.pane.geom, sess.ID)
	for m.focusScroll < 2*rows {
		before := m.focusScroll
		if cmd := m.scrollFocus(-1); cmd != nil {
			updated, _ := m.Update(cmd())
			*m = *updated.(*Model)
		}
		if m.focusScroll == before {
			t.Skip("pane has too little history to scroll clear of the live screen")
		}
	}

	// The resize pass, which is where an adopted pane first gets pinned.
	m.resizeNow(t)
	if got := m.pane.geom[sess.ID]; got != [2]int{width, rows} {
		t.Fatalf("resize pass left the pane at %v, want %v", got, [2]int{width, rows})
	}

	before := m.focusScroll
	if cmd := m.scrollFocus(-1); cmd != nil {
		updated, _ := m.Update(cmd())
		*m = *updated.(*Model)
	}
	if m.focusScroll == before {
		t.Fatal("the wheel stopped at the pin")
	}
	assertFrameMatchesTmux(t, m, sess.ID, rows)
}

// assertFrameMatchesTmux compares the frame on screen against the capture
// tmux answers for the same region, read straight off the pane's own server.
func assertFrameMatchesTmux(t *testing.T, m *Model, sessID string, rows int) {
	t.Helper()
	want, err := m.tmux.CaptureRegion(sessID, -m.focusScroll, rows-1-m.focusScroll)
	if err != nil {
		t.Fatalf("direct capture at offset %d: %v", m.focusScroll, err)
	}
	if got := m.preview; got != want {
		t.Fatalf("frame at offset %d does not match tmux\n got:\n%s\nwant:\n%s",
			m.focusScroll, firstLines(got), firstLines(want))
	}
}

// waitPaneQuiet holds until the pane stops adding history, so a comparison
// against a second capture is not racing the output of the first.
func waitPaneQuiet(t testing.TB, m *Model, sessID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	last := -1
	for {
		size := paneHistorySize(t, m, sessID)
		if size == last {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane never went quiet")
		}
		last = size
		time.Sleep(120 * time.Millisecond)
	}
}

func firstLines(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) > 6 {
		lines = lines[:6]
	}
	return strings.Join(lines, "\n")
}
