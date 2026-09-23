package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func homeKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyHome} }

func endKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyEnd} }

// The counts either side of the cursor are what a jump promises: nothing the
// rail stops on is left beyond the row it landed on. Artifacts do not count,
// because the row the jump opened writes fresh ones under itself.
func nonArtifactsAbove(m *Model) int {
	count := 0
	for i := 0; i < m.cursor && i < len(m.rows); i++ {
		if !m.rows[i].isArtifact() {
			count++
		}
	}
	return count
}

func nonArtifactsBelow(m *Model) int {
	count := 0
	for i := m.cursor + 1; i < len(m.rows); i++ {
		if !m.rows[i].isArtifact() {
			count++
		}
	}
	return count
}

// home and end cross the fleet in one press, from wherever the cursor is,
// which is what a held j on eighty-odd sessions is too slow to be.
func TestHomeAndEndJumpToTheEndsOfTheList(t *testing.T) {
	m := undecidedFleet(t, fleetSize, 120, 40)
	m.cursor = len(m.rows) / 2
	m.rebuildRows()

	m.handleKey(homeKey())
	if m.cursor != 0 {
		t.Fatalf("home landed on row %d, want the top", m.cursor)
	}
	if row, ok := m.cursorRow(); !ok || row.isArtifact() {
		t.Fatalf("home landed on %+v, which is work and not a row the rail stops on", row)
	}

	m.handleKey(endKey())
	if got := nonArtifactsBelow(m); got != 0 {
		t.Fatalf("end left %d rows below the cursor (row %d of %d)", got, m.cursor, len(m.rows))
	}
	if row, ok := m.cursorRow(); !ok || row.isArtifact() {
		t.Fatalf("end landed on %+v, which is work and not a row the rail stops on", row)
	}

	// The rail draws a window around the cursor rather than a stored offset,
	// so a jump is only navigation if the row it lands on is on screen: at
	// eighty-odd sessions the bottom of the tree is several screens down.
	rail := ansi.Strip(railLinesText(m.railLines(80, m.listBodyHeight())))
	if label := rowLabel(m, m.rows[m.cursor]); !strings.Contains(rail, label) {
		t.Fatalf("end left %q off the drawn rail:\n%s", label, rail)
	}

	m.handleKey(homeKey())
	if got := nonArtifactsAbove(m); got != 0 {
		t.Fatalf("end then home left %d rows above the cursor", got)
	}
	if rail := ansi.Strip(railLinesText(m.railLines(80, m.listBodyHeight()))); rail == "" {
		t.Fatal("home drew an empty rail")
	}
}

// rowLabel is the text a row is drawn under: a group wears its own name
// rather than its path, and root is drawn as the word.
func rowLabel(m *Model, row treeRow) string {
	if !row.isGroup {
		return m.displayName(row.sess)
	}
	if row.isRoot() {
		return displayGroup(row.group)
	}
	return baseName(row.group)
}

// The jump is the step's neighbour: pressing it twice, or pressing it where
// it already is, is not a move, and an empty list has no end to go to.
func TestJumpToAnEndIsIdempotentAndSafeWhenEmpty(t *testing.T) {
	m := undecidedFleet(t, fleetSize, 120, 40)

	m.handleKey(endKey())
	bottom := m.cursor
	m.handleKey(endKey())
	if m.cursor != bottom {
		t.Fatalf("a second end moved the cursor from %d to %d", bottom, m.cursor)
	}

	m.rows = nil
	m.cursor = 0
	if cmd := m.jumpCursor(1); cmd != nil {
		t.Fatal("an empty list scheduled a preview")
	}
	if m.cursor != 0 {
		t.Fatalf("an empty list moved the cursor to %d", m.cursor)
	}
}

// A tree whose last rows are a session's pull requests -- what a search for
// one session leaves behind -- still ends at the session. A step never lands
// on an artifact, and the jump has the same rule.
func TestEndStopsAboveTheWorkThatEndsTheTree(t *testing.T) {
	m := undecidedFleet(t, fleetSize, 120, 40)
	sess := firstWorkingSession(t, m)
	m.search = sess.Name
	m.rebuildRows()
	m.cursor = sessionRowIndex(t, m, sess.ID)
	m.rebuildRows()
	if last := m.rows[len(m.rows)-1]; !last.isArtifact() {
		t.Fatalf("the search left %+v at the foot of the tree, not work", last)
	}

	m.handleKey(endKey())
	row, ok := m.cursorRow()
	if !ok || row.isArtifact() {
		t.Fatalf("end landed on %+v, want the session its work hangs off", row)
	}
	if !row.isSession() || row.sess.ID != sess.ID {
		t.Fatalf("end landed on %+v, want session %q", row, sess.ID)
	}
}

// An artifact row refuses the session keys, but not the navigation ones: a
// cursor parked on a pull request has to be able to leave.
func TestAnArtifactRowAnswersTheJumpKeys(t *testing.T) {
	m := undecidedFleet(t, fleetSize, 120, 40)
	// Work opens under the cursor, so the artifact rows exist only once the
	// cursor is on the session they hang off.
	sess := firstWorkingSession(t, m)
	m.cursor = sessionRowIndex(t, m, sess.ID)
	m.rebuildRows()
	index := -1
	for i, row := range m.rows {
		if row.isArtifact() {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("fixture listed no artifact rows")
	}
	m.cursor = index
	m.errBar.text = ""

	m.handleKey(endKey())
	if m.errBar.text != "" {
		t.Fatalf("end on an artifact row was refused: %q", m.errBar.text)
	}
	if got := nonArtifactsBelow(m); got != 0 {
		t.Fatalf("end from an artifact left %d rows below the cursor", got)
	}
}
