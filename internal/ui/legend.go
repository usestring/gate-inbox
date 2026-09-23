// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"strconv"
	"strings"
)

// A legend is the app's key map made visible: tiers of bindings, each tier
// named for what its keys act on, so the footer answers "what can I do to
// the thing under the cursor" before it answers "what keys exist".
const (
	// legendTitleColumn keeps every tier's first binding on one column, so
	// stacked tiers read as a table rather than as ragged prose.
	legendTitleColumn = 11
	legendGap         = 2
	// legendMaxRows is the footer's height budget. Past it the tail is cut
	// and marked; the full map is one ? away.
	legendMaxRows = 3
)

// legendSection is one tier: its title, its bindings, and whether it is a
// secondary tier that recedes behind the tier above it.
type legendSection struct {
	title string
	pairs [][2]string
	quiet bool
}

// legendSlots is the footer's memo. A frame draws the bar twice -- once for
// what is on screen and once for the list footer's height, which is what a
// transient tier is padded to -- and the answer changes only when the cursor
// moves onto a different kind of row, a toggle flips or the window resizes.
// Four slots hold both of a frame's calls with room for the tier either side
// of a step, and the whole thing is bounded by construction.
var legendSlots [4]struct {
	key, value string
	set        bool
}

var legendSlot int

// legendKey is the tiers themselves rather than the state behind them: a tier
// that grows a binding keys itself, with no list of inputs to keep in step.
func legendKey(sections []legendSection, width int) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(width))
	b.WriteByte('/')
	b.WriteString(strconv.Itoa(renderGen))
	for _, section := range sections {
		b.WriteByte('\x00')
		b.WriteString(section.title)
		if section.quiet {
			b.WriteByte('~')
		}
		for _, pair := range section.pairs {
			b.WriteByte('\x01')
			b.WriteString(pair[0])
			b.WriteByte('\x02')
			b.WriteString(pair[1])
		}
	}
	return b.String()
}

func legendBar(sections []legendSection, width, maxRows int) string {
	if maxRows <= 0 {
		return ""
	}
	key := legendKey(sections, width) + "/" + strconv.Itoa(maxRows)
	for _, slot := range legendSlots {
		if slot.set && slot.key == key {
			return slot.value
		}
	}
	out := buildLegendBar(sections, width, maxRows)
	legendSlots[legendSlot] = struct {
		key, value string
		set        bool
	}{key: key, value: out, set: true}
	legendSlot = (legendSlot + 1) % len(legendSlots)
	return out
}

// buildLegendBar renders a legend as the app's footer, one tier per line where
// the terminal allows it and the tail marked when it does not. maxRows is the
// height budget: legendMaxRows on a terminal with room, one on a short one.
func buildLegendBar(sections []legendSection, width, maxRows int) string {
	indent := spaces(railGutter)
	cont := indent + spaces(legendTitleColumn)
	sep := subtleStyle.Render(" · ")
	more := subtleStyle.Render("…")

	var out []string
	for _, section := range sections {
		if len(section.pairs) == 0 || len(out) >= maxRows {
			continue
		}
		title := legendBadgeStyle.Render(section.title)
		if section.quiet {
			title = legendTitleStyle.Render(section.title)
		}
		head := indent + padRight(title, legendTitleColumn)
		line, lineWidth, started := head, cellWidth(head), false
		cut := false
		for _, pair := range section.pairs {
			part, gap := keyCap(pair[0], pair[1]), spaces(legendGap)
			if section.quiet {
				part, gap = keyCapQuiet(pair[0], pair[1]), sep
			}
			partWidth := cellWidth(part) + cellWidth(gap)
			// The row that cannot wrap further keeps room for the cut
			// marker, so the marker never lands past the terminal edge.
			avail := width
			if len(out) >= maxRows-1 {
				avail = width - 1 - cellWidth(more)
			}
			switch {
			case !started:
				line, lineWidth, started = line+part, lineWidth+cellWidth(part), true
			case lineWidth+partWidth <= avail:
				line += gap + part
				lineWidth += partWidth
			case len(out) < maxRows-1:
				out = append(out, line)
				line, lineWidth = cont+part, cellWidth(cont)+cellWidth(part)
			default:
				cut = true
			}
			if cut {
				break
			}
		}
		if cut {
			line += " " + more
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// legendInline renders one tier as a single untitled run, for the foot of a
// modal card where the tier's subject is the card's own title.
func legendInline(pairs [][2]string, width int) string {
	gap := spaces(legendGap)
	var lines []string
	var parts []string
	lineWidth := 0
	for _, pair := range pairs {
		part := keyCap(pair[0], pair[1])
		partWidth := cellWidth(part)
		if len(parts) > 0 && lineWidth+legendGap+partWidth > width {
			lines = append(lines, strings.Join(parts, gap))
			parts, lineWidth = nil, 0
		}
		if len(parts) > 0 {
			lineWidth += legendGap
		}
		parts = append(parts, part)
		lineWidth += partWidth
	}
	lines = append(lines, strings.Join(parts, gap))
	return strings.Join(lines, "\n")
}
