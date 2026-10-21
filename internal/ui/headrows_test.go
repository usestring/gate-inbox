package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// headModel is rowMarksModel with "batch" opening its headers and "other"
// drawing them as lines, and every press of either recorded.
func headModel(t *testing.T) (*Model, *ExtensionBridge, *[]Press, *[]Press) {
	t.Helper()
	var opened, pressed []Press
	m, bridge := rowMarksModel(t,
		ExtensionUI{Owner: "batch", OpenHeader: func(press Press) error {
			opened = append(opened, press)
			return nil
		}, Keys: []ExtensionKey{{Action: "batch_poke", Keys: []string{"alt+p"}, Label: "poke", Run: func(press Press) error {
			pressed = append(pressed, press)
			return nil
		}}}},
		ExtensionUI{Owner: "other"},
	)
	return m, bridge, &opened, &pressed
}

func headIndex(t *testing.T, m *Model, owner, sessionID string) int {
	t.Helper()
	for i, row := range m.rows {
		if row.head == owner && row.sess.ID == sessionID {
			return i
		}
	}
	t.Fatalf("no %s header row over %s in %s", owner, sessionID, strings.Join(rowKeys(m), ","))
	return -1
}

// A header whose extension opens it is a row of its own, just above the
// session it heads; another extension's header over the same session stays a
// line of the session's entry.
func TestAnOpenedHeaderIsARowOfItsOwn(t *testing.T) {
	m, bridge, _, _ := headModel(t)
	rows := len(m.rows)
	bridge.Group("batch", "s9", []Span{{Text: "◈ ship it", Tone: ToneAccent}})
	bridge.Group("other", "s9", []Span{{Text: "a caption"}})
	m.Update(extensionBadgesMsg{})
	if len(m.rows) != rows+1 {
		t.Fatalf("rows = %s, want one header row added", strings.Join(rowKeys(m), ","))
	}
	at := headIndex(t, m, "batch", "s9")
	if next := m.rows[at+1]; !next.isSession() || next.sess.ID != "s9" {
		t.Fatalf("the header row is not directly over its session: %s", strings.Join(rowKeys(m), ","))
	}
	if m.rows[at].isSession() {
		t.Fatal("a header row reads as a session")
	}

	var lines []string
	for _, line := range m.entryLines(m.rows, 0, 80, 30) {
		lines = append(lines, ansi.Strip(line.text))
	}
	joinedLines := strings.Join(lines, "\n")
	if strings.Count(joinedLines, "◈ ship it") != 1 || !strings.Contains(joinedLines, "a caption") {
		t.Fatalf("want the header row drawn once and the other caption kept:\n%s", joinedLines)
	}

	bridge.Group("batch", "s9", nil)
	m.Update(extensionBadgesMsg{})
	if len(m.rows) != rows {
		t.Fatalf("rows = %s after the header was cleared", strings.Join(rowKeys(m), ","))
	}
}

// The cursor steps onto a header row; open there calls the extension with
// the session it heads and opens nothing else, and any other key on it acts
// on that session.
func TestOpenOnAHeaderRowOpensWhatItHeads(t *testing.T) {
	m, bridge, opened, pressed := headModel(t)
	bridge.Group("batch", "s9", []Span{{Text: "◈ ship it"}})
	m.Update(extensionBadgesMsg{})
	at := headIndex(t, m, "batch", "s9")
	m.cursor = m.stepCursor(at-1, 1)
	if m.cursor != at {
		t.Fatalf("a step from the row above landed on %d, want the header row %d (%s)", m.cursor, at, strings.Join(rowKeys(m), ","))
	}
	if sess, ok := m.selected(); !ok || sess.ID != "s9" {
		t.Fatalf("selected = %q, want the session the header heads", sess.ID)
	}

	_, cmd := m.handleKey(key("enter"))
	if cmd == nil {
		t.Fatal("open on a header row did nothing")
	}
	if msg, ok := cmd().(extensionKeyDoneMsg); !ok || msg.err != nil {
		t.Fatalf("open came back as %#v", msg)
	}
	if len(*opened) != 1 || (*opened)[0].SessionID != "s9" {
		t.Fatalf("opened = %#v, want the header's session once", *opened)
	}
	if m.mode != modeList {
		t.Fatalf("open on a header row left the list for %s", m.mode)
	}

	if _, cmd := m.handleKey(key("alt+p")); cmd != nil {
		cmd()
	}
	if len(*pressed) != 1 || (*pressed)[0].SessionID != "s9" {
		t.Fatalf("pressed = %#v, want a key on the header pressed on its session", *pressed)
	}
}

// Triage builds its queue from the same rows, so a header row comes along
// over a session waiting on the operator.
func TestAHeaderRowRidesIntoTriage(t *testing.T) {
	m, bridge, _, _ := headModel(t)
	bridge.Group("batch", "c2", []Span{{Text: "◈ ship it"}})
	m.Update(extensionBadgesMsg{})
	m.triage = true
	m.rebuildRows()
	at := headIndex(t, m, "batch", "c2")
	if next := m.rows[at+1]; !next.isSession() || next.sess.ID != "c2" {
		t.Fatalf("triage rows = %s, want the header over c2", strings.Join(rowKeys(m), ","))
	}
}
