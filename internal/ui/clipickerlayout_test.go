package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestAgentBoxKeepsEverySelectionAndFooterVisible(t *testing.T) {
	for _, size := range [][2]int{{44, 8}, {44, 9}, {44, 10}, {44, 12}, {60, 14}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := buildModel(t)
			m.width, m.height = size[0], size[1]
			m.openAgentPick()
			m.agentPick.names = nil
			for i := 0; i < 35; i++ {
				m.agentPick.names = append(m.agentPick.names, fmt.Sprintf("cli-%02d-long", i))
			}
			m.agentPick.names = append(m.agentPick.names, "terminal")
			for i, name := range m.agentPick.names {
				m.setAgentPick(name)
				frame := ansi.Strip(m.viewAgentPick())
				if lipgloss.Height(frame) > m.height {
					t.Fatalf("selection %d makes %d rows for %d-row terminal", i, lipgloss.Height(frame), m.height)
				}
				if strings.Count(frame, name) < 2 {
					t.Fatalf("selected CLI is absent from alternatives:\n%s", frame)
				}
				if !strings.Contains(frame, "cancel") || !strings.Contains(frame, "╯") {
					t.Fatalf("footer or bottom border missing:\n%s", frame)
				}
				for _, line := range strings.Split(frame, "\n") {
					if lipgloss.Width(line) > m.width {
						t.Fatalf("row exceeds width: %q", line)
					}
				}
			}
		})
	}
}

func TestToolFieldMovesOversizedLoneAlternativeBelow(t *testing.T) {
	for _, name := range []string{"alternative-cli", "界界界界界界界", "éééééééééééééé"} {
		t.Run(name, func(t *testing.T) {
			m := buildModel(t)
			m.width, m.height = 44, 30
			m.openForm()
			m.form.toolNames = []string{"a", name}
			m.form.toolIndex = 0
			m.form.toolFilter.SetValue("")
			m.syncFormFieldWidths()
			field := ansi.Strip(m.viewToolField())
			if !strings.Contains(field, "\n") || !strings.Contains(field, name) {
				t.Fatalf("alternative must move to its own line: %q", field)
			}
			for _, line := range strings.Split(field, "\n") {
				if lipgloss.Width(line) > m.formValueWidth() {
					t.Fatalf("value exceeds its column budget: %q", line)
				}
			}
			if !strings.Contains(ansi.Strip(m.viewForm()), name) {
				t.Fatal("the card clips the alternative")
			}
		})
	}
}

func TestAgentBoxFitsAnErrorOnAShortTerminal(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 44, 10
	m.openAgentPick()
	typeInto(t, m, "zzz")
	m.errBar.text = "no CLI matches"
	frame := ansi.Strip(m.viewAgentPick())
	if lipgloss.Height(frame) > m.height || !strings.Contains(frame, "cancel") || !strings.Contains(frame, "no CLI matches") {
		t.Fatalf("error or cancellation hint clipped:\n%s", frame)
	}
}

func TestToolFieldKeepsAFittingAlternativeInline(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 80, 30
	m.openForm()
	m.form.toolNames = []string{"a", "b"}
	m.form.toolIndex = 0
	m.form.toolFilter.SetValue("")
	m.syncFormFieldWidths()
	field := ansi.Strip(m.viewToolField())
	if strings.Contains(field, "\n") || !strings.Contains(field, "b") || lipgloss.Width(field) > m.formValueWidth() {
		t.Fatalf("fitting alternative should stay inline: %q", field)
	}
}
