// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/status"
)

// Colors are resolved from the active Theme by applyTheme; nothing here
// hardcodes a palette. See theme.go for the token meanings.
var (
	colorBg color.Color
	// colorBackdrop is the backdrop the frame sits on, which is the
	// terminal's own while the backdrop is being inherited. colorBg stays
	// the theme's, because its other job is ink on an accent fill.
	colorBackdrop color.Color
	colorSurface  color.Color
	colorOverlay  color.Color
	colorBorder   color.Color
	colorBright   color.Color
	colorText     color.Color
	colorDim      color.Color
	colorSubtle   color.Color
	colorAccent   color.Color
	colorAccent2  color.Color
	colorSelBg    color.Color

	colorWorking  color.Color
	colorWaiting  color.Color
	colorFinished color.Color
	colorErrored  color.Color
	colorIdle     color.Color
)

var (
	sectionStyle     fastStyle
	selectedRowStyle fastStyle

	mutedStyle  fastStyle
	subtleStyle fastStyle
	valueStyle  fastStyle
	labelStyle  fastStyle
	errStyle    fastStyle
	doneStyle   fastStyle
	keyStyle    fastStyle

	// The key map's own selection: the row the rebind cursor is on. It is a
	// band rather than a tint because the card has no gutter to put a marker
	// in, and a key that is merely a brighter accent is indistinguishable
	// from the accent every other key already renders in.
	selectedKeyStyle  fastStyle
	selectedTextStyle fastStyle

	annotationStyle fastStyle
	scopeBadgeStyle fastStyle
	inboxBadgeStyle fastStyle
	focusEdgeStyle  fastStyle
	accentStyle     fastStyle

	chipStyle             fastStyle
	imageChipStyle        fastStyle
	imageChipPastingStyle fastStyle

	legendTitleStyle fastStyle
	legendBadgeStyle fastStyle
	legendLabelStyle fastStyle

	// The four tints a rail row picks between per entry. Named here so they
	// resolve once with the theme rather than once per row per frame.
	selectedNameStyle  fastStyle
	selectedLabelStyle fastStyle
	groupNameStyle     fastStyle
	runTitleStyle      fastStyle
	rootGroupNameStyle fastStyle
)

func init() { applyTheme(themes[themeIndex(defaultTheme)]) }

// rebuildStyles re-derives every style from the colors applyTheme just set.
func rebuildStyles() {
	sectionStyle = newFastStyle(lipgloss.NewStyle().Bold(true).Foreground(colorAccent))

	selectedRowStyle = newFastStyle(lipgloss.NewStyle().Background(colorSelBg).Foreground(colorBright))

	mutedStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorDim))
	subtleStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorSubtle))
	valueStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorText))
	labelStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorSubtle))
	errStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorErrored).Bold(true))
	doneStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorFinished))
	keyStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorAccent).Bold(true))
	selectedKeyStyle = newFastStyle(lipgloss.NewStyle().Background(colorSelBg).Foreground(colorBright).Bold(true))
	selectedTextStyle = newFastStyle(lipgloss.NewStyle().Background(colorSelBg).Foreground(colorBright))

	annotationStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorAccent).Bold(true))
	scopeBadgeStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorBg).Background(colorAccent2).Bold(true).Padding(0, 1))
	// Foreground only: a fill would punch a chip through the band a selected
	// row paints behind it.
	inboxBadgeStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorAccent).Bold(true))
	focusEdgeStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorAccent))
	accentStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorAccent))

	chipStyle = newFastStyle(lipgloss.NewStyle().Background(colorSurface).Padding(0, 1))
	imageChipStyle = newFastStyle(lipgloss.NewStyle().
		Foreground(colorBright).
		Background(colorSurface))
	imageChipPastingStyle = newFastStyle(lipgloss.NewStyle().
		Foreground(colorWorking).
		Background(colorSurface))

	legendTitleStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorSubtle).Bold(true))
	legendBadgeStyle = newFastStyle(lipgloss.NewStyle().
		Foreground(colorBg).Background(colorAccent).Bold(true).Padding(0, 1))
	legendLabelStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorText))

	selectedNameStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorBright).Bold(true))
	selectedLabelStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorBright))
	groupNameStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorAccent2).Bold(true))
	// A run is titled by its goal, so it reads like a heading rather than
	// like the session names under it.
	runTitleStyle = newFastStyle(lipgloss.NewStyle().Foreground(colorBright).Bold(true))
	rootGroupNameStyle = newFastStyle(lipgloss.NewStyle().
		Foreground(lipgloss.Color(mix(current.Accent2, current.Subtle, 0.5))).Bold(true))
}

