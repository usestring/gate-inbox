package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The rule the whole saving rests on, stated against a stamp that cannot say
// more than which second the pane wrote in. See shouldCapture.
func TestShouldCaptureReadsTheActivityStamp(t *testing.T) {
	stamped := func(sec int64) paneFacts {
		return paneFacts{activity: sec, activityOK: true}
	}

	tests := []struct {
		name  string
		gate  captureGate
		facts paneFacts
		want  bool
	}{
		{
			name:  "quiet pane, clock has left the stamped second",
			gate:  captureGate{sinceSec: 101},
			facts: stamped(100),
			want:  false,
		},
		{
			name: "the last look was taken inside the second the pane wrote in, " +
				"so output may have landed behind it",
			gate:  captureGate{sinceSec: 100},
			facts: stamped(100),
			want:  true,
		},
		{
			name:  "the pane wrote again",
			gate:  captureGate{sinceSec: 101},
			facts: stamped(140),
			want:  true,
		},
		{
			name:  "no stamp is not a promise the pane held still",
			gate:  captureGate{sinceSec: 101},
			facts: paneFacts{},
			want:  true,
		},
		{
			name:  "a keystroke the pane may have swallowed is still owed a look",
			gate:  captureGate{force: true, sinceSec: 101},
			facts: stamped(100),
			want:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldCapture(tt.gate, tt.facts); got != tt.want {
				t.Fatalf("shouldCapture = %v, want %v", got, tt.want)
			}
		})
	}
}

// captureGate is assembled on the event loop from the things the stamp
// cannot report, and every one of them has to force the fork.
func TestCaptureGateForcesWhatTheStampCannotSee(t *testing.T) {
	m := &Model{focusCaptureFor: "s1", focusCaptureSec: 100, focusCaptureGeom: [2]int{80, 24}}
	m.pane.geom = map[string][2]int{"s1": {80, 24}}

	if gate := m.captureGate("s1"); gate.force || gate.sinceSec != 100 {
		t.Fatalf("a quiet pane at an unchanged size: gate = %+v", gate)
	}
	if gate := m.captureGate("s2"); !gate.force {
		t.Fatal("a pane the board has never captured did not force a capture")
	}

	m.pane.geom["s1"] = [2]int{100, 24}
	if gate := m.captureGate("s1"); !gate.force {
		t.Fatal("a pane tmux was just told a new size for did not force a capture")
	}
	m.pane.geom["s1"] = [2]int{80, 24}

	m.noteFocusActivity()
	if gate := m.captureGate("s1"); !gate.force {
		t.Fatal("a keystroke did not force a capture")
	}
	m.focusActiveAt = time.Time{}

	m.noteFocusWheel()
	if gate := m.captureGate("s1"); !gate.force {
		t.Fatal("a wheel notch did not force a capture")
	}
	m.focusWheelAt = time.Time{}

	m.noteFocusRepaint()
	if gate := m.captureGate("s1"); !gate.force {
		t.Fatal("a pane repainting on its own did not force a capture")
	}
}

// What the gate is measured against is "when did the board last actually
// look at this pane", and only a frame whose timestamp was taken before its
// capture can say that.
func TestOnlyAFrameStampedBeforeItsCaptureMovesTheMark(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "mark", t.TempDir(), "")
	m.selectSessionRow(t, "mark")
	sess := m.rows[m.cursor].sess

	at := time.Now()
	m.applyTickBatch(t, previewMsg{sessID: sess.ID, at: at, preview: "a tick's frame"})
	if m.focusCaptureSec != at.Unix() {
		t.Fatalf("a tick's frame left the mark at %d, want %d", m.focusCaptureSec, at.Unix())
	}

	// A chase stamps itself after its capture, so its second can be one
	// past the look it actually took.
	later := at.Add(2 * time.Second)
	m.applyTickBatch(t, previewMsg{sessID: sess.ID, at: later, preview: "a chase's frame", chase: true, echoed: true})
	if m.focusCaptureSec != at.Unix() {
		t.Fatalf("a chase's frame moved the mark to %d, want it left at %d", m.focusCaptureSec, at.Unix())
	}

	// A tick that forked nothing has looked at nothing: it must move
	// neither the mark nor the age of the frame on screen, or a real
	// capture still in flight behind it would be dropped as stale.
	was := m.previewAt
	m.applyTickBatch(t, previewMsg{sessID: sess.ID, skipped: true, factsOK: true})
	if m.focusCaptureSec != at.Unix() || !m.previewAt.Equal(was) {
		t.Fatalf("a skipped tick moved the mark to %d or aged the frame to %v", m.focusCaptureSec, m.previewAt)
	}
	if m.preview != "a chase's frame" {
		t.Fatalf("a skipped tick painted over the frame on screen: %q", m.preview)
	}
}

