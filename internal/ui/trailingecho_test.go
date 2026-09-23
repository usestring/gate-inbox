package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Typing has the shape a flick has: it repeats faster than a chase completes.
// One chase per key forks a capture per key, which is what pinned a core on
// the wheel -- but a key that arms nothing has still put a character on the
// pane, and it must not wait for a tick to appear. So the keys behind a chase
// leave it owed, and the frame that chase brings back arms one more.
func TestAHeldKeyArmsOneChaseAndOwesATrailingOne(t *testing.T) {
	m, _ := focusedWithHistory(t, "trailingecho")
	sess, ok := m.selected()
	if !ok {
		t.Fatal("no session selected")
	}

	chases := 0
	for i := 0; i < 25; i++ {
		updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
		*m = *updated.(*Model)
		if cmd != nil {
			chases++
		}
	}
	if chases != 1 {
		t.Fatalf("25 keystrokes armed %d chases, want 1", chases)
	}
	if !m.echoPending {
		t.Fatal("the keys behind the chase left nothing owed; their characters would wait for a tick")
	}

	// The chase lands. What it brought back may predate the keys behind it,
	// so one more chase follows, measured against that frame.
	updated, cmd := m.Update(previewMsg{sessID: sess.ID, preview: "a frame\n"})
	*m = *updated.(*Model)
	if cmd == nil {
		t.Fatal("the landed frame armed no trailing chase, so the keys behind it wait for a tick")
	}
	if m.echoPending {
		t.Fatal("the trailing chase was armed and the debt kept; it would arm again on every frame")
	}
	if !m.focusChasing {
		t.Fatal("the trailing chase is out but nothing records it; the next key would arm a second")
	}

	// A frame landing with nothing owed arms nothing: a quiet pane costs no
	// captures at all, which is the whole point of a chase armed by input.
	updated, cmd = m.Update(previewMsg{sessID: sess.ID, preview: "another frame\n"})
	*m = *updated.(*Model)
	if cmd != nil {
		t.Fatal("a frame armed a chase with no input behind it")
	}
	if _, next := m.handleKey(tea.KeyPressMsg{Code: 'b', Text: "b"}); next == nil {
		t.Fatal("no chase armed for a key typed after the chase landed")
	}
}
