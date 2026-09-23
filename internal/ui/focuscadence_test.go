package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A focused pane has three cadences, and which one it gets is decided by who
// last changed it: the operator, the agent, or nobody. What each rate is for
// is written above focusIntervalStream.
func TestARepaintingPaneIsNotSampledAtTheKeystrokeRate(t *testing.T) {
	if focusIntervalStream <= focusIntervalActive {
		t.Fatalf("streaming cadence %v is no slower than the keystroke cadence %v; a repaint would cost what a keystroke does",
			focusIntervalStream, focusIntervalActive)
	}
	if focusIntervalStream >= focusIntervalIdle {
		t.Fatalf("streaming cadence %v is no faster than the idle one %v; a pane writing output would read as abandoned",
			focusIntervalStream, focusIntervalIdle)
	}

	m := &Model{mode: modeFocus, rows: []treeRow{{sess: store.Session{Status: status.Working}}}}
	// The pane wrote something. Nobody has typed.
	m.setPreview("frame one")
	if got := m.previewInterval(); got != focusIntervalStream {
		t.Fatalf("pane repainted with nobody typing: interval = %v, want %v", got, focusIntervalStream)
	}
	// The operator types into it, and the keystroke rate takes over.
	m.noteFocusActivity()
	if got := m.previewInterval(); got != focusIntervalActive {
		t.Fatalf("pane typed into: interval = %v, want %v", got, focusIntervalActive)
	}
	// They stop typing while the agent keeps writing: back to the rate the
	// output earns, not down to the idle one.
	m.focusActiveAt = time.Now().Add(-focusActiveFor - time.Millisecond)
	m.setPreview("frame two")
	if got := m.previewInterval(); got != focusIntervalStream {
		t.Fatalf("pane still writing after the typing stopped: interval = %v, want %v", got, focusIntervalStream)
	}
	// The agent finishes and the pane holds still.
	m.focusRepaintAt = time.Now().Add(-focusActiveFor - time.Millisecond)
	if got := m.previewInterval(); got != focusIntervalIdle {
		t.Fatalf("pane left alone: interval = %v, want %v", got, focusIntervalIdle)
	}
	// A frame identical to the one on screen is not a repaint.
	m.setPreview("frame two")
	if got := m.previewInterval(); got != focusIntervalIdle {
		t.Fatalf("unchanged frame counted as a repaint: interval = %v, want %v", got, focusIntervalIdle)
	}
}

// The cadence is a rate at which to ask, not a rate at which to fork.
//
// The burst below is drained by nothing on purpose: a test that answers each
// capture before issuing the next tick cannot see a stacked capture at all.
func TestFocusTicksDoNotStackCaptures(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "cadence", t.TempDir(), "")
	m.selectSessionRow(t, "cadence")
	sess := m.rows[m.cursor].sess
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("did not enter focus: %q", m.errBar.text)
	}

	if _, _ = m.Update(previewTickMsg{}); !m.focusCapturing {
		t.Fatal("the first tick took no capture")
	}
	// The stamp is what says a capture went out, and it is the only thing
	// that does. frameReuse used to stand in for it, back when standing down
	// was the only way a tick could leave the frame alone; a tick that arms a
	// capture leaves it alone too -- nothing it writes is read by any view --
	// so the flag no longer tells the two apart.
	armed := m.focusCapturingAt
	for i := 0; i < 20; i++ {
		_, _ = m.Update(previewTickMsg{})
		if !m.focusCapturingAt.Equal(armed) {
			t.Fatalf("tick %d issued a second capture while the first was still out", i+2)
		}
	}

	// The frame lands, and the next tick looks again.
	_, _ = m.Update(previewMsg{sessID: sess.ID, at: time.Now(), preview: "landed"})
	if m.focusCapturing {
		t.Fatal("a landed frame left the capture marked as still out")
	}
	if _, _ = m.Update(previewTickMsg{}); !m.focusCapturing {
		t.Fatal("the tick after a landed frame took no capture")
	}

	// A capture that never comes back must not silence the cadence for good.
	lost := time.Now().Add(-focusCaptureStale - time.Millisecond)
	m.focusCapturingAt = lost
	_, _ = m.Update(previewTickMsg{})
	if m.focusCapturingAt.Equal(lost) {
		t.Fatal("a capture lost for longer than focusCaptureStale still held the cadence off")
	}
}
