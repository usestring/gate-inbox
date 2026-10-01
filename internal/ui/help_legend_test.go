// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/status"
)

// The key map carries bindings; the glyphs live one key off it.
func TestKeysViewCarriesNoLegend(t *testing.T) {
	for _, section := range helpModel().resolvedHelp() {
		if strings.HasPrefix(section.title, "legend:") {
			t.Errorf("the key map still carries %q", section.title)
		}
		for _, row := range section.rows {
			for _, glyph := range []string{"◐ working", "◆ waiting", "● finished", "○ idle", "✕ errored", "◌ starting", "■ ◧ ◰", "▢ □", "▣", "▲ ◭ △"} {
				if row.key == glyph {
					t.Errorf("the key map still lists the legend row %q", glyph)
				}
			}
		}
	}
	frame := ansi.Strip(helpModel().frame())
	for _, glyph := range []string{"◐ working", "■ ◧ ◰", "▲ ◭ △"} {
		if strings.Contains(frame, glyph) {
			t.Errorf("the key map screen still shows %q", glyph)
		}
	}
}

// The legend is one key off the key map and back with the same key.
func TestLegendOpensFromKeysWithL(t *testing.T) {
	m := helpModel()
	m.handleHelpKey(runeKey("l"))
	if !m.help.legend {
		t.Fatal("l did not open the legend")
	}
	if m.mode != modeHelp {
		t.Fatalf("l left mode %v", m.mode)
	}
	stripped := ansi.Strip(m.frame())
	for _, want := range []string{"Legend", "◐ working", "◆ waiting", "■ ◧ ◰", "▲ ◭ △"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("the legend does not show %q:\n%s", want, stripped)
		}
	}
	for _, gone := range []string{"quit (sessions keep running)", "rebind"} {
		if strings.Contains(stripped, gone) {
			t.Fatalf("the legend still shows the key map's %q", gone)
		}
	}
	m.handleHelpKey(runeKey("l"))
	if m.help.legend {
		t.Fatal("l did not walk back to the key map")
	}
	if stripped := ansi.Strip(m.frame()); !strings.Contains(stripped, "Keys") {
		t.Fatalf("walking back did not return to the key map:\n%s", stripped)
	}
}

// The legend paints glyphs the way the board does: session marks in their
// status tints under a secondary-accent rule, where the key map paints
// every key in the primary accent under a primary-accent rule.
func TestLegendUsesItsOwnColors(t *testing.T) {
	keys := helpModel()
	inner := cardInnerWidth(helpCardWidth(keys.width))
	keysLines, _ := keys.helpBodyLines(matchHelp(keys.helpCatalogSections(), ""), inner, "")
	keysBody := strings.Join(keysLines, "\n")
	legend := helpModel()
	legend.help.legend = true
	legendLines, _ := legend.legendBodyLines(matchHelp(legend.helpCatalogSections(), ""), inner, "")
	legendBody := strings.Join(legendLines, "\n")

	mark := statusTint(status.Working, "◐ working")
	if !strings.Contains(legendBody, mark) {
		t.Fatal("the legend does not tint the working mark in its status color")
	}
	if strings.Contains(keysBody, mark) {
		t.Fatal("the key map tints a mark it no longer lists")
	}
	rule := legendDivider("legend: the mark on a session row", inner)
	if !strings.Contains(legendBody, rule) {
		t.Fatal("the legend does not rule its sections in its own color")
	}
	if strings.Contains(keysBody, rule) {
		t.Fatal("the key map rules a section in the legend's color")
	}
}

// The legend binds nothing: it scrolls where the key map selects, and the
// rebind keys stay quiet there.
func TestLegendScrollsWithoutACursor(t *testing.T) {
	m := helpModel()
	m.help.legend = true
	if rows := m.helpBindings(); len(rows) != 0 {
		t.Fatalf("the legend walks %d bindings", len(rows))
	}
	m.handleHelpKey(runeKey("enter"))
	if m.help.capturing {
		t.Fatal("enter armed a rebind on the legend")
	}
	m.handleHelpKey(runeKey("r"))
	if len(m.help.notes) != 0 {
		t.Fatalf("r left notes on the legend: %q", m.help.notes)
	}
	// The legend is two short sections: a tall terminal fits it whole, so
	// scroll on a short one, where it overflows like the key map does.
	m.height = 14
	if limit := m.helpScrollLimit(); limit == 0 {
		t.Fatal("the legend should overflow a short terminal")
	}
	m.handleHelpKey(runeKey("j"))
	if m.help.scroll == 0 {
		t.Fatal("j did not scroll the legend")
	}
	m.handleHelpKey(runeKey("k"))
	m.handleHelpKey(runeKey("k"))
	if m.help.scroll != 0 {
		t.Fatalf("k left the legend scrolled at %d", m.help.scroll)
	}
}

// Search narrows the legend the way it narrows the key map, counting what
// it kept in the legend's own words.
func TestLegendSearchNarrowsSymbols(t *testing.T) {
	m := helpModel()
	m.help.legend = true
	m.help.query = "waiting"
	sections := matchHelp(m.helpCatalogSections(), m.help.query)
	if n := helpRowCount(sections); n == 0 {
		t.Fatal("waiting matches nothing in the legend")
	}
	if line := m.helpSearchLine(sections); !strings.Contains(line, "symbol") {
		t.Fatalf("the legend counts its hits as %q", line)
	}
	m.help.query = "zzzz"
	if frame := ansi.Strip(m.frame()); !strings.Contains(frame, "no symbol matches that") {
		t.Fatalf("an empty legend search says:\n%s", frame)
	}
}