// renderSelectedRow wraps a pre-styled line with the selected row's
// background and foreground. Internal SGR resets emitted by per-segment
// lipgloss.Render calls (and padRight) would otherwise clear the outer
// background after the first segment, leaving only the bar glyph tinted.
// Re-applying the selected bg+fg after every reset keeps the row tinted
// end-to-end.
func renderSelectedRow(s string) string {
	reapply := "\x1b[0m" + bgSeq(current.Surface) + fgSeq(current.Bright)
	return selectedRowStyle.Render(strings.ReplaceAll(s, "\x1b[0m", reapply))
}

func statusColor(s string) color.Color {
	switch s {
	case status.Working, status.Starting:
		return colorWorking
	case status.Waiting:
		return colorWaiting
	case status.Finished:
		return colorFinished
	case status.Errored, status.Dead:
		return colorErrored
	default:
		return colorIdle
	}
}

// statusLabel is the human text for a status; most match the raw value, but
// the transient launch state reads better spelled out.
func statusLabel(s string) string {
	if s == status.Starting {
		return "starting up"
	}
	return s
}

// padRight pads or clips a possibly-styled string to an exact display width.
func padRight(s string, width int) string {
	w := textfmt.Width(s)
	if w > width {
		s = textfmt.TruncateWidth(s, width, "…")
		w = textfmt.Width(s)
	}
	if w < width {
		s += spaces(width - w)
	}
	if strings.ContainsRune(s, 0x1b) {
		s += "\x1b[0m"
	}
	return s
}

// gaugeGlyph is the meter's bar unit: a heavy rule reads as a slim
// continuous line rather than a row of stacked blocks.
const (
	gaugeGlyph = "━"
	gaugeHalf  = "─"
)

// gauge renders a meter for a 0-100 percentage: a colored run over a muted
// track, the color ramping from calm to alarming as it fills, with a
// half-width cap so small changes still move the bar.
func gauge(percent float64, width int) string {
	if width < 1 {
		width = 1
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	units := percent / 100 * float64(width)
	filled := int(units)
	if filled > width {
		filled = width
	}
	half := units-float64(filled) >= 0.5

	ramp := lipgloss.Color(gaugeRamp(percent))
	bar := lipgloss.NewStyle().Foreground(ramp).Render(strings.Repeat(gaugeGlyph, filled))
	rest := width - filled
	if half && rest > 0 {
		bar += lipgloss.NewStyle().Foreground(ramp).Render(gaugeHalf)
		rest--
	}
	track := lipgloss.NewStyle().Foreground(colorOverlay).Render(strings.Repeat(gaugeGlyph, rest))
	return bar + track
}

// gaugeRamp blends a meter from the theme's calm color to its alarm color
// as the load climbs. The anchors are deliberately "finished" and "errored"
// rather than any state color: a machine meter is a temperature, and a
// theme whose working color is blue would otherwise paint a cold gauge.
func gaugeRamp(percent float64) string {
	if percent >= 85 {
		return current.Errored
	}
	return mix(current.Finished, current.Errored, percent/85)
}

// pill renders a small label chip: tinted text on the surface fill, so a
// row of them reads as tokens rather than as more prose.
func pill(text string, fg color.Color) string {
	return chipStyle.Foreground(fg).Render(text)
}

// inboxBadge marks a session another agent has messages waiting for, in
// the manager's own accent so it cannot be read as a state the agent is in.
func inboxBadge(count int) string {
	return inboxBadgeStyle.Render("»" + strconv.Itoa(count))
}

// keyPill renders a chip with the key that changes it dimmed in front, so
// the header doubles as a key legend: each changeable value wears its
// shortcut. The key is dim enough to lose to the value at a glance but
// reads clearly when hunted.
func keyPill(key, text string, fg color.Color) string {
	return subtleStyle.Render(key+" ") + pill(text, fg)
}

// keyCap renders one binding: the key in accent, its action beside it. The
// key stays plain text; the legend's badge carries the visual weight, so a
// row of bindings reads as prose under a header rather than as buttons.
func keyCap(key, label string) string {
	cache := capKey{key, label}
	if out, ok := keyCapCache[cache]; ok {
		return out
	}
	out := keyStyle.Render(key) + " " + legendLabelStyle.Render(label)
	keyCapCache[cache] = out
	return out
}

// keyCapQuiet is keyCap for a secondary tier: the label drops to the dim
// tone so the tier recedes behind the one above it.
func keyCapQuiet(key, label string) string {
	cache := capKey{key, label}
	if out, ok := keyCapQuietCache[cache]; ok {
		return out
	}
	out := keyStyle.Render(key) + " " + mutedStyle.Render(label)
	keyCapQuietCache[cache] = out
	return out
}

// imageChip tints a pasted-image token. Color only: the token's own
// characters are what the textarea measured when it wrapped the line.
func imageChip(token string) string {
	return imageChipStyle.Render(token)
}

// imageChipPasting tints a chip whose clipboard read is still running.
func imageChipPasting(token string) string {
	return imageChipPastingStyle.Render(token)
}
