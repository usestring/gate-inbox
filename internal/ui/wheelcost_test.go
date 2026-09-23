package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A notch is not a keystroke. Under focus it earns the streaming rate, the
// one a person reading can follow, never the 12ms keystroke rate; in the
// list it still lifts a calm preview to the live cadence, since the frames
// it provokes must not wait out previewIntervalCalm.
func TestAWheelTurnIsNotTyping(t *testing.T) {
	m := &Model{mode: modeFocus, rows: []treeRow{{sess: store.Session{Status: status.Idle}}}}
	m.noteFocusWheel()
	if m.focusActive() {
		t.Fatal("a notch marked the pane as typed into")
	}
	if got := m.previewInterval(); got != focusIntervalStream {
		t.Fatalf("focused pane under the wheel: interval = %v, want %v", got, focusIntervalStream)
	}
	m.mode = modeList
	if got := m.previewInterval(); got != previewIntervalLive {
		t.Fatalf("previewed pane under the wheel: interval = %v, want %v", got, previewIntervalLive)
	}
	m.focusWheelAt = time.Now().Add(-focusActiveFor - time.Millisecond)
	if got := m.previewInterval(); got != previewIntervalCalm {
		t.Fatalf("wheel long still: interval = %v, want %v", got, previewIntervalCalm)
	}
}

func enterFocus(t testing.TB, name string) (*Model, store.Session) {
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
	return m, sess
}

// A chase is a capture loop already looking at the pane; a tick forking a
// second read beside it doubled a flick's capture rate for frames no newer
// than the chase's own. A chase that never returns must not hold the tick
// off for good.
func TestTicksStandDownBehindAChase(t *testing.T) {
	m, _ := enterFocus(t, "chasetick")
	m.focusChasing, m.focusChasingAt = true, time.Now()
	m.frameReuse = false
	_, _ = m.Update(previewTickMsg{})
	if m.focusCapturing || !m.frameReuse {
		t.Fatal("a tick forked its own capture while a chase was out")
	}
	m.focusChasingAt = time.Now().Add(-focusCaptureStale - time.Millisecond)
	m.frameReuse = false
	_, _ = m.Update(previewTickMsg{})
	if !m.focusCapturing {
		t.Fatal("a chase lost for longer than focusCaptureStale still held the tick off")
	}
}

// Under the wheel each notch's chase lands a frame of its own, and a frame
// younger than one tick is the frame the tick would take. A pane being typed
// into keeps its tick: there the frame owed is the one behind the key.
func TestAWheelFrameCoversTheTick(t *testing.T) {
	m, _ := enterFocus(t, "wheeltick")
	m.focusActiveAt = time.Time{}
	m.noteFocusWheel()
	m.previewAt = time.Now()
	m.frameReuse = false
	_, _ = m.Update(previewTickMsg{})
	if m.focusCapturing || !m.frameReuse {
		t.Fatal("a tick captured behind a frame younger than itself")
	}
	m.previewAt = time.Now().Add(-focusIntervalStream - time.Millisecond)
	_, _ = m.Update(previewTickMsg{})
	if !m.focusCapturing {
		t.Fatal("a tick behind a frame older than one interval took no capture")
	}
	m.focusCapturing = false
	m.previewAt = time.Now()
	m.noteFocusActivity()
	_, _ = m.Update(previewTickMsg{})
	if !m.focusCapturing {
		t.Fatal("a pane being typed into lost its tick to a fresh wheel frame")
	}
}

// A tick that read the same frame, caret and pane claims as the ones on
// screen has nothing to paint; a frame whose caret moved does.
func TestAnUnchangedTickFrameIsNotRepainted(t *testing.T) {
	m, sess := enterFocus(t, "sameframe")
	facts := paneFacts{cursorX: 3, cursorY: 4, cursorOK: true, historySize: 7}
	_, _ = m.Update(previewMsg{sessID: sess.ID, at: time.Now(), preview: "frame", facts: facts, factsOK: true})
	if m.frameReuse {
		t.Fatal("the first frame of a pane was not painted")
	}
	_, _ = m.Update(previewMsg{sessID: sess.ID, at: time.Now(), preview: "frame", facts: facts, factsOK: true})
	if !m.frameReuse {
		t.Fatal("an identical frame with identical facts was painted again")
	}
	m.frameReuse = false
	facts.cursorX = 4
	_, _ = m.Update(previewMsg{sessID: sess.ID, at: time.Now(), preview: "frame", facts: facts, factsOK: true})
	if m.frameReuse {
		t.Fatal("a moved caret was not painted")
	}
	m.frameReuse = false
	_, _ = m.Update(previewMsg{sessID: sess.ID, at: time.Now(), preview: "frame", facts: facts, factsOK: true, chase: true})
	if m.frameReuse {
		t.Fatal("a chase frame reused the screen; its deaf count is part of the frame")
	}
}

// A flick lands a capture every few milliseconds, and each is a real read
// of the pane taken before the next notch: the chase's baseline, without a
// fork. Anything older, or a frame of history, is read afresh.
func TestAFreshFrameIsTheNotchsBaseline(t *testing.T) {
	m := &Model{preview: "on screen"}
	m.pane.forID = "s1"
	m.previewAt = time.Now()
	if got, ok := m.freshBaseline("s1"); !ok || got != "on screen" {
		t.Fatalf("fresh frame refused as baseline: %q %v", got, ok)
	}
	if _, ok := m.freshBaseline("s2"); ok {
		t.Fatal("another pane's frame served as baseline")
	}
	m.focusScroll = 3
	if _, ok := m.freshBaseline("s1"); ok {
		t.Fatal("a frame of history served as the live baseline")
	}
	m.focusScroll = 0
	m.previewAt = time.Now().Add(-wheelBaselineFresh - time.Millisecond)
	if _, ok := m.freshBaseline("s1"); ok {
		t.Fatal("a stale frame served as baseline")
	}
}
