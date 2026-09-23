package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The ring opens on the frame rule the body already spends: its title and its
// left corner ride the first row, and the mirrored pane starts on the row
// directly under it. Drawing the ring's own top edge one row lower read as a
// single thickened hairline and cost the agent a row of its terminal.
func TestFocusRingOpensOnTheFrameRule(t *testing.T) {
	m := fleetModel(t, 6, 120, 24)
	m.mode = modeFocus
	lines := strings.Split(ansi.Strip(m.frame()), "\n")

	top := lines[0]
	if !strings.Contains(top, "focused · ctrl+q back") {
		t.Fatalf("the frame's top rule does not name the mode: %q", top)
	}
	if !strings.Contains(top, "╭") || !strings.HasSuffix(strings.TrimRight(top, " "), "╮") {
		t.Errorf("the top rule does not open the ring: %q", top)
	}
	if strings.Contains(lines[1], "╭") {
		t.Errorf("the ring opens a second time on the row below: %q", lines[1])
	}
}

// The body spends no row on a seam of its own, so the pane tmux is pinned to
// is as tall as the body itself.
func TestFocusedPaneKeepsTheWholeBody(t *testing.T) {
	m := fleetModel(t, 6, 120, 24)
	m.mode = modeFocus
	if got, want := m.previewPaneHeight(), m.listBodyHeight(); got != want {
		t.Errorf("the pane is pinned to %d rows of a %d-row body", got, want)
	}
	m.frame()
	if got, want := m.pane.box.y, m.listChromeRows(); got != want {
		t.Errorf("the mirrored pane starts at row %d, want the first body row %d", got, want)
	}
}

// A content column too narrow for the words keeps the corner and drops the
// title: the ring's right upright runs down from that cell, so truncating it
// away leaves the edge open.
func TestFocusRuleTailKeepsItsCornerWhenNarrow(t *testing.T) {
	m := fleetModel(t, 6, 120, 24)
	tail := m.focusRuleTail(12, true)
	if got := lipgloss.Width(tail); got != 12 {
		t.Errorf("the tail is %d cells wide, want the 12 it was given", got)
	}
	if !strings.HasSuffix(ansi.Strip(tail), "╮") {
		t.Errorf("the tail does not close the ring: %q", ansi.Strip(tail))
	}
}
