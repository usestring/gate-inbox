package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// A terminal without an enhanced keyboard protocol sends ctrl+i as tab, so a
// TRIAGE badge naming ctrl+i as the way out names a key that never arrives.
func TestTheTriageBadgeNamesAKeyTheTerminalCanSend(t *testing.T) {
	m := shotModel()
	m.triage = true

	badge := ansi.Strip(strings.Join(m.filterBadgeLines(), "\n"))
	if !strings.Contains(badge, "alt+t") || strings.Contains(badge, "ctrl+i") {
		t.Errorf("legacy terminal badge should name alt+t, not ctrl+i:\n%s", badge)
	}

	m.Update(tea.KeyboardEnhancementsMsg{Flags: ansi.KittyDisambiguateEscapeCodes})
	badge = ansi.Strip(strings.Join(m.filterBadgeLines(), "\n"))
	if !strings.Contains(badge, "ctrl+i") {
		t.Errorf("enhanced terminal badge should keep ctrl+i:\n%s", badge)
	}
}

func TestAltTTogglesTriage(t *testing.T) {
	m := shotModel()
	m.triage = true
	m.handleKey(tea.KeyPressMsg{Code: 't', Mod: tea.ModAlt})
	if m.triage {
		t.Error("alt+t did not turn triage off")
	}
}
