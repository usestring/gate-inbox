package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// One notch must settle. applyFocusScroll re-reads whenever the frame it
// landed was captured for a target that has since moved -- a different
// offset, or a panel of a different size -- and nothing bounds how often it
// may do that. If the size it compares against can differ between the
// request and the reply, a single notch re-reads forever: a fork per read,
// which is a pinned core and a view that never moves.
func TestOneNotchSettlesInsteadOfReReading(t *testing.T) {
	m, sessID := listWithHistory(t, "scrollloop")
	deepen(t, m, sessID)
	m.pane.history = paneHistorySize(t, m, sessID)

	cmd := m.scrollFocus(-1)
	if cmd == nil {
		t.Fatal("the notch scheduled no read")
	}
	reads := 0
	for cmd != nil {
		reads++
		if reads > 20 {
			t.Fatalf("one notch has issued %d reads without settling: a re-read loop", reads)
		}
		msg := cmd()
		if msg == nil {
			break
		}
		updated, follow := m.Update(msg)
		*m = *updated.(*Model)
		cmd = follow
	}
	t.Logf("one notch settled after %d read(s)", reads)
}

// The same, driven the way the operator drives it: a run of alt+up keys with
// each reply landing before the next key, as the event loop would.
func TestAltArrowRunSettlesEveryStep(t *testing.T) {
	m, sessID := listWithHistory(t, "scrollloopkeys")
	deepen(t, m, sessID)
	m.pane.history = paneHistorySize(t, m, sessID)

	total := 0
	for i := 0; i < 12; i++ {
		updated, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
		*m = *updated.(*Model)
		reads := 0
		for cmd != nil {
			reads++
			if reads > 20 {
				t.Fatalf("alt+up %d issued %d reads without settling", i+1, reads)
			}
			msg := cmd()
			if msg == nil {
				break
			}
			updated, follow := m.Update(msg)
			*m = *updated.(*Model)
			cmd = follow
		}
		total += reads
	}
	if total > 24 {
		t.Fatalf("12 notches cost %d reads; a notch should cost about one", total)
	}
	t.Logf("12 notches cost %d reads", total)
}

// A flick is dozens of notches inside one frame, and the event loop hands
// them over one after another without waiting for any read to come back. Each
// read is a fork, so one per notch is what pinned a core on the operator's
// board: two hundred captures in flight in one second, each then taking 1.8s,
// each landing stale and asking for another.
func TestABurstOfNotchesCostsOneReadNotOnePerNotch(t *testing.T) {
	m, sessID := listWithHistory(t, "scrollburst")
	deepen(t, m, sessID)
	m.pane.history = paneHistorySize(t, m, sessID)

	// Nothing is drained in between: this is the burst as the event loop
	// sees it, every notch arriving while the first read is still out.
	var pending []tea.Cmd
	for i := 0; i < 40; i++ {
		if cmd := m.scrollFocus(-1); cmd != nil {
			pending = append(pending, cmd)
		}
	}
	if len(pending) != 1 {
		t.Fatalf("a 40-notch burst scheduled %d reads, want 1", len(pending))
	}
	if m.focusScroll != 40*focusScrollStep {
		t.Fatalf("the burst moved the offset to %d, want %d", m.focusScroll, 40*focusScrollStep)
	}

	// The one read lands stale -- the offset moved 39 times while it was out
	// -- so it issues exactly one more, for where the burst actually ended.
	updated, follow := m.Update(pending[0]())
	m = updated.(*Model)
	if follow == nil {
		t.Fatal("the stale frame did not re-read for where the burst ended")
	}
	updated, again := m.Update(follow())
	m = updated.(*Model)
	if again != nil {
		t.Fatal("the second read did not settle")
	}
	assertFrameMatchesTmux(t, m, sessID, m.previewPaneHeight())

	// And the pane is readable again afterwards: a flag left set would mean
	// no notch on any row ever reads again.
	if cmd := m.scrollFocus(-1); cmd == nil {
		t.Fatal("the next notch after a burst scheduled no read")
	}
}

// The same burst against a pane that owns the mouse: every notch reaches the
// agent, because the operator asked for that distance, but only one chase
// looks for what it did. A chase is a loop of forked captures.
func TestABurstOfForwardedNotchesArmsOneChase(t *testing.T) {
	m, _ := listWithHistory(t, "forwardburst")
	m.pane.mouse, m.pane.sgr, m.pane.history = true, true, 0
	x, y := previewCell(m)

	chases := 0
	for i := 0; i < 25; i++ {
		updated, cmd := m.handleMouse(wheel(true, x, y))
		m = updated.(*Model)
		if cmd != nil {
			chases++
		}
	}
	if chases != 1 {
		t.Fatalf("a 25-notch burst armed %d chases, want 1", chases)
	}

	// A frame landing means the manager has looked, so the next notch may
	// chase again -- otherwise one burst would silence the pane for good.
	updated, _ := m.Update(previewMsg{sessID: m.rows[m.cursor].sess.ID, preview: "a frame\n"})
	m = updated.(*Model)
	if _, cmd := m.handleMouse(wheel(true, x, y)); cmd == nil {
		t.Fatal("no chase armed after a frame landed")
	}
}