// tickCapture runs one preview tick the way the event loop does and reports
// the frame it produced, if it produced one at all.
func tickCapture(t *testing.T, m *Model) (previewMsg, bool) {
	t.Helper()
	updated, cmd := m.Update(previewTickMsg{})
	*m = *updated.(*Model)
	if cmd == nil {
		return previewMsg{}, false
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		return previewMsg{}, false
	}
	var got previewMsg
	var have bool
	for _, one := range batch {
		if one == nil {
			continue
		}
		out := one()
		if _, repeat := out.(previewTickMsg); out == nil || repeat {
			continue
		}
		if msg, isPreview := out.(previewMsg); isPreview {
			got, have = msg, true
		}
		updated, _ := m.Update(out)
		*m = *updated.(*Model)
	}
	return got, have
}

// The point of the whole change, measured against a real tmux pane: a
// focused pane nobody is typing into and whose agent has stopped writing
// stops costing forks entirely, and starts again the moment it writes.
func TestAQuietFocusedPaneStopsForkingCaptures(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "quiet", t.TempDir(), "")
	m.selectSessionRow(t, "quiet")
	sess := m.rows[m.cursor].sess
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("did not enter focus: %q", m.errBar.text)
	}
	// Entering focus is a keystroke, and a keystroke forces a capture for
	// focusActiveFor after it. Nothing here is about that window.
	m.focusActiveAt, m.focusWheelAt, m.focusRepaintAt = time.Time{}, time.Time{}, time.Time{}

	// The pane has just launched, so it is still inside the second it wrote
	// in. Tick until it falls quiet, which by shouldCapture's rule can take
	// no longer than the clock needs to leave that second.
	quiet := false
	deadline := time.Now().Add(5 * time.Second)
	for !quiet && time.Now().Before(deadline) {
		msg, ok := tickCapture(t, m)
		quiet = ok && msg.skipped
		if !quiet {
			time.Sleep(focusIntervalIdle)
		}
	}
	if !quiet {
		t.Fatal("a focused pane that had not written for seconds never stopped forking captures")
	}

	// From here it forks nothing at all: every tick reads the stamp over the
	// pooled pipe and stands down.
	forked := 0
	for i := 0; i < 8; i++ {
		msg, ok := tickCapture(t, m)
		if !ok {
			t.Fatalf("tick %d produced no message at all", i)
		}
		if !msg.skipped {
			forked++
		}
		// The facts still land, so the caret does not go stale behind the
		// saving.
		if !msg.factsOK || !msg.facts.activityOK {
			t.Fatalf("tick %d landed no pane facts: %+v", i, msg.facts)
		}
		time.Sleep(focusIntervalIdle)
	}
	if forked != 0 {
		t.Fatalf("a quiet focused pane still forked %d of 8 captures", forked)
	}

	// It writes. The very next tick captures -- the stamp is read inside the
	// same tick, so waking up costs no extra cadence.
	if err := m.tmux.SendText(sess.ID, "the agent says something\n"); err != nil {
		t.Fatalf("send text: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	msg, ok := tickCapture(t, m)
	if !ok || msg.skipped {
		t.Fatalf("the tick after the pane wrote did not capture: ok=%v msg=%+v", ok, msg)
	}
}
