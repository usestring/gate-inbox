package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension/textfmt"
)

// Marks drop the state words and write the three known CLIs as their
// letters; a CLI without letters keeps its name, and every frame line keeps
// its width.
func TestRowDetailMarksReplaceStateAndToolWords(t *testing.T) {
	m := fleetModel(t, 12, 120, 40)
	words := ansi.Strip(m.frame())
	for _, want := range []string{"errored · claude", "waiting · code"} {
		if !strings.Contains(words, want) {
			t.Fatalf("words mode frame lacks %q", want)
		}
	}

	m.rowDetail = rowDetailMarks
	marks := ansi.Strip(m.frame())
	for _, gone := range []string{"errored ·", "waiting ·", "finished ·", "· claude", "· codex", "· opencode"} {
		if strings.Contains(marks, gone) {
			t.Errorf("marks mode frame still says %q", gone)
		}
	}
	for _, want := range []string{"CC", "CX", "OC", "grok"} {
		if !strings.Contains(marks, want) {
			t.Errorf("marks mode frame lacks %q", want)
		}
	}
	for i, line := range splitLines(m.frame()) {
		if got := textfmt.Width(line); got != 120 {
			t.Fatalf("frame line %d is %d cells, want 120", i, got)
		}
	}
}

func TestRowDetailSettingRoundTripsAndFailsSafe(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	m.settings.field = settingsFieldRowDetail
	m.applyCmd(t, m.cycleSetting(1))
	if m.settings.rowDetail != rowDetailMarks {
		t.Fatalf("one step landed on %q, want %q", m.settings.rowDetail, rowDetailMarks)
	}
	if _, cmd := m.saveAndCloseSettings(); cmd != nil {
		m.applyCmd(t, cmd)
	}
	if !m.rowMarks() {
		t.Error("saving did not put the board on marks")
	}
	if got := storedRowDetail(m.store); got != rowDetailMarks {
		t.Fatalf("stored %q, want %q", got, rowDetailMarks)
	}
	if err := m.store.SetSetting(rowDetailSetting, "icons"); err != nil {
		t.Fatal(err)
	}
	if got := storedRowDetail(m.store); got != rowDetailWords {
		t.Errorf("an unknown stored value read as %q, want %q", got, rowDetailWords)
	}
}
