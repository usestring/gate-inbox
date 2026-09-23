// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// The list view sits on the terminal's own background, with the sessions
// rail filling its pane flush — header rule to footer rule, window edge to
// seam — and the selected entry lifted once more. Except in OLED mode,
// the backdrop is never painted, so the window padding around the cell
// grid carries the same color as the unpainted cells and the frame meets the window without
// a ring of a different tone. What makes the two agree depends on the
// backdrop mode: inheriting, the tones below are mixed from the terminal's
// own background, so they land a step above whatever it already draws;
// syncing, the terminal is repainted to the theme's backdrop instead.

// backdropHex is the backdrop's fill: none. paint treats it as "pad, but
// leave the terminal's background alone".
func backdropHex() string { return "" }

// panelHex lifts the rail off the backdrop except in OLED mode.
func panelHex() string {
	if current.Name == "oled" {
		return current.Bg
	}
	return themeTone("panel", backdropBase(), current.Surface, 0.55)
}

// blockHex groups content sections with a quieter fill, except in OLED mode.
func blockHex() string {
	if current.Name == "oled" {
		return current.Bg
	}
	return themeTone("block", backdropBase(), current.Surface, 0.35)
}

// selectedHex is the band under a row being renamed.
func selectedHex() string { return current.Surface }

// ruleHex is the hairline tone: lifted just far enough off the backdrop to
// draw a seam without becoming a border.
func ruleHex() string { return themeTone("rule", backdropBase(), current.Text, 0.22) }

// hrule is a horizontal seam across a painted row.
func hrule(width int) string {
	if width < 1 {
		return ""
	}
	if out, ok := hruleCache[width]; ok {
		return out
	}
	out := lipgloss.NewStyle().Foreground(lipgloss.Color(ruleHex())).Render(strings.Repeat("─", width))
	hruleCache[width] = out
	return out
}

// railTopRow opens the body half a cell under the wordmark, the mirror of
// the row that closes it. It drew a third of a cell in sextants until those
// came back as a band of replacement glyphs on the phone and on iPad; the
// frame is held to widely drawn glyphs by TestFrameStaysInWidelyDrawnGlyphs.
func (m *Model) railTopRow(paneWidth, width int) string {
	return m.boundedRuleRow(paneWidth, width, "▀")
}

// topRule opens the body. In focus mode it doubles as the top edge of the
// focused pane's ring — the mirror of focusBottomRule, which already closes
// the ring along the frame's bottom rule. The ring used to open on a row of
// its own directly under this one, which drew as two hairlines a cell apart
// and spent a line of the mirrored agent's terminal on saying twice what one
// row says.
func (m *Model) topRule(paneWidth, width int) string {
	if m.mode != modeFocus {
		return m.railTopRow(paneWidth, width)
	}
	if paneWidth >= width {
		// One pane: the mirrored terminal is the whole width, with no rail
		// to cap and no uprights for the rule to corner into.
		return paint(m.focusRuleTail(width, false), width, backdropHex())
	}
	tail := width - paneWidth - 1
	if tail < 1 || paneWidth < 2 {
		return m.railTopRow(paneWidth, width)
	}
	// The uprights are only drawn once there is a pane box to run down, so
	// without one the rule keeps the frame's own corner rather than opening
	// a ring nothing closes.
	corner := lipgloss.NewStyle().Foreground(colorBackdrop).Render("▜")
	if m.pane.box.ok {
		corner = focusEdgeStyle.Render("╭")
	}
	first := plain(lipgloss.NewStyle().Foreground(lipgloss.Color(panelHex())).Render("▄"), 1)
	interior := lipgloss.NewStyle().Foreground(colorBackdrop).Render(strings.Repeat("▀", paneWidth-1))
	return first + paint(interior, paneWidth-1, panelHex()) + paint(corner, 1, panelHex()) +
		paint(m.focusRuleTail(tail, m.pane.box.ok), tail, backdropHex())
}

// boundedRuleRow draws one of the rows that bound the body — edge "▀" opens
// it, edge "▄" closes it. Over the pane it draws half blocks in the
// pane's own tone — the fill bleeding half a cell past its box — and across
// the content it draws the thin rule, whose center line meets the half
// blocks' edge at the same height.
// The run's interior is drawn inverted — the cell background carries the
// pane tone and the glyph paints the backdrop half in the theme's backdrop
// color — but both end cells are not. The terminal extends a row's edge
// cell background into the window margin at full cell height, so an
// inverted first cell smears pane tone past the pane's corner; drawing it
// as a foreground half block keeps the margin on the terminal's own
// background. The last cell sits over the bleed column, whose fill is only
// half a cell wide, so it takes a quadrant instead of a half block and the
// pane tone stops at the corner instead of jutting past it.
func (m *Model) boundedRuleRow(paneWidth, width int, edge string) string {
	if paneWidth < 2 || paneWidth >= width {
		return paint(hrule(width), width, backdropHex())
	}
	facing, corner := "▄", "▜"
	if edge == "▄" {
		facing, corner = "▀", "▟"
	}
	first := plain(lipgloss.NewStyle().Foreground(lipgloss.Color(panelHex())).Render(facing), 1)
	interior := lipgloss.NewStyle().Foreground(colorBackdrop).
		Render(strings.Repeat(edge, paneWidth-1))
	last := lipgloss.NewStyle().Foreground(colorBackdrop).Render(corner)
	return first + paint(interior, paneWidth-1, panelHex()) + paint(last, 1, panelHex()) +
		paint(hrule(width-paneWidth-1), width-paneWidth-1, backdropHex())
}

