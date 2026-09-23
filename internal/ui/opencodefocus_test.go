package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// opencodePoses reloads the engine with the shipped defaults -- buildModel's
// fixture config carries no opencode tool -- and poses the live preview and
// caret a focused opencode session was measured with.
func opencodePosed(t *testing.T, preview string, cursor paneCursor, boxRows, boxCols int) (*Model, string) {
	t.Helper()
	m := &Model{engine: liveEngine(t), mode: modeFocus}
	m.preview = preview
	if !strings.HasSuffix(m.preview, "\n") {
		m.preview += "\n"
	}
	m.pane.forID = "s1"
	m.pane.box.height, m.pane.box.width = boxRows, boxCols
	m.pane.cursor = cursor
	return m, "s1"
}

// The reported bug: opencode parks the caret outside the question dialog it
// is asking from -- at the end of the "→ Asked 1 question" summary line
// above the composer box, measured live on 1.18.30 -- so the caret-on-the-
// marker rule never fires and Left was forwarded into a dialog that does
// nothing with the horizontal arrows. The operator stayed pinned in a
// session that is asking them something, exactly when they most want to
// step out and come back.
//
// The fixture is a verbatim "tmux capture-pane -p" frame of that state:
// the dialog, its "↑↓ select  enter submit  esc dismiss" legend, and the
// summary line the caret sits on.
func TestLeftLeavesOpencodeQuestionDialog(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "opencode-question-dialog.txt"))
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	rows := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(rows) != 50 || !strings.Contains(rows[31], "Asked 1 question") {
		t.Fatalf("the fixture no longer carries the dialog frame this pins: %d rows, caret row %q", len(rows), rows[31])
	}
	m, sessID := opencodePosed(t, string(raw), paneCursor{x: 24, y: 31, ok: true}, 50, 200)

	if m.caretAtInputStart(sessID, "opencode") {
		t.Fatal("the summary line read as the head of a prompt")
	}
	if !m.opencodeQuestionDialogUp() {
		t.Fatal("the question dialog was not recognised, so Left cannot leave focus")
	}
	if !m.leftLeavesFocus(sessID, "opencode") {
		t.Fatal("Left does not leave an opencode question dialog, so the operator is pinned in a session that is asking them something")
	}
}

// The counter-case: the permission overlay steps its options with Left
// ("⇆ select"), so Left stays the pane's there. The caret is posed on a
// transcript row outside the composer box, the same parked-outside shape as
// the question dialog -- what differs is the legend, not the caret.
func TestLeftStaysOnOpencodePermissionOverlay(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "status", "testdata", "opencode-permission-bash.txt"))
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	m, sessID := opencodePosed(t, string(raw), paneCursor{x: 5, y: 6, ok: true}, 50, 200)

	if m.opencodeQuestionDialogUp() {
		t.Fatal("the permission overlay read as a question dialog")
	}
	if m.leftLeavesFocus(sessID, "opencode") {
		t.Fatal("Left left a permission overlay, stealing the option step from the pane")
	}
}

// An empty boxed composer still leaves by the caret path, dialog or not.
func TestLeftLeavesOpencodeEmptyComposer(t *testing.T) {
	m, sessID := opencodePosed(t, "  ┃\n  ┃\n  ╹▀▀▀\n", paneCursor{x: 5, y: 1, ok: true}, 3, 80)
	if !m.leftLeavesFocus(sessID, "opencode") {
		t.Fatal("Left does not leave an empty opencode composer")
	}
}

// A custom answer being typed into the dialog stays the pane's: Left there
// is line editing, and the caret sitting past typed text on a composer row
// is what says so.
func TestLeftStaysWhileTypingOpencodeCustomAnswer(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "opencode-question-dialog.txt"))
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	// Row 40 of the fixture is the first option row inside the box; a caret
	// past its text is a half-written answer, wherever the rest of the
	// dialog is.
	m, sessID := opencodePosed(t, string(raw), paneCursor{x: 20, y: 39, ok: true}, 50, 200)
	if m.leftLeavesFocus(sessID, "opencode") {
		t.Fatal("Left left while a custom answer was being typed, stealing line editing from the pane")
	}
}

// End to end: the key that was swallowed now returns to the list.
func TestFocusLeftKeyLeavesOpencodeQuestionDialog(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "ocleft", t.TempDir(), "")
	m.selectSessionRow(t, "ocleft")

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	*m = *updated.(*Model)
	if m.mode != modeFocus {
		t.Fatalf("after enter, mode = %v, err = %q", m.mode, m.errBar.text)
	}
	// buildModel's fixture config carries no opencode tool; the shipped
	// defaults are what an operator focuses with.
	m.engine = liveEngine(t)
	sess := m.rows[m.cursor].sess
	m.rows[m.cursor].sess.Tool = "opencode"
	raw, err := os.ReadFile(filepath.Join("testdata", "opencode-question-dialog.txt"))
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	m.preview = string(raw)
	m.pane.forID = sess.ID
	m.pane.box.height, m.pane.box.width = 50, 200
	m.pane.cursor = paneCursor{x: 24, y: 31, ok: true}

	updated, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	*m = *updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("Left did not leave the opencode dialog, mode = %v, err = %q", m.mode, m.errBar.text)
	}
}
