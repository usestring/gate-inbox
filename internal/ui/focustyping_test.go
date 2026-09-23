package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// typingRig is a focused session with a rail the size of a real board behind
// it. The rail is what a keystroke's frame mostly costs, so measuring the
// focused path against a one-session model would measure a screen nobody has.
//
// The one real session is the focus target; the rest are rows. Nothing in
// this test drives a poll, so the padding rows are never asked for a pane.
//
// The rig used to stand up a control-mode mirror here and wait for it to
// serve the pane, because that mirror was where a focused frame came from.
// It is gone -- see focusecho.go -- and frames now come from the pane's own
// capture, so the rig seeds one the same way the focused tick does.
func typingRig(t testing.TB, rows int) *Model {
	t.Helper()
	m := buildModel(t)
	createSession(t, m, "typed", t.TempDir(), "")
	sess := m.sessions[0]
	m.sessions = append(m.sessions, fleetSessions(rows)...)
	m.groups = fleetGroups[1:]
	m.rebuildRows()
	m.selectSessionRow(t, "typed")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	// One real capture on screen, so the first measured keystroke is
	// measured against a warm render rather than the first fill of every
	// memo -- and so the echo chase has a frame to tell "changed" from.
	cmd := m.focusCaptureCmd(sess.ID, m.previewGen, true, captureGate{force: true})
	if cmd == nil {
		t.Fatal("the focused session produced no capture command")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("capturing the focused pane returned nothing")
	}
	updated, _ = m.Update(msg)
	*m = *updated.(*Model)
	m.frameReuse = false
	m.frame()
	return m
}

// typedKeys is a run long enough to average out one scheduling hiccup and
// short enough to stay well inside the pane's own line.
const typedKeys = 60

// The breakdown a keystroke pays, phase by phase: the handler that puts the
// key on the wire, and the frame Bubble Tea paints after it. The timings are
// reported rather than ranked -- the point is which of the two dominates, and
// a millisecond budget is what a loaded box flakes on. What is asserted is
// the structural claim behind them: over a whole run of typing, not one key
// asked for a frame.
func TestFocusedKeystrokeLatencyBreakdown(t *testing.T) {
	m := typingRig(t, 87)
	var send, paint time.Duration
	repainted, armed, owed := 0, 0, 0
	for i := 0; i < typedKeys; i++ {
		msg := tea.KeyPressMsg{Code: 'a', Text: "a"}
		start := time.Now()
		updated, cmd := m.Update(msg)
		*m = *updated.(*Model)
		mid := time.Now()
		if cmd != nil {
			armed++
		} else if m.echoPending {
			owed++
			m.echoPending = false
		}
		if !m.frameReuse {
			repainted++
		}
		if m.frame() == "" {
			t.Fatal("no frame was painted")
		}
		send += mid.Sub(start)
		paint += time.Since(mid)
	}
	if m.errBar.text != "" {
		t.Fatalf("forwarding set err: %q", m.errBar.text)
	}
	t.Logf("focused keystroke, averaged over %d keys on a %d-row rail:", typedKeys, len(m.sessions))
	t.Logf("  forward to the pane: %v", (send / typedKeys).Round(time.Microsecond))
	t.Logf("  frame after it:      %v", (paint / typedKeys).Round(time.Microsecond))
	t.Logf("  total:               %v", ((send + paint) / typedKeys).Round(time.Microsecond))
	if repainted != 0 {
		t.Fatalf("%d of %d forwarded keys asked for a frame nothing on screen needed", repainted, typedKeys)
	}
	// The frame the key does not paint is the frame the echo brings back. A
	// character with nothing coming for it waits for the idle tick, which is
	// the 300ms the mirror's removal was meant to take out, not put back.
	//
	// "Something coming" is one chase, not one per key. A chase is a loop of
	// forked captures and typing repeats faster than one completes, so a key
	// arriving while a chase is out leaves the chase owed instead: when that
	// chase lands, trailingEcho arms one more, measured against the frame it
	// brought back. Nothing here lands one -- this run is 60 keys with no
	// event loop under it -- so what every key must have is one or the other.
	if armed+owed != typedKeys {
		t.Fatalf("%d of %d forwarded keys neither armed an echo nor left one owed, so their character had nothing to bring it back",
			typedKeys-armed-owed, typedKeys)
	}
	if armed != 1 {
		t.Fatalf("%d chases armed across %d keys with none landing; one at a time is what keeps a held key from forking per keystroke",
			armed, typedKeys)
	}
}