// railEdgeCell is one row of the pane's first column, drawn as a foreground
// block in the row's own tone rather than as cell background. The window
// margin beside it inherits the cell's background — the terminal's own —
// so the pane's left edge lands exactly on the cell grid.
func railEdgeCell(fill string) string {
	if out, ok := edgeCellCache[fill]; ok {
		return out
	}
	out := plain(lipgloss.NewStyle().Foreground(lipgloss.Color(fill)).Render("█"), 1)
	edgeCellCache[fill] = out
	return out
}

// vruleColumn is the seam between two painted columns. It doubles as the
// resize grip, taking the accent while the divider is being moved.
func (m *Model) vruleColumn(height int) []string {
	lines := make([]string, height)
	for i := range lines {
		lines[i] = m.seamCell(false)
	}
	return lines
}

// bleedColumn finishes a pane's right edge: a half block in the pane's
// tone on the backdrop, extending the fill half a cell past the seam.
func (m *Model) bleedColumn(height int) []string {
	cell := paint(lipgloss.NewStyle().Foreground(colorBackdrop).Render("▐"), 1, panelHex())
	lines := make([]string, height)
	for i := range lines {
		lines[i] = cell
	}
	// Keep the edge joined to the footer even before a full capture arrives.
	if m.mode == modeFocus && m.pane.box.ok {
		edge := paint(focusEdgeStyle.Render("│"), 1, panelHex())
		corner := paint(focusEdgeStyle.Render("╭"), 1, panelHex())
		top := m.pane.box.y - m.listChromeRows()
		if top-1 >= 0 && top-1 < len(lines) {
			lines[top-1] = corner
		}
		for row := max(0, top); row < len(lines); row++ {
			lines[row] = edge
		}
	}
	return lines
}

func (m *Model) focusRightColumn(height int) []string {
	lines := paintRows(nil, 1, height, backdropHex())
	if m.mode != modeFocus || !m.pane.box.ok {
		return lines
	}
	top := m.pane.box.y - m.listChromeRows()
	for row := max(0, top-1); row < height; row++ {
		glyph := "│"
		if row == top-1 {
			glyph = "╮"
		}
		lines[row] = plain(focusEdgeStyle.Render(glyph), 1)
	}
	return lines
}

// focusBottomRule closes the focused pane's hairline along the frame rule
// under the body: the corner cell is the bleed column the left edge runs
// down, so the two meet exactly.
func (m *Model) focusBottomRule(paneWidth, width int) string {
	tail := width - paneWidth - 1
	if tail < 1 || paneWidth < 2 {
		return m.boundedRuleRow(paneWidth, width, "▄")
	}
	first := plain(lipgloss.NewStyle().Foreground(lipgloss.Color(panelHex())).Render("▀"), 1)
	interior := lipgloss.NewStyle().Foreground(colorBackdrop).Render(strings.Repeat("▄", paneWidth-1))
	corner := focusEdgeStyle.Render("╰")
	return first + paint(interior, paneWidth-1, panelHex()) + paint(corner, 1, panelHex()) +
		paint(focusEdgeStyle.Render(strings.Repeat("─", tail-1)+"╯"), tail, backdropHex())
}

// seamCell is one row of the vertical seam. The column is the pane's own
// fill, so only the pane's rules cross it: a content rule carried across
// reads as the content's separator running into the sessions list.
func (m *Model) seamCell(railRule bool) string {
	// While the divider is being moved the seam becomes the grip.
	if m.split.dragging || m.split.resizeMode {
		color := colorAccent
		if m.split.dragging {
			color = colorAccent2
		}
		return paint(lipgloss.NewStyle().Foreground(color).Render("║"), 1, panelHex())
	}
	if railRule {
		return paint(hrule(1), 1, panelHex())
	}
	return paint("", 1, panelHex())
}

