package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
)

// Each docked rail panel heads itself with a fold row naming its key; folding
// leaves only that row and survives a restart.
func TestRailPanelsFoldToTheirHeaderRows(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 60
	createSession(t, m, "demo", t.TempDir(), "")
	m.applyCmd(t, nil)

	height := m.listBodyHeight()
	rail := railLinesText(m.railLines(48, height))
	for _, want := range []string{"▾ details", "▾ system", "computer", "started",
		m.cap(keymap.ContextList, keymap.FoldRailSystem)} {
		if !strings.Contains(rail, want) {
			t.Fatalf("unfolded rail lacks %q:\n%s", want, rail)
		}
	}

	m.toggleRailFold(railFoldDetails)
	m.toggleRailFold(railFoldSystem)
	rail = railLinesText(m.railLines(48, height))
	for _, want := range []string{"▸ details", "▸ system"} {
		if !strings.Contains(rail, want) {
			t.Fatalf("folded rail lacks %q:\n%s", want, rail)
		}
	}
	for _, gone := range []string{"computer", "started"} {
		if strings.Contains(rail, gone) {
			t.Fatalf("folded rail still shows %q:\n%s", gone, rail)
		}
	}
	if folds := storedRailFolds(m.store); !folds[railFoldDetails] || !folds[railFoldSystem] {
		t.Fatalf("folds not persisted: %v", folds)
	}
}

// A click on a panel's header row on the painted board folds it.
func TestClickingARailPanelHeaderFoldsIt(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 120, 60
	createSession(t, m, "demo", t.TempDir(), "")
	m.applyCmd(t, nil)

	screen := ansi.Strip(m.viewListFrame())
	for y, line := range strings.Split(screen, "\n") {
		i := strings.Index(line, "▾ system")
		if i < 0 {
			continue
		}
		m.handleMouse(tea.MouseClickMsg{X: ansi.StringWidth(line[:i]), Y: y, Button: tea.MouseLeft})
		if !m.railFolds[railFoldSystem] {
			t.Fatal("clicking the system header did not fold it")
		}
		if strings.Contains(ansi.Strip(m.viewListFrame()), "computer") {
			t.Fatal("the folded system panel still paints its meters")
		}
		return
	}
	t.Fatalf("no system header on the board:\n%s", screen)
}