// Typing fast must reach the pane as typed: every character, once, in order.
// The forward is a write down a pipe the manager already holds, and a
// coalescing or reordering bug there is invisible in the model, which
// records nothing about what it sent.
func TestFastTypingReachesThePaneInOrder(t *testing.T) {
	m := typingRig(t, 4)
	sess, ok := m.selected()
	if !ok {
		t.Fatal("nothing selected in focus mode")
	}
	const typed = "abcdefghijklmnopqrstuvwxyz0123456789"
	for _, r := range typed {
		updated, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		*m = *updated.(*Model)
	}
	if m.errBar.text != "" {
		t.Fatalf("forwarding set err: %q", m.errBar.text)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(sess.ID)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if strings.Contains(pane, typed) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the typed run never arrived intact; the pane holds %q", pane)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The reused frame has to be the frame that would have been painted. This is
// the whole claim: the operator sees the same screen either way, and the only
// difference is that nobody spent 2ms arriving at it.
func TestForwardedKeyPaintsTheFrameItWouldHavePainted(t *testing.T) {
	m := typingRig(t, 12)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	*m = *updated.(*Model)
	if !m.frameReuse {
		t.Fatal("a forwarded key that changed nothing still claimed the frame had moved")
	}
	reused := m.frame()
	m.frameReuse = false
	repainted := m.frame()
	if reused != repainted {
		t.Fatalf("the reused frame is not the frame the renderer would have built\nreused:\n%s\nrepainted:\n%s", reused, repainted)
	}
}

// A key that catches a scrolled pane up to the live bottom takes the
// "scrolled N lines back" notice off the status bar, so it has to paint.
func TestAKeyThatCatchesUpAScrolledPanePaints(t *testing.T) {
	m := typingRig(t, 12)
	m.focusScroll = 3
	m.frameReuse = false
	before := m.frame()
	if !strings.Contains(ansi.Strip(before), "scrolled") {
		t.Fatalf("a scrolled pane did not say so on the status bar:\n%s", before)
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	*m = *updated.(*Model)
	if m.frameReuse {
		t.Fatal("a key that caught the pane up claimed the frame had not moved")
	}
	// The region read is what paints the live bottom this key pulled the
	// pane back to, and it must be the only capture in flight: an echo chase
	// alongside it would race a live-bottom frame against the region one and
	// the view would land on whichever finished last.
	if cmd == nil {
		t.Fatal("a key that caught the pane up asked for no capture at all")
	}
	if _, ok := cmd().(focusScrollMsg); !ok {
		t.Fatalf("a key that caught the pane up returned %T, want the region read alone", cmd())
	}
	if after := m.frame(); strings.Contains(ansi.Strip(after), "scrolled") {
		t.Fatalf("the frame after the key still says the pane is scrolled back:\n%s", after)
	}
}

// A key pressed while the caret is blinked out turns it back on, which is a
// cell on screen changing, so that key paints too.
func TestAKeyThatRelightsTheCaretPaints(t *testing.T) {
	m := typingRig(t, 12)
	m.cursorOn = false
	m.frameReuse = false
	dark := m.frame()
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	*m = *updated.(*Model)
	if m.frameReuse {
		t.Fatal("a key that relit the caret claimed the frame had not moved")
	}
	if lit := m.frame(); lit == dark {
		t.Fatal("the caret was off, the key turned it on, and the frame did not change")
	}
}

// A tool redraws around a prompt being written -- growing its input box,
// opening a completion menu -- and the poller must know that repaint was
// the operator's, not the agent's.
func TestForwardedTypingTellsThePoller(t *testing.T) {
	m := typingRig(t, 1)
	sess := m.sessions[0]
	if m.poller.operatorEcho(sess.ID) {
		t.Fatal("an untouched session should carry no forwarded-input stamp")
	}
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	*m = *updated.(*Model)
	if !m.poller.operatorEcho(sess.ID) {
		t.Fatal("a keystroke did not tell the poller it forwarded input")
	}
}

// The same for a paste, which is the other way text reaches the pane.
func TestForwardedPasteTellsThePoller(t *testing.T) {
	m := typingRig(t, 1)
	sess := m.sessions[0]
	updated, _ := m.Update(tea.PasteMsg{Content: "some pasted prompt"})
	*m = *updated.(*Model)
	if !m.poller.operatorEcho(sess.ID) {
		t.Fatal("a paste did not tell the poller it forwarded input")
	}
}
