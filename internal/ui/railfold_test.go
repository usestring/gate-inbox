package ui

import (
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/keymap"
)

// Each docked rail panel heads itself with a fold row naming its key; folding
// leaves only that row, gives the list the rows back, and survives a restart.
func TestRailPanelsFoldToTheirHeaderRows(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 60
	createSession(t, m, "demo", t.TempDir(), "")
	m.applyCmd(t, nil)

	height := m.listBodyHeight()
	rail := railLinesText(m.railLines(48, height))
	for _, want := range []string{"▾ details", "▾ system", "cpu", "cli"} {
		if !strings.Contains(rail, want) {
			t.Fatalf("unfolded rail lacks %q:\n%s", want, rail)
		}
	}
	if !strings.Contains(rail, m.cap(keymap.ContextList, railFoldAction(railFoldSystem))) {
		t.Fatalf("system header does not name its key:\n%s", rail)
	}

	m.toggleRailFold(railFoldDetails)
	m.toggleRailFold(railFoldSystem)
	rail = railLinesText(m.railLines(48, height))
	for _, want := range []string{"▸ details", "▸ system"} {
		if !strings.Contains(rail, want) {
			t.Fatalf("folded rail lacks %q:\n%s", want, rail)
		}
	}
	for _, gone := range []string{"cpu", "started"} {
		if strings.Contains(rail, gone) {
			t.Fatalf("folded rail still shows %q:\n%s", gone, rail)
		}
	}

	if folds := storedRailFolds(m.store); !folds[railFoldDetails] || !folds[railFoldSystem] {
		t.Fatalf("folds not persisted: %v", folds)
	}

	// A click on a header row unfolds it.
	rows := m.railLines(48, height)
	hit := newRailHitMap(rows, 0, 48)
	for y, section := range hit.folds {
		if section == railFoldSystem {
			m.toggleRailFold(hit.folds[y])
		}
	}
	if m.railFolds[railFoldSystem] {
		t.Fatal("system panel still folded after toggling its header row")
	}
}
