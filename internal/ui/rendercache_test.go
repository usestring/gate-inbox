package ui

import (
	"math/rand"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// themeStyles is every style the theme builds, so a style that grows a
// padding or a width later is compared here rather than found on screen.
func themeStyles() map[string]fastStyle {
	return map[string]fastStyle{
		"section": sectionStyle, "selectedRow": selectedRowStyle,
		"muted": mutedStyle, "subtle": subtleStyle, "value": valueStyle,
		"label": labelStyle, "err": errStyle, "done": doneStyle, "key": keyStyle,
		"annotation": annotationStyle, "scopeBadge": scopeBadgeStyle,
		"inboxBadge": inboxBadgeStyle,
		"focusEdge":  focusEdgeStyle, "accent": accentStyle, "chip": chipStyle,
		"imageChip": imageChipStyle, "imageChipPasting": imageChipPastingStyle,
		"legendTitle": legendTitleStyle, "legendBadge": legendBadgeStyle,
		"legendLabel": legendLabelStyle, "selectedName": selectedNameStyle,
		"selectedLabel": selectedLabelStyle, "groupName": groupNameStyle,
		"rootGroupName": rootGroupNameStyle,
	}
}

// TestFastStyleMatchesLipgloss is the guard on the whole optimisation: a
// fastStyle must answer byte-for-byte what the style it wraps answers, over
// every string the frame hands one.
func TestFastStyleMatchesLipgloss(t *testing.T) {
	for name, style := range themeStyles() {
		for _, s := range widthCorpus {
			if got, want := style.Render(s), style.Style.Render(s); got != want {
				t.Errorf("%s.Render(%q) = %q, lipgloss = %q", name, s, got, want)
			}
		}
	}
}

func TestFastStyleMatchesLipglossRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	styles := themeStyles()
	for i := 0; i < 5000; i++ {
		var b strings.Builder
		for n := rng.Intn(5); n >= 0; n-- {
			b.WriteString(widthCorpus[rng.Intn(len(widthCorpus))])
		}
		s := b.String()
		for name, style := range styles {
			if got, want := style.Render(s), style.Style.Render(s); got != want {
				t.Fatalf("%s.Render(%q) = %q, lipgloss = %q", name, s, got, want)
			}
		}
	}
}

// awkwardStyles are the ones whose answer is not obviously a prefix and a
// suffix. Some of them are anyway -- a padding and a margin are a fixed run of
// cells either side of a line, whatever the line says -- and the probe lets
// those through, which is why the comparison below is over every style rather
// than only the theme's.
func awkwardStyles() map[string]lipgloss.Style {
	return map[string]lipgloss.Style{
		"padding":   lipgloss.NewStyle().Padding(0, 2),
		"width":     lipgloss.NewStyle().Width(6),
		"margin":    lipgloss.NewStyle().Margin(0, 1),
		"border":    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
		"align":     lipgloss.NewStyle().Width(8).AlignHorizontal(lipgloss.Center),
		"maxWidth":  lipgloss.NewStyle().MaxWidth(3),
		"transform": lipgloss.NewStyle().Transform(strings.ToUpper),
		"padColour": lipgloss.NewStyle().Background(lipgloss.Color("#402030")).Padding(1, 3),
	}
}

func TestFastStyleMatchesLipglossAwkward(t *testing.T) {
	for name, style := range awkwardStyles() {
		fast := newFastStyle(style)
		for _, s := range widthCorpus {
			if got, want := fast.Render(s), style.Render(s); got != want {
				t.Errorf("%s.Render(%q) = %q, lipgloss = %q", name, s, got, want)
			}
		}
	}
}

// TestFastStyleRefusesSizingStyles pins the probe: a style that sizes itself
// to its text cannot be a fixed prefix and suffix, and must keep the real
// Render.
func TestFastStyleRefusesSizingStyles(t *testing.T) {
	awkward := awkwardStyles()
	for _, name := range []string{"width", "border", "align", "maxWidth", "transform"} {
		if newFastStyle(awkward[name]).direct {
			t.Errorf("%s took the direct path", name)
		}
	}
}

// TestFastStyleTakesDirectPath is the other half: the colour-only styles the
// theme is made of must actually be getting the fast path, or the whole
// change is inert.
func TestFastStyleTakesDirectPath(t *testing.T) {
	for _, name := range []string{"muted", "subtle", "value", "key", "selectedRow", "selectedName"} {
		if !themeStyles()[name].direct {
			t.Errorf("%s did not take the direct path", name)
		}
	}
}
