// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Every line of the rail's own detail block has to stay inside the rail: a
// fact that overflows tears the panel beside it.
func TestSessionDetailLinesFitTheirColumn(t *testing.T) {
	for _, width := range []int{20, 32, 46, 70, 120} {
		session := shotModel()
		session.queuedMessages = map[string]int{"add-rate-limiting": 2}

		group := shotModel()
		for i, row := range group.rows {
			if row.isGroup && row.group == "backend" {
				group.cursor = i
			}
		}
		blocks := map[string][]string{
			"session": session.sessionDetailLines(width),
			"group":   group.sessionDetailLines(width),
			"roster":  strings.Split(group.viewGroupAgents("backend", width, 12), "\n"),
		}
		for name, lines := range blocks {
			for i, line := range lines {
				if got := ansi.StringWidth(line); got > width {
					t.Errorf("%s block at %d: line %d is %d wide: %q", name, width, i, got, ansi.Strip(line))
				}
			}
		}

		if !strings.Contains(ansi.Strip(strings.Join(blocks["session"], "\n")), "»2") {
			t.Errorf("block at %d dropped the queued-message badge", width)
		}
	}
}

// The name and state survive even a rail too narrow for the rest of the
// facts; a fact whose value does not fit is cut with an ellipsis rather
// than dropped or left to overflow.
func TestSessionDetailLinesStayReadableWhenNarrow(t *testing.T) {
	m := shotModel()

	wide := ansi.Strip(strings.Join(m.sessionDetailLines(70), "\n"))
	for _, want := range []string{"add-rate-limiting", "claude", "working"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide block is missing %q: %q", want, wide)
		}
	}

	narrow := m.sessionDetailLines(20)
	if len(narrow) == 0 {
		t.Fatal("a 20-column rail should still carry the block")
	}
	for _, line := range narrow {
		if got := ansi.StringWidth(line); got > 20 {
			t.Errorf("narrow line is %d wide, want at most 20: %q", got, ansi.Strip(line))
		}
	}
	plain := ansi.Strip(strings.Join(narrow, "\n"))
	if !strings.Contains(plain, "add-rate") {
		t.Errorf("20 columns lost the name entirely: %q", plain)
	}
	if !strings.Contains(plain, "working") {
		t.Errorf("20 columns lost the state: %q", plain)
	}

	// Too tight for even a label column: the block steps aside instead of
	// painting something that cannot fit.
	if lines := m.sessionDetailLines(9); lines != nil {
		t.Errorf("a 9-column rail should drop the block, got %v", lines)
	}
}

// A group block's own status breakdown reads at rail widths too tight for
// the session facts beside it, cut rather than dropped.
func TestGroupDetailLinesCarryTheStateBreakdown(t *testing.T) {
	m := shotModel()
	for i, row := range m.rows {
		if row.isGroup && row.group == "backend" {
			m.cursor = i
		}
	}
	wide := ansi.Strip(strings.Join(m.sessionDetailLines(70), "\n"))
	if !strings.Contains(wide, "working") {
		t.Errorf("wide group block lost the state breakdown: %q", wide)
	}
	narrow := ansi.Strip(strings.Join(m.sessionDetailLines(24), "\n"))
	if !strings.Contains(narrow, "working") {
		t.Errorf("narrow group block lost the state breakdown: %q", narrow)
	}
}

// A roster is a table: every tool starts on one column and every state ends
// on one edge, whatever the names around them do.
func TestGroupRosterColumnsAlign(t *testing.T) {
	m := shotModel()
	rows := strings.Split(ansi.Strip(m.viewGroupAgents("backend", 76, 12)), "\n")
	if len(rows) < 3 {
		t.Fatalf("roster is only %d rows: %q", len(rows), rows)
	}
	column := -1
	for _, row := range rows[1:] {
		at := strings.Index(row, "claude")
		if at < 0 {
			at = strings.Index(row, "codex")
		}
		if at < 0 {
			t.Fatalf("no tool on roster row %q", row)
		}
		// Byte offsets shift with every multi-byte glyph in a name, so the
		// column is the display width of what precedes the tool.
		cell := ansi.StringWidth(row[:at])
		if column == -1 {
			column = cell
		} else if cell != column {
			t.Errorf("tool column moved from %d to %d: %q", column, cell, row)
		}
		if got := ansi.StringWidth(row); got != 76 {
			t.Errorf("roster row is %d wide, want the full 76: %q", got, row)
		}
	}
}