// paint pads a possibly-styled line to an exact width and fills every cell
// with bg. Inner SGR resets emitted by per-segment renders would drop the
// fill partway across, so each reset re-applies it. A fastStyle-cached
// style's suffix is whatever its underlying lipgloss style rendered with a
// probe string, and a foreground-only style renders the bare "\x1b[m" form
// rather than the explicit "\x1b[0m" one -- both mean the same full reset to
// a terminal, so both have to be caught here, or the fill drops right after
// the last fastStyle segment in the row (visible as a stray box around a
// lone label like "session" or "computer", the rest of the row unfilled).
// A captured pane drops back to the default background with "\x1b[49m"
// rather than a reset -- tmux writes each attribute change on its own -- so
// that takes the fill back too, or every unstyled run of agent output shows
// the terminal's colour through the frame.
func paint(s string, width int, bg string) string {
	if bg == "" {
		return plain(s, width)
	}
	fill := bgSeq(bg)
	// A row with no reset in it has nothing to re-fill, and most do not.
	// ReplaceAll would still build the replacement and walk the row.
	if strings.Contains(s, ansiReset) || strings.Contains(s, ansiResetShort) {
		refill := refillSeq(bg)
		s = strings.ReplaceAll(s, ansiReset, refill)
		s = strings.ReplaceAll(s, ansiResetShort, refill)
	}
	if strings.Contains(s, ansiDefaultBg) {
		s = strings.ReplaceAll(s, ansiDefaultBg, fill)
	}
	pad := 0
	if w := cellWidth(s); w > width {
		s = cellTruncate(s, width, "…")
	} else {
		pad = width - w
	}
	return fill + s + spaces(pad) + ansiReset
}

// plain pads a line to width without filling it, leaving the terminal's own
// background showing through. Captured agent output is drawn this way so a
// session's CLI looks exactly as it does inside the session.
func plain(s string, width int) string {
	pad := 0
	if w := cellWidth(s); w > width {
		s = cellTruncate(s, width, "")
	} else {
		pad = width - w
	}
	return ansiReset + s + spaces(pad) + ansiReset
}

// ansiReset is the sequence a per-segment render closes itself with.
const ansiReset = "\x1b[0m"

// ansiResetShort is the bare-parameter form of the same full reset. A
// fastStyle setting only a foreground renders its suffix this way (lipgloss
// omits the redundant "0"), so paint must catch it too, not just ansiReset.
const ansiResetShort = "\x1b[m"

// ansiDefaultBg returns the background alone to the terminal's default.
const ansiDefaultBg = "\x1b[49m"

// spacesRun is the padding every painted row ends in, cut from one string
// rather than built. A row is padded to the column width on every frame, and
// strings.Repeat allocates a fresh run each time for a value that is always
// the same spaces.
const spacesRun = "                                                                " +
	"                                                                " +
	"                                                                " +
	"                                                                "

// spaces is n spaces. Beyond the shared run -- a column wider than 256 cells
// -- it falls back to building them.
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	if n <= len(spacesRun) {
		return spacesRun[:n]
	}
	return strings.Repeat(" ", n)
}

// contentLine is one row of the content column: ours to paint, a seam that
// spans the column edge to edge, or captured output that must keep the
// terminal's own backdrop. Rail rows also carry the tone their fill uses,
// so the edge column beside them can match the selected entry's band.
type contentLine struct {
	text string
	raw  bool
	rule bool
	tone string
}

// paintContent paints a content column, leaving raw rows unfilled.
func paintContent(lines []contentLine, width, height int, bg string) []string {
	out := make([]string, height)
	for i := 0; i < height; i++ {
		if i >= len(lines) {
			out[i] = paint("", width, bg)
			continue
		}
		if lines[i].rule {
			// Seams are drawn at the column's full width, not the inset the
			// text uses, so they meet the frame's edges exactly.
			out[i] = paint(hrule(width), width, bg)
			continue
		}
		if lines[i].raw {
			out[i] = plain(lines[i].text, width)
			continue
		}
		out[i] = paint(lines[i].text, width, bg)
	}
	return out
}

// paintRows paints each line of a block, padding the block itself out to
// height so a short column still fills its side of the frame.
func paintRows(lines []string, width, height int, bg string) []string {
	out := make([]string, height)
	for i := 0; i < height; i++ {
		content := ""
		if i < len(lines) {
			content = lines[i]
		}
		out[i] = paint(content, width, bg)
	}
	return out
}

// joinColumns stitches painted columns row by row.
func joinColumns(columns ...[]string) []string {
	height := 0
	for _, col := range columns {
		if len(col) > height {
			height = len(col)
		}
	}
	rows := make([]string, height)
	for i := 0; i < height; i++ {
		size := 0
		for _, col := range columns {
			if i < len(col) {
				size += len(col[i])
			}
		}
		var b strings.Builder
		b.Grow(size)
		for _, col := range columns {
			if i < len(col) {
				b.WriteString(col[i])
			}
		}
		rows[i] = b.String()
	}
	return rows
}

// indentLines insets a block by n columns.
func indentLines(lines []string, n int) []string {
	pad := spaces(n)
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = pad + line
	}
	return out
}

// splitLines splits a rendered block into lines, treating the empty string
// as no lines rather than one blank line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
