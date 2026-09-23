package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
)

func TestFuzzyLiveSessionsEndToEnd(t *testing.T) {
	m := buildModel(t)
	for _, tool := range []string{"claude", "codex", "opencode"} {
		m.cfg.Tools[tool] = config.Tool{Command: "cat", DefaultStatus: status.Idle}
		if err := m.spawnSession(tool, "gate-inbox-"+tool, t.TempDir(), "", "", false); err != nil {
			t.Fatal(err)
		}
	}
	m.applyCmd(t, m.refreshCmd())
	press := func(key tea.KeyPressMsg) {
		_, cmd := m.handleKey(key)
		m.applyCmd(t, cmd)
	}
	typeText := func(text string) {
		for _, r := range text {
			press(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
	for _, tc := range []struct{ tool, query string }{
		{"claude", "cld"}, {"codex", "cdx"}, {"opencode", "opc"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			typeText("/gtin")
			if len(m.sessionRows()) != 3 {
				t.Fatalf("name abbreviation returned %v", sessionNames(m))
			}
			m.selectSessionRow(t, "gate-inbox-"+tc.tool)
			before, _ := m.selected()
			typeText(" " + tc.query)
			rows := m.sessionRows()
			selected, ok := m.selected()
			if len(rows) != 1 || !ok || selected.ID != before.ID || selected.Tool != tc.tool {
				t.Fatalf("refinement selected %+v from %v, expected %s", selected, sessionNames(m), tc.tool)
			}
			press(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.searching || m.search != "gtin "+tc.query {
				t.Fatalf("Enter did not retain the query: searching=%v query=%q mode=%v", m.searching, m.search, m.mode)
			}
			m.applyCmd(t, m.refreshCmd())
			selected, ok = m.selected()
			if !ok || selected.ID != before.ID {
				t.Fatal("a real poll changed the selected session")
			}
			press(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.mode != modeFocus || m.focusedID != before.ID {
				t.Fatalf("Enter focused mode=%v session=%s, expected %s", m.mode, m.focusedID, before.ID)
			}
			if !strings.Contains(ansi.Strip(m.frame()), tc.tool) {
				t.Fatal("focused frame does not identify the requested tool")
			}
			m.leaveFocusForFixture(t)
			press(tea.KeyPressMsg{Code: tea.KeyEsc})
			if m.search != "" || len(m.sessionRows()) != 3 {
				t.Fatalf("Escape did not restore all sessions: %v", sessionNames(m))
			}
		})
	}
	typeText("/zzzznomatch")
	if len(m.sessionRows()) != 0 {
		t.Fatal("non-matching query kept a row")
	}
	press(tea.KeyPressMsg{Code: tea.KeyEsc})
	if len(m.sessionRows()) != 3 {
		t.Fatal("clearing an empty result did not restore the board")
	}
}
