package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// railDock is where the rail's machine dock starts: dockRule is the LAST
// rule on the rail, the one that opens the dock itself. The selected row's
// own detail block, and the prompt beside it, open with an earlier rule of
// their own, so firstRule is where the list area ends, and lastEntry the
// last row the entry list painted above it.
func railDock(t *testing.T, m *Model) (lastEntry, firstRule, dockRule, height int) {
	t.Helper()
	rows := m.railLines(59, m.listBodyHeight())
	lastEntry, firstRule, dockRule = -1, -1, -1
	for i, row := range rows {
		if row.rule {
			dockRule = i
			if firstRule < 0 {
				firstRule = i
			}
			continue
		}
		if firstRule < 0 && strings.TrimSpace(ansi.Strip(row.text)) != "" {
			lastEntry = i
		}
	}
	if dockRule < 0 {
		t.Fatalf("the rail has no machine dock:\n%s", railLinesText(rows))
	}
	return lastEntry, firstRule, dockRule, len(rows)
}

// dockRows is how many rows the dock takes under its rule: the meters, with
// the blank line that closes them.
func dockRows(m *Model) int { return len(m.computerLines(59)) }

// A hundred-row rail holding thirty sessions has rows to spare, and the
// machine dock is pinned at the rail's foot regardless: the bottom-left
// corner is where the machine reading lives, on every terminal.
func TestATallRailPinsTheDockAtTheFoot(t *testing.T) {
	m := fleetModel(t, 30, 200, 100)
	lastEntry, _, dockRule, height := railDock(t, m)
	if height != m.listBodyHeight() {
		t.Fatalf("the rail is %d rows, want the body's %d", height, m.listBodyHeight())
	}
	if dockRule != height-dockRows(m)-1 {
		t.Fatalf("the dock opens at row %d of %d, want it pinned at the foot", dockRule, height)
	}
	if lastEntry >= dockRule-1 {
		t.Fatalf("the list ran into the dock at row %d", lastEntry)
	}
}

// A rail the list fills leaves no blank padding before the selected row's
// own detail block, and the dock stays pinned at the foot under it.
func TestAFullRailKeepsTheDockAtTheFoot(t *testing.T) {
	m := fleetModel(t, 87, 200, 50)
	m.firstPrompts, m.sessions[0].LaunchPrompt = nil, ""
	for i := range m.rows {
		m.rows[i].sess.LaunchPrompt = ""
	}
	lastEntry, firstRule, dockRule, height := railDock(t, m)
	if firstRule != lastEntry+1 {
		t.Fatalf("a full rail put %d blank rows between its list and its detail block",
			firstRule-lastEntry-1)
	}
	if dockRule != height-dockRows(m)-1 {
		t.Fatalf("the dock opens at row %d of %d, want it pinned at the foot", dockRule, height)
	}
}
