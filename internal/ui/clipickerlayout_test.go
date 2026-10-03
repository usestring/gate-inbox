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

// TestToolFieldKeepsItsShapeAsTheSelectionMoves pins the picker to a fixed
// layout: every CLI in display order, wrapped the same way whichever one is
// selected, with only the brackets moving.
func TestToolFieldKeepsItsShapeAsTheSelectionMoves(t *testing.T) {
	for _, width := range []int{44, 60, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := buildModel(t)
			m.width, m.height = width, 30
			m.openForm()
			m.form.toolNames = []string{"claude", "codex", "opencode", "alternative-cli", "界界界界"}
			m.form.toolFilter.SetValue("")
			m.syncFormFieldWidths()
			var shape []int
			for i, name := range m.form.toolNames {
				m.form.toolIndex = i
				field := ansi.Strip(m.viewToolField())
				lines := strings.Split(field, "\n")
				var widths []int
				for _, line := range lines {
					if lipgloss.Width(line) > m.formValueWidth() {
						t.Fatalf("row exceeds its column budget: %q", line)
					}
					widths = append(widths, lipgloss.Width(line))
				}
				if shape == nil {
					shape = widths
				} else if fmt.Sprint(widths) != fmt.Sprint(shape) {
					t.Fatalf("selecting %q reshaped the field: %v, want %v\n%s", name, widths, shape, field)
				}
				if !strings.Contains(field, "["+name+"]") {
					t.Fatalf("selected %q is not bracketed:\n%s", name, field)
				}
				if strings.Count(field, "[") != 1 {
					t.Fatalf("exactly one CLI should be bracketed:\n%s", field)
				}
				at := -1
				for _, other := range m.form.toolNames {
					next := strings.Index(field, other)
					if next <= at {
						t.Fatalf("%q is out of display order:\n%s", other, field)
					}
					at = next
				}
			}
		})
	}
}

func TestToolFieldDrawsTheSelectionBold(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 80, 30
	m.openForm()
	m.form.toolNames = []string{"claude", "codex"}
	m.form.toolIndex = 1
	m.syncFormFieldWidths()
	if field := m.viewToolField(); !strings.Contains(field, selectedNameStyle.Render("codex")) {
		t.Fatalf("selected CLI is not drawn bold: %q", field)
	}
}

func TestToolFieldKeepsNamesTheFilterRulesOut(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 80, 30
	m.openForm()
	m.form.toolNames = []string{"claude", "codex", "opencode"}
	m.form.toolIndex = 0
	m.syncFormFieldWidths()
	typeIntoForm(m, "op")
	field := ansi.Strip(m.viewToolField())
	for _, name := range m.form.toolNames {
		if !strings.Contains(field, name) {
			t.Fatalf("filter hid %q:\n%s", name, field)
		}
	}
	if !strings.Contains(field, "[opencode]") {
		t.Fatalf("filter should select opencode:\n%s", field)
	}
}

func TestToolFieldLooksTheSameFocusedOrNot(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 80, 30
	m.openForm()
	m.form.toolNames = []string{"claude", "codex", "opencode"}
	m.form.toolIndex = 1
	m.syncFormFieldWidths()
	focused := ansi.Strip(m.viewToolField())
	m.formFocus(1)
	blurred := ansi.Strip(m.viewToolField())
	if !strings.HasPrefix(focused, blurred) {
		t.Fatalf("leaving the field changed the list:\nfocused: %q\nblurred: %q", focused, blurred)
	}
}

func TestToolFilterSharesTheListRowWhenItFits(t *testing.T) {
	m := buildModel(t)
	m.width, m.height = 80, 30
	m.openForm()
	m.form.toolNames = []string{"claude", "codex", "opencode"}
	m.syncFormFieldWidths()
	if field := ansi.Strip(m.viewToolField()); strings.Contains(field, "\n") {
		t.Fatalf("filter should share the list's row: %q", field)
	}
}
