package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/keymap"
)

func TestDefaultLegendIsTheSessionRowAndThePeekKey(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 100, 40
	createSession(t, m, "legend", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())

	footer := ansi.Strip(m.viewFooter())
	if got := lipgloss.Height(footer); got != 2 {
		t.Fatalf("default footer takes %d rows, want the row tier and the peek key:\n%s", got, footer)
	}
	for _, want := range []string{"Session", "focus", "prompt", "kill", "? more"} {
		if !strings.Contains(footer, want) {
			t.Errorf("default footer is missing %q:\n%s", want, footer)
		}
	}
	for _, hidden := range []string{"fork", "rename", "restart", "settings", "quit", "editor", "navigate", "triage", "View", "mute"} {
		if strings.Contains(footer, hidden) {
			t.Errorf("default footer still advertises %q:\n%s", hidden, footer)
		}
	}
}

func TestDefaultLegendAlwaysKeepsQuestionMark(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "pinned", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())
	for _, width := range []int{40, 60, 80, 100} {
		m.width, m.height = width, 40
		footer := ansi.Strip(m.viewFooter())
		if !strings.Contains(footer, "? more") {
			t.Errorf("%d-column footer dropped the peek key:\n%s", width, footer)
		}
	}
}

func TestListFooterHeightHoldsAcrossRowsAtANarrowWidth(t *testing.T) {
	m := fleetModel(t, 87, 40, 50)
	for i := range m.rows {
		m.placeCursor(i)
		footer := ansi.Strip(m.viewFooter())
		if got := lipgloss.Height(footer); got != 2 {
			t.Fatalf("row %d footer takes %d rows, want the row tier and the peek key:\n%s", i, got, footer)
		}
	}
}

func TestTapLegendPeekExpiresWithoutReleaseEvents(t *testing.T) {
	m := buildModel(t)
	_, cmd := m.handleKey(runeKey("?"))
	if cmd == nil || !m.legendPeek.visible {
		t.Fatal("? did not show the legend and arm its tap window")
	}
	m.settleLegendPeek(legendPeekDecayMsg{seq: m.legendPeek.seq})
	if m.legendPeek.visible {
		t.Fatal("a tap left the legend open")
	}
}

func TestLegendPeekLastsUntilOpeningKeyRelease(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: '/', Mod: tea.ModShift, Text: "?"},
		{Code: 'p', Mod: tea.ModCtrl},
	} {
		t.Run(key.String(), func(t *testing.T) {
			m := buildModel(t)
			if key.Code == 'p' {
				m.rebind(keymap.ContextList, keymap.LegendPeek, []string{"ctrl+p"})
			}
			m.Update(tea.KeyboardEnhancementsMsg{Flags: ansi.KittyReportEventTypes | ansi.KittyReportAllKeysAsEscapeCodes})
			enhancements := m.View().KeyboardEnhancements
			if !enhancements.ReportEventTypes || !enhancements.ReportAllKeysAsEscapeCodes || !enhancements.ReportAssociatedText {
				t.Fatal("view did not request release events")
			}
			_, cmd := m.Update(key)
			if !m.legendPeek.visible || cmd != nil {
				t.Fatal("press did not open the legend without a decay timer")
			}
			m.Update(legendPeekDecayMsg{seq: m.legendPeek.seq})
			key.IsRepeat = true
			m.Update(key)
			m.Update(tea.KeyReleaseMsg{Code: 'z'})
			if !m.legendPeek.visible {
				t.Fatal("timer, repeat, or unrelated release closed the held legend")
			}
			m.Update(tea.KeyReleaseMsg{Code: key.Code})
			if m.legendPeek.visible {
				t.Fatal("opening key release left the legend open after modifiers were released")
			}
		})
	}
}

func TestLegendPeekDismissesOnBlur(t *testing.T) {
	m := buildModel(t)
	m.Update(tea.KeyboardEnhancementsMsg{Flags: ansi.KittyReportEventTypes | ansi.KittyReportAllKeysAsEscapeCodes})
	m.Update(runeKey("?"))
	m.Update(tea.BlurMsg{})
	if m.legendPeek.visible {
		t.Fatal("blur left the legend waiting for a release it may never receive")
	}
}

func TestRepeatedQuestionMarkCollapsesAfterReleaseWindow(t *testing.T) {
	m := buildModel(t)
	m.handleKey(runeKey("?"))
	first := m.legendPeek.seq
	m.handleKey(runeKey("?"))
	last := m.legendPeek.seq
	if last == first {
		t.Fatal("a repeated ? was not recognized as a hold")
	}
	m.settleLegendPeek(legendPeekDecayMsg{seq: first})
	if !m.legendPeek.visible {
		t.Fatal("the superseded tap timer closed a held peek")
	}
	m.settleLegendPeek(legendPeekDecayMsg{seq: last})
	if m.legendPeek.visible {
		t.Fatal("the held peek stayed open after repeats stopped")
	}
}

func TestLegendPeekOverlaysWithoutChangingFooterOrFrameHeight(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 100, 40
	createSession(t, m, "overlay", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())
	footerRows := m.footerRows()
	before := m.frame()
	m.handleKey(runeKey("?"))
	after := m.frame()

	if m.footerRows() != footerRows {
		t.Fatalf("peek changed footer height from %d to %d", footerRows, m.footerRows())
	}
	if lipgloss.Height(after) != lipgloss.Height(before) {
		t.Fatalf("peek changed frame height from %d to %d", lipgloss.Height(before), lipgloss.Height(after))
	}
	plain := ansi.Strip(after)
	if !strings.Contains(plain, "Available") || !strings.Contains(plain, "h full key map") {
		t.Fatalf("peek did not overlay the full applicable legend:\n%s", plain)
	}
}

func TestHelpMarksUnavailableListActions(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "available", t.TempDir(), "")
	m.applyCmd(t, m.refreshCmd())

	available := map[keymap.Action]bool{}
	for _, section := range m.resolvedHelp() {
		for _, row := range section.rows {
			if row.ctx == keymap.ContextList {
				available[row.action] = row.available
			}
		}
	}
	if available[keymap.Restore] {
		t.Fatal("restore is marked available on an active session")
	}
	if !available[keymap.Restart] {
		t.Fatal("restart is marked unavailable on a live session")
	}
}
