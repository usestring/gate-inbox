package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// The board takes the whole terminal: no preview column at all, on a
// terminal easily wide enough for two.
func TestBoardLayoutGivesTheListTheWholeWidth(t *testing.T) {
	m := fleetModel(t, 20, 120, 40)
	m.layout = layoutBoard
	left, right := m.splitWidths()
	if left != 120 || right != 0 {
		t.Fatalf("board split is %d/%d, want the full 120 and no preview column", left, right)
	}
}

// auto is unchanged on the same terminal: two columns, the preview keeping
// the larger share.
func TestAutoLayoutStillSplits(t *testing.T) {
	m := fleetModel(t, 20, 120, 40)
	m.layout = layoutAuto
	left, right := m.splitWidths()
	if right == 0 {
		t.Fatalf("auto gave up the preview column on a %d-wide terminal", m.width)
	}
	if left+right != 120 {
		t.Errorf("split is %d+%d, which does not cover the terminal", left, right)
	}
}

// Every row of the painted frame runs the full width, so nothing is left
// drawing at the old rail's thirty percent.
func TestBoardLayoutPaintsFullWidthRows(t *testing.T) {
	m := fleetModel(t, 20, 120, 40)
	m.layout = layoutBoard
	frame := m.frame()
	for i, line := range strings.Split(frame, "\n") {
		if got := lipgloss.Width(line); got != 120 {
			t.Fatalf("frame line %d is %d cells wide, want 120", i, got)
		}
	}
}

// Focusing still opens the pane -- the board drops the preview column, not
// the pane behind it -- and it opens at the full width rather than at the
// column that is no longer there.
func TestBoardLayoutStillOpensThePaneFullWidth(t *testing.T) {
	m := fleetModel(t, 20, 120, 40)
	m.layout = layoutBoard
	board := m.previewPaneWidth()
	if board < 100 {
		t.Fatalf("panes are sized to %d columns under the board layout, want near the full 120", board)
	}
	m.mode = modeFocus
	if got := m.previewPaneWidth(); got != board {
		t.Errorf("focusing changed the pane width from %d to %d; the board draws one panel either way", board, got)
	}
}

// The setting cycles onto the new mode and back off it, and an unknown
// stored value still reads as auto.
func TestBoardLayoutIsInTheSettingsCycle(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	m.settings.field = settingsFieldLayout
	seen := map[string]bool{}
	for range layoutModes {
		m.applyCmd(t, m.cycleSetting(1))
		seen[m.settings.layout] = true
	}
	if !seen[layoutBoard] {
		t.Errorf("cycling the layout never reached %q; it visited %v", layoutBoard, seen)
	}
	if got := normalizeLayout("panorama"); got != layoutAuto {
		t.Errorf("an unknown layout read as %q, want %q", got, layoutAuto)
	}
	if got := normalizeLayout(layoutBoard); got != layoutBoard {
		t.Errorf("%q did not survive normalisation, got %q", layoutBoard, got)
	}
}

// The board is a choice about columns, not about a small screen: it must not
// pull in the phone tiering that mobile does.
func TestBoardLayoutIsNotTheMobileTier(t *testing.T) {
	m := fleetModel(t, 20, 120, 40)
	m.layout = layoutBoard
	if m.tight() {
		t.Error("the board layout tightened the rail on a full-size terminal")
	}
	if got := m.legendRows(); got != legendMaxRows {
		t.Errorf("the board layout cut the legend to %d rows, want %d", got, legendMaxRows)
	}
}
