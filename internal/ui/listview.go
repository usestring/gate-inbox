// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/clipboard"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/launch"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
)

// railGutter is the left inset every rail line shares, and contentGutter
// the content column's. Consistent insets are what make an unbordered
// layout read as columns.
const (
	railGutter    = 2
	contentGutter = 2
	// railInset is the pad inside the rail's own column, one short of
	// railGutter because the edge column already occupies the first cell.
	railInset = railGutter - 1
)

const shellGlyph = "❯"

// viewListFrame is the sessions rail beside the session content, both
// painted surfaces rather than drawn panels.
func (m *Model) viewListFrame() string {
	leftWidth, rightWidth := m.splitWidths()
	footer := m.viewFooter()
	bodyHeight := m.listBodyHeight()

	if rightWidth == 0 {
		return m.viewOnePaneFrame(bodyHeight, footer)
	}

	// The seam between the rail and the content tees into the top rule and
	// runs down to meet the footer's.
	contentWidth := rightWidth - 1

	frame := []string{}
	// The rail's fill runs from the edge column through the seam column,
	// then bleeds half a cell further right, and half a cell above and
	// below its body rows — soft edges drawn with half blocks, the finest
	// step a character cell allows. The first column is drawn as foreground
	// blocks so the window margin beside it keeps the terminal's own
	// background and the fill's corners land exactly on the cell grid.
	bleedWidth := contentWidth - 2
	railWidth := leftWidth - 1
	m.pane.columnX = leftWidth + 2
	railRows := m.railLines(railWidth, bodyHeight)
	contentRows := m.contentLines(bleedWidth, bodyHeight)
	seam := make([]string, bodyHeight)
	edge := make([]string, bodyHeight)
	for i := range seam {
		seam[i] = m.seamCell(i < len(railRows) && railRows[i].rule)
		tone := panelHex()
		if i < len(railRows) && railRows[i].tone != "" {
			tone = railRows[i].tone
		}
		edge[i] = railEdgeCell(tone)
	}
	frame = append(frame, m.topRule(leftWidth+1, m.width))
	frame = append(frame, joinColumns(
		edge,
		paintContent(railRows, railWidth, bodyHeight, panelHex()),
		seam,
		m.bleedColumn(bodyHeight),
		paintContent(contentRows, bleedWidth, bodyHeight, backdropHex()),
		m.focusRightColumn(bodyHeight),
	)...)
	bottom := m.boundedRuleRow(leftWidth+1, m.width, "▄")
	if m.mode == modeFocus && m.pane.box.ok {
		bottom = m.focusBottomRule(leftWidth+1, m.width)
	}
	frame = append(frame, bottom)
	for _, line := range splitLines(footer) {
		frame = append(frame, paint(line, m.width, backdropHex()))
	}
	screen := m.overlayLegendPeek(strings.Join(frame, "\n"), bodyHeight)
	return m.overlayTopRight(screen, m.statusToast(), m.listChromeRows()+1)
}

// viewOnePaneFrame draws a single panel across the whole terminal, for a width
// too narrow to give either panel a usable share.
//
// Which panel depends on what the operator is doing: focus mode is driving a
// live agent and the pane it mirrors is the whole point, so it takes the
// screen; everything else shows the list. Collapsing to the list in both cases
// would leave a focused session with nowhere to draw.
func (m *Model) viewOnePaneFrame(bodyHeight int, footer string) string {
	frame := []string{}
	paneWidth := m.width - 1
	rows := m.railLines(paneWidth, bodyHeight)
	tone := panelHex()
	// No content column, so a click can never land in a pane.
	m.pane.columnX = m.width + 1
	if m.mode == modeFocus {
		rows = m.contentLines(paneWidth, bodyHeight)
		tone = backdropHex()
		m.pane.columnX = 1
	}

	edge := make([]string, bodyHeight)
	for i := range edge {
		cell := tone
		if i < len(rows) && rows[i].tone != "" {
			cell = rows[i].tone
		}
		edge[i] = railEdgeCell(cell)
	}

	// There is no seam to decorate, so the caps fall back to the plain rule --
	// focused, the top one still carries the mode's title.
	frame = append(frame, m.topRule(m.width, m.width))
	frame = append(frame, joinColumns(edge, paintContent(rows, paneWidth, bodyHeight, tone))...)
	frame = append(frame, m.boundedRuleRow(m.width, m.width, "▄"))
	for _, line := range splitLines(footer) {
		frame = append(frame, paint(line, m.width, backdropHex()))
	}
	screen := m.overlayLegendPeek(strings.Join(frame, "\n"), bodyHeight)
	return m.overlayTopRight(screen, m.statusToast(), m.listChromeRows()+1)
}

// searchFieldLine is the live filter at the head of the rail: the typed
// query with a caret, and the key that closes it when there is room. With
// the field closed and a query still applied it drops the caret and offers
// to clear instead, so the rail always accounts for the entries it is
// holding back.
func (m *Model) searchFieldLine(width int) string {
	indent := spaces(railInset)
	glyph := keyStyle.Render("≡ ")
	caret := lipgloss.NewStyle().Foreground(colorAccent).Render("▏")
	hint := keyCapQuiet("esc", "close")
	if !m.searching {
		caret, hint = "", keyCapQuiet("esc", "clear")
	}
	chrome := railInset + cellWidth(glyph) + cellWidth(caret)

	if m.search == "" {
		field := glyph + subtleStyle.Render("fuzzy session search") + caret
		if gap := width - railInset - cellWidth(field) - cellWidth(hint) - 1; gap >= 2 {
			return indent + field + spaces(gap) + hint
		}
		return indent + field
	}
	// A query longer than the rail keeps its end: that is where the caret is
	// and where the next keystroke lands.
	room := width - chrome - cellWidth(hint) - 2
	if room < 8 {
		hint, room = "", width-chrome
	}
	query := m.search
	if cellWidth(query) > room {
		query = "…" + string([]rune(query)[len([]rune(query))-max(room-1, 1):])
	}
	field := glyph + valueStyle.Render(query) + caret
	if hint == "" {
		return indent + field
	}
	gap := width - railInset - cellWidth(field) - cellWidth(hint) - 1
	return indent + field + spaces(max(gap, 1)) + hint
}

// matchedInPane reports that the filter kept this session for what its pane
// is showing and for nothing the row prints. Such a row carries the query
// nowhere the reader can see it, so it reads as a stray match unless it says
// where the hit was.
func (m *Model) matchedInPane(sess store.Session) bool {
	query := strings.ToLower(strings.TrimSpace(m.search))
	if query == "" || matchesMetadata(sess, query) {
		return false
	}
	return strings.Contains(m.searchText[sess.ID], query)
}

// paneHitBadge wears the search field's own glyph, so the mark and the field
// that caused it read as one thing. Foreground only, like the inbox badge: a
// fill would punch through the band a selected row paints behind it.
func paneHitBadge() string {
	return keyStyle.Render("≡") + subtleStyle.Render("pane")
}

// railLines is the sessions rail: the entry list on top and the machine
// meters docked under it.
func (m *Model) railLines(width, height int) []contentLine {
	meters := m.computerLines(width)
	tier := dockTierFor(height, len(meters))
	switch tier {
	case dockBrief:
		meters = []string{m.computerBrief(width)}
	case dockNone:
		meters = nil
	}
	listHeight := height
	if meters != nil {
		listHeight = height - len(meters) - 1
	}
	// The opening block only rides a rail with the full dock, and only while
	// the list keeps the rows the full dock itself promises it. The selected
	// row's own identity sits right above the prompt it was launched with,
	// since both answer "what is this" before the dock answers "how loaded
	// is the machine".
	var opening []string
	if tier == dockFull {
		block := append(m.sessionDetailLines(width), m.promptLines(width)...)
		if len(block) > 0 && listHeight-len(block)-1 >= dockFullMinList {
			opening = block
			listHeight -= len(block) + 1
		}
	}
	var rows []contentLine
	// A tight terminal skips the padding rows outright: the blank line either
	// side of a field is a row the list needs more than the field does.
	tight := m.tight()
	// A banner costs the list the rows it paints plus its padding, so each one
	// is only laid while entries still have room under it: a rail that is all
	// banner says nothing about the fleet.
	const railBannerRows, railListMin = 3, 3
	room := func(cost int) bool { return listHeight-len(rows)-cost >= railListMin }
	// Search heads the list it filters, so the query sits over the entries it
	// is narrowing. It is also the field being typed into, so a rail too tight
	// for the padded block keeps the bare field rather than dropping it.
	if m.searching || m.search != "" {
		field := contentLine{text: m.searchFieldLine(width)}
		switch {
		case !tight && room(railBannerRows):
			rows = append(rows, contentLine{}, field, contentLine{})
		case room(1):
			rows = append(rows, field)
		}
	}
	// The list starts straight under the pane's top edge; the empty state
	// centers itself in the full list area instead. Every filter the rail is
	// under gets a badge here, since a narrowed list cannot show what it is
	// leaving out. A rail too tight for the padded block keeps the bare
	// badges, the way the search field does.
	if badges := m.filterBadgeLines(); len(badges) > 0 {
		lines := make([]contentLine, 0, len(badges))
		for _, badge := range badges {
			lines = append(lines, contentLine{text: badge})
		}
		switch {
		case !tight && room(len(lines)+2):
			rows = append(rows, contentLine{})
			rows = append(rows, lines...)
			rows = append(rows, contentLine{})
		case room(len(lines)):
			rows = append(rows, lines...)
		}
	}
	rows = append(rows, m.entryLines(m.rows, 0, width, max(listHeight-len(rows), 0))...)
	if len(rows) > listHeight {
		rows = rows[:listHeight]
	}
	// The dock is pinned at the rail's foot, with the opening block right
	// above it, so both sit at the same place on every terminal however
	// short the list is: the reader's eye goes to the bottom-left corner for
	// the machine, and just above it for what the selected agent was asked.
	for len(rows) < listHeight {
		rows = append(rows, contentLine{})
	}
	if opening != nil {
		rows = append(rows, contentLine{rule: true})
		for _, line := range opening {
			rows = append(rows, contentLine{text: line})
		}
	}
	if meters != nil {
		rows = append(rows, contentLine{rule: true})
		for _, line := range meters {
			rows = append(rows, contentLine{text: line})
		}
	}
	for len(rows) < height {
		rows = append(rows, contentLine{})
	}
	return rows[:height]
}

// firstPromptRows is how many opening prompts the rail shows for the
// selected session: every one the conversation index keeps.
const firstPromptRows = convo.FirstPromptCount

// promptBlockRows is the most rows the opening block spends on prompt text.
// One long prompt takes them all, cut short at the end; short prompts share
// them, so a session opened with "fix the build" and "then run it" shows
// both.
const promptBlockRows = 4

// openingPrompts is what the session was started for, as typed: the
// source's opening for a migration, otherwise the conversation's own opening
// once the sweep has read it, and the launch prompt until then.
func (m *Model) openingPrompts(sess store.Session) []string {
	if sess.MigrationID != "" && sess.MigrationID != sess.ID {
		for _, source := range m.sessions {
			if source.ID == sess.MigrationID {
				if opening := m.openingPrompts(source); len(opening) > 0 {
					return opening
				}
				break
			}
		}
	}
	if opening := typedPrompts(sess.MigrationOpening); len(opening) > 0 {
		return opening
	}
	if opening := m.firstPrompts[sess.ID]; len(opening) > 0 {
		return opening
	}
	if typed := launch.TypedPrompt(sess.LaunchPrompt); typed != "" {
		return []string{typed}
	}
	return nil
}

// promptLines is the opening block above the machine dock: a label and the
// selected session's first prompts, wrapped to the rail. Nothing for a
// group row, or a session with no opening on file yet.
func (m *Model) promptLines(width int) []string {
	sess, ok := m.selected()
	if !ok {
		return nil
	}
	opening := m.openingPrompts(sess)
	if len(opening) == 0 {
		return nil
	}
	pad := spaces(railInset)
	room := width - railInset - 2
	if room < 8 {
		return nil
	}
	lines := []string{pad + subtleStyle.Render("prompt")}
	budget := promptBlockRows
	for i, prompt := range opening {
		if i >= firstPromptRows || budget <= 0 {
			break
		}
		wrapped := strings.Split(ansi.Wordwrap(promptPlain(prompt), room, ""), "\n")
		if len(wrapped) > budget {
			// The first prompt is the one the block exists for, so it takes
			// whatever rows there are and says it was cut; a later one that
			// does not fit is left for the transcript.
			if i > 0 {
				break
			}
			wrapped = wrapped[:budget]
			last := wrapped[len(wrapped)-1]
			wrapped[len(wrapped)-1] = cellTruncate(last, max(cellWidth(last)-1, 1), "…")
		}
		for j, line := range wrapped {
			lead := "  "
			if j == 0 {
				lead = subtleStyle.Render("❯ ")
			}
			lines = append(lines, pad+lead+valueStyle.Render(line))
		}
		budget -= len(wrapped)
	}
	return lines
}

// railFactLabelWidth is the column every label in a rail identity block
// (session or group) is padded to, so the values under it line up.
const railFactLabelWidth = 8

// railFact is one label:value line of a rail identity block. value must
// already fit the room the caller computed, since a label column this
// narrow leaves no room for a second truncation pass.
func railFact(pad, label, value string) string {
	return pad + labelStyle.Render(padRight(label, railFactLabelWidth)) + value
}

// sessionDetailLines is the selected session's or group's identity — name,
// state, and the facts that place it — moved out of the content column and
// into the rail, right above the prompt block it now sits beside. Freeing
// the content column of its own head leaves nothing there but the live pane.
func (m *Model) sessionDetailLines(width int) []string {
	pad := spaces(railInset)
	room := width - railInset - 2
	if room < 8 {
		return nil
	}
	if group, ok := m.selectedGroup(); ok {
		return m.groupDetailLines(group, pad, room)
	}
	sess, ok := m.selected()
	if !ok {
		return nil
	}
	tool := sess.Tool
	if m.mode == modeRename && m.rename.sessID == sess.ID {
		if picked := m.renameTool(); picked != "" {
			tool = picked
		}
	}
	// A session on a chosen model wears it on the same chip as the CLI it
	// qualifies. Most sessions run their CLI's default and say nothing, so
	// the chip only grows where the answer is not the obvious one.
	if sess.Model != "" {
		tool += " " + sess.Model
	}
	// And whose subscription it spends, for the same reason.
	if sess.Account != "" {
		tool += " as " + sess.Account
	}
	name := lipgloss.NewStyle().Foreground(colorBright).Bold(true).Render(m.displayName(sess))
	if queued := m.queuedMessages[sess.ID]; queued > 0 {
		name = inboxBadge(queued) + " " + name
	}
	head := name + "  " + chipStyle.Render(tool)
	state := lipgloss.NewStyle().Foreground(statusColor(sess.Status)).
		Render(statusGlyph(sess.Status)+" "+statusLabel(sess.Status)) +
		subtleStyle.Render(" · "+relSince(lastActivity(sess)))
	factRoom := max(room-railFactLabelWidth, 1)
	usage := ""
	if m.procFor == sess.ID && m.proc.OK {
		usage = cellTruncate(
			labelStyle.Render("cpu ")+valueStyle.Render(fmt.Sprintf("%.1f%%", m.proc.CPUPercent))+
				subtleStyle.Render(" · ")+labelStyle.Render("ram ")+valueStyle.Render(fmt.Sprintf("%.1f%%", m.proc.RamPercent))+
				subtleStyle.Render(" · ")+valueStyle.Render(humanBytes(m.proc.RSS)),
			factRoom, "…")
	}
	lines := []string{
		pad + subtleStyle.Render("session"),
		pad + cellTruncate(head, room, "…"),
		pad + cellTruncate(state, room, "…"),
		railFact(pad, "group", lipgloss.NewStyle().Foreground(colorAccent2).Render(cellTruncate(displayGroup(sess.Group), factRoom, "…"))),
		railFact(pad, "started", subtleStyle.Render(cellTruncate(relSince(sess.CreatedAt), factRoom, "…"))),
		railFact(pad, "dir", mutedStyle.Render(truncateTail(sess.Cwd, factRoom))),
	}
	if usage != "" {
		lines = append(lines, railFact(pad, "usage", usage))
	}
	return lines
}

// groupDetailLines is sessionDetailLines' counterpart for a selected group
// row: its name, how many agents sit under it, where it starts new ones, and
// what they are all doing.
func (m *Model) groupDetailLines(group, pad string, room int) []string {
	count := m.groupSessionCount(group)
	countLabel := fmt.Sprintf("%d agents", count)
	if count == 1 {
		countLabel = "1 agent"
	}
	title := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).Render(displayGroup(group))
	lines := []string{
		pad + subtleStyle.Render("group"),
		pad + cellTruncate(title+"  "+chipStyle.Render(countLabel), room, "…"),
	}
	path := m.groupPaths[group]
	suffix := ""
	if path == "" {
		path = m.groupDefaultDir(group)
		suffix = " " + subtleStyle.Render("inherited")
	}
	dir := mutedStyle.Render(truncateTail(path, max(room-railFactLabelWidth-cellWidth(suffix), 1))) + suffix
	lines = append(lines, railFact(pad, "dir", dir))
	if breakdown := m.groupStatusBreakdown(group); breakdown != "" {
		lines = append(lines, railFact(pad, "state", trimmedValue(breakdown)(max(room-railFactLabelWidth, 1))))
	}
	return lines
}

// promptPlain flattens a prompt to one run of words, so line breaks in what
// was typed do not spend the opening block's rows on blank lines. A pasted
// image reaches the agent as the path it was written to, which says nothing
// about the task, so the pictures drop out and the words stay.
func promptPlain(prompt string) string {
	words := make([]string, 0, len(strings.Fields(prompt)))
	for _, word := range strings.Fields(prompt) {
		if clipboard.IsPastePath(word) {
			continue
		}
		words = append(words, word)
	}
	return strings.Join(words, " ")
}

// filterBadgeLines is one badge per narrowing the rail is under, each next
// to the key that lifts it. Ordered widest to narrowest: triage rebuilds
// the whole rail, the archive is a different fleet, the status filter hides
// sessions, hiding empty groups only hides scaffolding.
func (m *Model) filterBadgeLines() []string {
	var lines []string
	badge := func(label, key, action string) {
		lines = append(lines, spaces(railInset)+scopeBadgeStyle.Render(label)+
			subtleStyle.Render("  ")+keyCap(key, action))
	}
	if m.triage {
		// The rail has flattened the groups away by the time this paints, so
		// the badge is the only thing left saying which group the queue was
		// drawn from. Truncated rather than wrapped: the badge shares its
		// line with the key that lifts it.
		label, key, out := "TRIAGE", "i", "back to groups"
		// The gate replaces that badge rather than sitting above it. It is
		// the same queue under the same scope, and two badges would offer
		// two ways out of one state, only one of which gives the rail back.
		if m.gate.on {
			label, key, out = "GATE", "G", "stop the gate"
		}
		if m.triageScope != "" {
			label += " " + strings.ToUpper(cellTruncate(baseName(m.triageScope), 12, "…"))
		}
		badge(label, key, out)
	}
	if m.layout == layoutBoard {
		// The other badges here narrow what the list shows; this one
		// reports the frame. It earns its row because the state hides its
		// own way out: a focused session with the rail away has no list to
		// print the key on, so the board has to carry it.
		badge("WIDE", `\`, "bring the pane back")
	}
	if m.showArchived {
		badge("ARCHIVED", "t", "back to active")
	}
	if m.statusFilter.active() {
		badge(strings.ToUpper(m.statusFilter.label()), "w", "show all")
	}
	m.extensionFilterBadges(badge)
	if m.hideEmptyGroups {
		badge("HIDE EMPTY", "e", "show empty")
	}
	return lines
}

// entryLines renders the visible slice of rows, which sit at offset in
// m.rows so the cursor and the tree guides still resolve against the whole
// list. Entries are two lines tall, so the window is measured in lines
// rather than rows. Each line carries the tone its entry painted, which the
// edge column matches.
func (m *Model) entryLines(rows []treeRow, offset, width, height int) []contentLine {
	render := func(entry treeRow, selected bool, index int, tone string) string {
		if height < m.entryHeight(entry)+2 {
			return m.renderTreeRowContent(entry, selected, width, index, tone)
		}
		return m.renderTreeRow(entry, selected, width, index, tone)
	}
	// Root alone is still an empty list: it says what the rail holds, not
	// what to do about it being empty.
	if rest := rowsBelowRoot(rows); len(rest) == 0 {
		var lines []contentLine
		for i, entry := range rows {
			for _, line := range splitLines(render(entry, m.cursor == offset+i, offset+i, panelHex())) {
				lines = append(lines, contentLine{text: line})
			}
		}
		for _, line := range m.emptyRailLines(width, height-len(lines)) {
			lines = append(lines, contentLine{text: line})
		}
		return lines
	}
	heights := make([]int, len(rows))
	// An extension's header over a session is a line of the entry, drawn
	// outside its box, so the window keeps it with the row it heads.
	headers := make([][]string, len(rows))
	for i := range heights {
		headers[i] = m.extensionHeaderLines(rows[i], width, offset+i)
		heights[i] = m.entryHeight(rows[i]) + len(headers[i])
		if offset+i == m.cursor && height >= heights[i]+2 && width >= 4 {
			heights[i] += 2
		}
	}
	start, end := lineWindow(heights, m.cursor-offset, height)

	var lines []contentLine
	for i := start; i < end; i++ {
		selected := offset+i == m.cursor
		entry := rows[i]
		// The cursor's entry is marked by its box rather than a fill; only a
		// row being renamed lifts onto the band, behind the field being typed.
		tone := panelHex()
		if m.renamingRow(entry) {
			tone = selectedHex()
		}
		for _, line := range headers[i] {
			lines = append(lines, contentLine{text: line, tone: panelHex()})
		}
		for _, line := range splitLines(render(entry, selected, offset+i, tone)) {
			lines = append(lines, contentLine{text: line, tone: tone})
		}
	}
	// The counters ride in whatever room the entries leave. Claiming a line
	// they do not have would trim an entry's last line away, and half a
	// two-line entry reads as a whole one that lost its meta.
	spare := height - len(lines)
	if start > 0 && spare > 0 {
		lines = append([]contentLine{{text: subtleText(spaces(railInset) + fmt.Sprintf("↑ %d more", start))}}, lines...)
		spare--
	}
	if end < len(rows) && spare > 0 {
		lines = append(lines, contentLine{text: subtleText(spaces(railInset) + fmt.Sprintf("↓ %d more", len(rows)-end))})
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// entryHeight is how many lines an entry paints: one in the compact list,
// two once the comfortable density unstacks the meta onto its own line.
// Groups match sessions either way, since a ragged list of one- and
// two-line rows reads as gaps rather than as rhythm.
func (m *Model) entryHeight(entry treeRow) int {
	// An artifact has no second line to unstack: its state already rides
	// beside it, and a blank line under every pull request would cost the
	// rail more rows than the work it is showing.
	if entry.isArtifact() {
		return 1
	}
	if m.stackedRows() {
		return 2
	}
	return 1
}

// lineWindow keeps the cursor's entry fully visible inside a line budget,
// scrolling by whole entries so an entry is never cut in half.
func lineWindow(heights []int, cursor, budget int) (int, int) {
	if len(heights) == 0 || budget <= 0 {
		return 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(heights) {
		cursor = len(heights) - 1
	}
	total := 0
	for _, h := range heights {
		total += h
	}
	if total <= budget {
		return 0, len(heights)
	}
	// Grow a window around the cursor, preferring to keep entries above it
	// on screen so the list does not jump when stepping down.
	start, end, used := cursor, cursor+1, heights[cursor]
	for {
		grew := false
		if end < len(heights) && used+heights[end] <= budget-1 {
			used += heights[end]
			end++
			grew = true
		}
		if start > 0 && used+heights[start-1] <= budget-1 {
			start--
			used += heights[start]
			grew = true
		}
		if !grew {
			break
		}
	}
	return start, end
}

func (m *Model) emptyRailLines(width, height int) []string {
	title := "no sessions yet"
	hint := keyCap("n", "starts one")
	if m.showArchived {
		title = "nothing archived"
		hint = keyCap("t", "back to active")
	}
	if m.triage && m.triageScope != "" {
		title = "nothing in " + baseName(m.triageScope)
		hint = keyCap("i", "back to groups")
	}
	if m.statusFilter.active() {
		title = "nothing needs " + m.statusFilter.label()
		hint = keyCap("w", "show all")
	}
	if search := strings.TrimSpace(m.search); search != "" {
		title = "no matches"
		hint = subtleStyle.Render("for \"" + search + "\"")
	}
	titleLine := centerLine(
		lipgloss.NewStyle().Bold(true).Foreground(colorBright).Render(title),
		width,
	)
	hintLine := centerLine(hint, width)
	block := []string{titleLine, "", hintLine}
	if height <= 0 {
		return block
	}
	if height < len(block) {
		return block[:height]
	}
	out := make([]string, height)
	start := (height - len(block)) / 2
	copy(out[start:], block)
	return out
}

// centerLine pads a styled string so its visible text sits in the middle
// of width columns.
func centerLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	w := cellWidth(s)
	if w >= width {
		return cellTruncate(s, width, "…")
	}
	left := (width - w) / 2
	return spaces(left) + s
}

// treeGuidesAt is the ancestry trail left of a nested entry: a branch
// connector — ├─ mid-list, ╰─ for the last child — behind one guide per
// ancestor, so a group's children hang off its branch the way the legacy
// tree drew them. A slot goes quiet once its level has no further
// siblings below, which is what closes a branch off.
func (m *Model) treeGuidesAt(index int) string {
	return m.treeGuides(index, false)
}

func (m *Model) treeGuides(index int, trail bool) string {
	if index < 0 || index >= len(m.rows) {
		return ""
	}
	entry := m.rows[index]
	depth := entry.depth
	if depth <= 0 {
		return ""
	}
	var guides strings.Builder
	for slot := 1; slot <= depth; slot++ {
		continues := m.slotContinues(index, slot)
		glyph := "   "
		branch := slot == depth || (entry.migrationHead && slot == entry.migrationDepth-1)
		switch {
		case !trail && entry.migrationHead && slot == entry.migrationDepth:
			glyph = "╭─ "
		case !trail && branch && continues:
			glyph = "├─ "
		case !trail && branch:
			glyph = "╰─ "
		case continues:
			glyph = "│  "
		}
		if slot == entry.migrationDepth {
			guides.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Render(glyph))
		} else {
			guides.WriteString(subtleText(glyph))
		}
	}
	return guides.String()
}

// treeGuideTrail is the same ancestry read one line lower: every branch
// the entry's own row connected to carries straight down past its second
// line, so a two-line entry cannot leave a gap in the tree.
func (m *Model) treeGuideTrail(index int) string {
	return m.treeGuides(index, true)
}

// slotContinues reports whether the level named by slot has another entry
// below index. A slot goes quiet once its level has no further siblings,
// which is what closes a branch off.
func (m *Model) slotContinues(index, slot int) bool {
	for j := index + 1; j < len(m.rows); j++ {
		depth := m.rows[j].depth
		if m.rows[j].migrationHead {
			depth--
		}
		if depth < slot {
			return false
		}
		if depth == slot {
			return true
		}
	}
	return false
}

// selectionBorder draws the cursor's box from eighth blocks: the top rule sits
// at the bottom of the line above and the bottom rule at the top of the line
// below, so the frame hugs the row's text rather than standing a full cell off
// it the way box-drawing lines do.
var selectionBorder = lipgloss.Border{
	Top: "▁", Bottom: "▔", Left: "▏", Right: "▕",
	TopLeft: "▁", TopRight: "▁", BottomLeft: "▔", BottomRight: "▔",
}

// renderTreeRow paints one entry: a status dot, the name, and what the
// entry is doing set against the row's far edge. The selected entry is
// framed in a box that takes the inset cell and the last column for its sides,
// so the name and status keep their room. The frame is muted while the list
// has the keyboard and turns accent while the entry's pane holds focus, so the
// row says which mode it is in without a badge pushing its status aside.
func (m *Model) renderTreeRow(entry treeRow, selected bool, width, index int, bg string) string {
	if !selected || width < 4 {
		return m.renderTreeRowContent(entry, selected, width, index, bg)
	}
	lines := splitLines(m.renderTreeRowContent(entry, selected, width, index, bg))
	for i, line := range lines {
		lines[i] = ansi.Cut(line, 1, width-1)
	}
	edge := colorDim
	if m.mode == modeFocus {
		edge = colorAccent
	}
	return lipgloss.NewStyle().
		Border(selectionBorder).
		BorderForeground(edge).
		BorderBackground(lipgloss.Color(bg)).
		Render(strings.Join(lines, "\n"))
}

func (m *Model) renderTreeRowContent(entry treeRow, selected bool, width, index int, bg string) string {
	pad := spaces(railInset)
	guides := m.treeGuidesAt(index)
	trail := m.treeGuideTrail(index)

	if m.renamingRow(entry) {
		line := pad + guides + m.renameRowInput(entry, width-railGutter-cellWidth(guides))
		row := paint(line, width, selectedHex())
		if m.stackedRows() {
			row += "\n" + paint(pad+trail, width, selectedHex())
		}
		return row
	}

	if entry.isArtifact() {
		return m.renderArtifactEntry(entry, selected, width, pad, guides, bg)
	}
	if entry.isGroup {
		return m.renderGroupEntry(entry, selected, width, pad, guides, trail, bg)
	}
	return m.renderSessionEntry(entry, selected, width, pad, guides, trail, bg)
}

// A shell takes a caret rather than an idle dot it would never leave, but
// a pane that has gone still has to say so.
func (m *Model) sessionGlyph(sess store.Session) string {
	if sess.Status == status.Starting {
		return statusTint(status.Starting, startupFrames[m.startupPhase%len(startupFrames)])
	}
	resting := sess.Status != status.Dead && sess.Status != status.Errored
	if resting && m.isShell(sess.Tool) {
		return subtleText(shellGlyph)
	}
	return statusTint(sess.Status, statusGlyph(sess.Status))
}

// namePlaceholder stands in for the name a spawn generated while the agent
// it asked to name itself has not answered, so the row settles on one name
// instead of flashing a throwaway one first.
const namePlaceholder = "…"

// placeholderPromptWidth caps the prompt a waiting row borrows. It is what
// the narrowest rail affords beside a starting row's state, tool and age, so
// the row keeps the shape every other row has instead of pushing a column
// off its own end.
const placeholderPromptWidth = 12

// renameGrace caps the wait for that answer. It spans the whole way there,
// the boot, the directive reaching the agent, and the command it runs, so it
// is generous; past it the session keeps the name it was given.
const renameGrace = time.Minute

// awaitedRename is what a spawn launched with: the name generated for it,
// which it falls back to, and the prompt it was given, which says which task
// the row is while it has no name of its own.
type awaitedRename struct {
	generated string
	prompt    string
}

// awaitingRename drops the record it reads as soon as the wait is over, so
// a session settling on its name needs nothing to sweep the map after it.
func (m *Model) awaitingRename(sess store.Session) bool {
	awaited, ok := m.awaitedRenames[sess.ID]
	if !ok {
		return false
	}
	if sess.Name == awaited.generated && sess.Status != status.Dead &&
		time.Since(sess.CreatedAt) < renameGrace {
		return true
	}
	delete(m.awaitedRenames, sess.ID)
	return false
}

// displayName is what every reading of a session prints, so the rail row and
// the columns beside it never disagree about who an agent is.
func (m *Model) displayName(sess store.Session) string {
	if !m.awaitingRename(sess) {
		if sess.MigrationID != "" {
			return "⇄ " + sess.Name
		}
		return sess.Name
	}
	// The stored LaunchPrompt is the decorated one, carrying the rename
	// directive the agent was sent; what the row wants is what was typed.
	if preview := promptPreview(m.awaitedRenames[sess.ID].prompt); preview != "" {
		return preview
	}
	return namePlaceholder
}

// promptPreview flattens a prompt into the one short line a row can wear as
// a name, so five agents spawned in a burst say which is which right away.
// A pasted image reaches the agent as the path it was written to, which
// would name every image-first spawn the same thing, so the pictures drop
// out of the preview and the words stay.
func promptPreview(prompt string) string {
	return cellTruncate(promptPlain(prompt), placeholderPromptWidth, "…")
}

func (m *Model) renderSessionEntry(entry treeRow, selected bool, width int, pad, guides, trail, bg string) string {
	sess := entry.sess
	dot := m.sessionGlyph(sess)
	nameStyle := valueStyle
	if selected {
		nameStyle = selectedNameStyle
	}
	head := pad + guides + m.railWorkFold(sess) + dot + " " + nameStyle.Render(m.displayName(sess))
	if queued := m.queuedMessages[sess.ID]; queued > 0 {
		head += " " + inboxBadge(queued)
	}
	if m.matchedInPane(sess) {
		head += " " + paneHitBadge()
	} else if m.matchedInHistory(sess) {
		hit, _ := m.historyHit(sess)
		head += " " + historyHitBadge(hit)
	}
	// A muted row stays on the rail rather than being filtered out of it:
	// silencing something the operator can no longer see is how a session
	// gets lost, and the mark is also the only prompt that "." un-mutes it.
	if m.isMuted(sess) {
		head += " " + subtleText(mutedGlyph())
	}
	// Beside the mute rather than in place of the status mark, and for the
	// same reason: "waiting" is still true of the session. What the mark adds
	// is that nobody can answer it from here -- which is why it is drawn in
	// the errored tint rather than the subtle one. A mute is a queue
	// decision; this is a fault.
	if m.isDeaf(sess) {
		head += " " + statusTint(status.Errored, deafGlyph())
	}
	// Last of the three, and the only one that is not a complaint: the status
	// beside it is true, and the mark adds only that the pane is all the
	// board has to derive it from.
	if m.isHookless(sess) {
		head += " " + subtleText(hooklessGlyph())
	}
	head += m.priorityMarker(sess)

	metaText := subtleText
	if selected {
		metaText = mutedText
	}
	// A session names its state in words as well as in its dot; a group,
	// whose row rolls several states together, is left to its dots.
	state := statusTint(sess.Status, statusLabel(sess.Status)) + metaText(" · "+sess.Tool)
	// An archived row is on a clock, and the clock is the one thing about it
	// that is not recoverable by looking. It goes after the age, in the same
	// muted weight: it is a fact about the row, not a warning, right up
	// until it is the last day. Both trail off one styling call rather than
	// two, which is the whole cost of carrying it on every frame.
	trailing := " · " + relSince(lastActivity(sess))
	if left := archiveTimeLeft(sess); left != "" {
		trailing += " · " + left
	}
	age := metaText(trailing)
	meta := state + age
	indent := metaIndent(pad, trail) + spaces(m.railFoldReserve())

	// The badge is the folded reading of the rows underneath; once they are
	// on screen it is the same thing said twice, in less detail. Wherever it
	// goes, it goes outside the state, the tool and the age rather than into
	// them: spliced between the state and the age it pushed "claude · 2h ago"
	// a different distance on every row that had work, and the run a reader
	// finds a row by stopped reading down the list at all.
	//
	// Which side it goes to is the one thing the two densities differ on,
	// because they have their spare cells in different places. A one-line row
	// sets the meta hard against the right edge, so the badge goes left, with
	// the name -- and there it competes with the name, and the name wins. A
	// row whose name already fills the line wears no badge rather than a
	// truncated name; the fold arrow beside the name still says there is
	// work, so all that is lost is the count.
	//
	// A comfortable row already puts the meta on its own line at a fixed
	// indent, so nothing there was ever out of column and the badge has no
	// reason to leave. It would be worse off if it did: the name line is
	// where the long names are, and on a forty-column rail moving the badge
	// up there cost the row its count for no alignment gained. So it stays on
	// the meta line and follows the age instead of interrupting it.
	childRoom := width - cellWidth(indent) - cellWidth(meta)
	if !m.stackedRows() {
		childRoom = width - railGutter - cellWidth(head) - 2 - cellWidth(meta)
	}
	child := m.childBadge(sess.ID, childRoom)

	// A folded parent must not be the only place in the program where a
	// fan-out is invisible.
	if m.stackedRows() {
		meta += child
	} else {
		head += child
	}

	if !m.railWorkExpanded(sess.ID) {
		if m.stackedRows() {
			meta += m.workBadge(sess.ID, width-cellWidth(indent)-cellWidth(meta))
		} else {
			head += m.workBadge(sess.ID, width-railGutter-cellWidth(head)-2-cellWidth(meta))
		}
	}

	// An extension's badges come last, after everything the board says
	// about the row itself, and take only the room that is left.
	if m.stackedRows() {
		meta += m.extensionBadges(sess.ID, width-cellWidth(indent)-cellWidth(meta))
	} else {
		head += m.extensionBadges(sess.ID, width-railGutter-cellWidth(head)-2-cellWidth(meta))
	}

	if m.stackedRows() {
		return stackedRow(head, indent+meta, width, bg)
	}
	return paint(rowColumns(head, meta, width-railGutter), width, bg)
}

// renderArtifactEntry paints one pull request or ticket under the session
// working on it, in the shape a session row already has: a coloured mark, a
// name, and the state set against the far edge. The rail is narrower than the
// work card, so the repository gives way before the number does -- a
// truncated "example-org/sample-repo…" loses the only part that names the thing.
func (m *Model) renderArtifactEntry(entry treeRow, selected bool, width int, pad, guides, bg string) string {
	art := entry.art
	nameStyle := mutedStyle
	metaText := subtleText
	if selected {
		nameStyle = selectedLabelStyle
		metaText = mutedText
	}
	room := width - railGutter - cellWidth(guides) - 2 - cellWidth(art.detail) - 2
	label := art.label
	if cellWidth(label) > room {
		label = art.short
	}
	// The child a rolled-up row came from gives way before the number does:
	// the number names the artifact, and the child is only where it came from.
	detail := art.detail
	if art.from != "" {
		spare := room - cellWidth(label) - cellWidth(childMark)
		if spare < minChildName && label != art.short {
			label = art.short
			spare = room - cellWidth(label) - cellWidth(childMark)
		}
		if spare >= minChildName {
			tag := childMark + cellTruncate(art.from, spare, "…")
			detail += tag
			room -= cellWidth(tag)
		}
	}
	// Unlike the work card, the link is not gated on width: a narrow rail has
	// already swapped the label for its short form, and the number is still
	// the thing to click.
	head := pad + guides + tinted(art.stateHint).Render(art.glyph) +
		" " + hyperlink(art.url, nameStyle.Render(cellTruncate(label, max(room, 1), "…")))
	return paint(rowColumns(head, metaText(detail), width-railGutter), width, bg)
}

// childMark leads the name of the child a rolled-up artifact came from, and
// minChildName is the fewest cells of that name worth drawing at all.
const (
	childMark    = " ↳ "
	minChildName = 4
)

// metaIndent lines a second row line up under the name on the first, past
// the entry's guides and the glyph column ahead of it.
func metaIndent(pad, trail string) string {
	return pad + trail + "  "
}

func stackedRow(head, meta string, width int, bg string) string {
	return paint(head, width, bg) + "\n" + paint(meta, width, bg)
}

func (m *Model) renderGroupEntry(entry treeRow, selected bool, width int, pad, guides, trail, bg string) string {
	marker := "▾"
	if m.collapsed[entry.group] {
		marker = "▸"
	}
	nameStyle := groupNameStyle
	if selected {
		nameStyle = selectedNameStyle
	}
	name := baseName(entry.group)
	if entry.isRoot() {
		// Nothing nests under root, so the marker is a blank that holds the column.
		marker, name = " ", "root"
		if !selected {
			nameStyle = rootGroupNameStyle
		}
	}
	head := pad + guides + subtleText(marker) + " " + m.groupNumberLabel(entry.group) + nameStyle.Render(name)
	if badge := priorityBadge(m.priorityGroups[entry.group]); badge != "" {
		head += " " + badge
	}

	// What the group is doing rides on the same line as its name, so a
	// folded group still reports its subtree without being opened. It is
	// written in dots rather than words: the counts state the size too.
	meta := m.groupStatusGlyphs(entry.group)
	if meta == "" {
		meta = subtleText("no agents yet")
	}

	if m.stackedRows() {
		return stackedRow(head, metaIndent(pad, trail)+meta, width, bg)
	}
	return paint(rowColumns(head, meta, width-railGutter), width, bg)
}

// groupNumberLabel is the outline number printed ahead of a group's name,
// with the trailing space that separates them, or "" when the view is not
// numbering groups.
//
// While a number is still being typed towards a group, the ones it could
// still become are lifted into the key tint, so a half-finished number shows
// what finishing it would reach. Once the digits name a group the tint is
// that group's alone: its children carry the number as a prefix, and lighting
// "1.1" up alongside the "1" the cursor just landed on reads as though both
// were selected.
func (m *Model) groupNumberLabel(path string) string {
	number := m.groupNumber(path)
	if number == "" {
		return ""
	}
	if m.jumpTints(number) {
		return keyStyle.Render(number) + " "
	}
	return subtleText(number) + " "
}

// jumpTints reports whether the number being typed should light this group's
// number up.
func (m *Model) jumpTints(number string) bool {
	if m.jump.buffer == "" {
		return false
	}
	if m.namesGroup(m.jump.buffer) {
		return number == m.jump.buffer
	}
	return strings.HasPrefix(number, m.jump.buffer)
}

// meterKey is everything computerLines reads: the two samples, the width it
// lays them out at, and the theme generation its styles came from.
type meterKey struct {
	gen   int
	width int
	snap  sysstat.Snapshot
	net   netStats
}

// computerLines is the machine block docked at the rail's foot: a label
// and one thin meter per resource.
func (m *Model) computerLines(width int) []string {
	key := meterKey{gen: renderGen, width: width, snap: m.snap, net: m.net}
	if m.meterMemoOK && m.meterMemoKey == key {
		return m.meterMemo
	}
	out := m.buildComputerLines(width)
	m.meterMemo, m.meterMemoKey, m.meterMemoOK = out, key, true
	return out
}

func (m *Model) buildComputerLines(width int) []string {
	snap := m.snap
	pad := spaces(railInset)
	barWidth := width - 22
	if barWidth < 4 {
		barWidth = 4
	}
	if barWidth > 10 {
		barWidth = 10
	}

	meter := func(label string, percent float64, ok bool, extra string) string {
		if !ok {
			return pad + labelStyle.Width(5).Render(label) + subtleStyle.Render("n/a")
		}
		line := pad + labelStyle.Width(5).Render(label) + gauge(percent, barWidth) +
			valueStyle.Render(fmt.Sprintf(" %3.0f%%", percent))
		if extra != "" {
			line += subtleStyle.Render(" " + extra)
		}
		return line
	}

	lines := []string{pad + subtleStyle.Render("computer")}
	lines = append(lines,
		meter("cpu", snap.CPUPercent, snap.CPUOK, ""),
		meter("mem", snap.MemPercent, snap.MemOK, humanBytes(snap.MemUsed)+"/"+humanBytes(snap.MemTotal)),
	)
	if snap.SwapOK && snap.SwapCeiling > 0 {
		// Percent is used/ceiling: how far swap is from running out, not
		// how full macOS's current, pressure-sized allocation is.
		lines = append(lines, meter("swap", snap.SwapPercent, true,
			humanBytes(snap.SwapUsed)+"/"+humanBytes(snap.SwapCeiling)))
	}
	if snap.DiskOK {
		lines = append(lines, meter("disk", snap.DiskPercent, true,
			humanBytes(snap.DiskFree)+" free"))
	} else {
		lines = append(lines, meter("disk", 0, false, ""))
	}
	if temps := tempReadings(snap); temps != "" {
		lines = append(lines, pad+labelStyle.Width(5).Render("temp")+temps)
	}
	if m.net.rates {
		lines = append(lines, pad+labelStyle.Width(5).Render("net")+
			valueStyle.Render("↓ "+humanBytes(m.net.down)+"/s")+
			subtleStyle.Render("  ↑ "+humanBytes(m.net.up)+"/s"))
	}
	return append(lines, "")
}

// computerBrief is the machine block folded to one line for a rail too short
// to dock the full block: the three readings that decide whether the machine
// has room for another agent, without the gauges, temps or network.
func (m *Model) computerBrief(width int) string {
	snap := m.snap
	sep := subtleStyle.Render(" · ")
	reading := func(label string, percent float64, ok bool) string {
		if !ok {
			return labelStyle.Render(label+" ") + subtleStyle.Render("n/a")
		}
		return labelStyle.Render(label+" ") + valueStyle.Render(fmt.Sprintf("%.0f%%", percent))
	}
	line := reading("cpu", snap.CPUPercent, snap.CPUOK) + sep + reading("mem", snap.MemPercent, snap.MemOK)
	if disk := reading("disk", snap.DiskPercent, snap.DiskOK); cellWidth(line)+cellWidth(sep)+cellWidth(disk)+railInset <= width {
		line += sep + disk
	}
	return spaces(railInset) + line
}

func tempReadings(snap sysstat.Snapshot) string {
	var parts []string
	if snap.CPUTempOK {
		parts = append(parts, valueStyle.Render(fmt.Sprintf("cpu %.0f°C", snap.CPUTemp)))
	}
	if snap.GPUTempOK {
		parts = append(parts, valueStyle.Render(fmt.Sprintf("gpu %.0f°C", snap.GPUTemp)))
	}
	if snap.SoCTempOK {
		parts = append(parts, valueStyle.Render(fmt.Sprintf("soc %.0f°C", snap.SoCTemp)))
	}
	return strings.Join(parts, subtleStyle.Render("  "))
}

// contentLines is the right column: what the cursor is on, then its live
// pane, with the quick prompt docked at the foot when it is open. width is
// the whole column; our own blocks sit inside its gutters, while the
// captured pane spans it edge to edge.
func (m *Model) contentLines(width, height int) []contentLine {
	gutter := spaces(contentGutter)
	inner := width - 2*contentGutter
	ours := func(lines []string) []contentLine {
		out := make([]contentLine, len(lines))
		for i, line := range lines {
			out[i] = contentLine{text: gutter + line}
		}
		return out
	}

	var bar []contentLine
	if m.quick.active && (m.mode != modeFocus || m.showsConversation()) {
		bar = append([]contentLine{{}}, ours(splitLines(m.viewQuickBar(inner)))...)
	}
	var body []contentLine
	// The frame's own top rule already seams the header off the body, so the
	// column opens straight into its content: a second hairline one row under
	// the first drew as a thickened line and cost the mirrored pane a row.
	rest := height - len(bar)
	if rest >= 3 {
		if group, ok := m.selectedGroup(); ok {
			body = append(body, ours(splitLines(m.viewGroupAgents(group, inner, rest)))...)
		} else {
			if _, ok := m.selected(); !ok {
				body = append(body, ours(splitLines(mutedStyle.Render("Select a session to inspect it.")))...)
			} else {
				m.previewBodyOffset = len(body)
				body = append(body, m.previewLines(width, rest, gutter)...)
			}
		}
	}
	for len(body)+len(bar) < height {
		body = append(body, contentLine{})
	}
	return append(body[:max(height-len(bar), 0)], bar...)
}

// focusRuleTail is the titled run of the focused pane's top edge, drawn over
// the content column of the frame rule the body already spends. It titles the
// ring so the mode names itself where the eye already is, and scrolled back it
// says how far and how to catch up: this row is on every terminal, where the
// notice card is not. corner closes it on the ring's right upright, which is
// only drawn once there is a pane box for the uprights to run down.
func (m *Model) focusRuleTail(width int, corner bool) string {
	title := " focused · ctrl+q back · " + keymap.Display("alt+↑↓") + " scroll "
	if m.scrolledBack() {
		title = fmt.Sprintf(" focused · %d lines back · %s or type to catch up ", m.focusScroll, keymap.Display("alt+down"))
	}
	if m.gate.on {
		mode, next := "Terminal", "messages"
		if m.gate.menu {
			mode, next = "Messages", "terminal"
		}
		title = fmt.Sprintf(" %s · %s %s · %s top ", mode, m.fullCap(keymap.ContextFocus, keymap.ToggleGateInput), next, m.gateCap(keymap.PreviewTop))
		if lipgloss.Width(title) > width-1 {
			title = fmt.Sprintf(" %s · %s %s ", mode, m.fullCap(keymap.ContextFocus, keymap.ToggleGateInput), next)
		}
	}
	edge := 0
	if corner {
		edge = 1
	}
	rest := width - edge - lipgloss.Width(title)
	if rest < 0 {
		// Narrower than the words: the ring still has to close, or its right
		// upright runs down from nothing, so the rule drops the title rather
		// than truncating the corner off the end of it.
		title, rest = "", width-edge
	}
	rule := annotationStyle.Render(title)
	if rest > 0 {
		rule += focusEdgeStyle.Render(strings.Repeat("─", rest))
	}
	if corner {
		rule += focusEdgeStyle.Render("╮")
	}
	return rule
}

var startupFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const startupRingPoints = 12

func (m *Model) startupLoader(width, height int) []string {
	sess, ok := m.selected()
	if !ok || m.mode == modeFocus || sess.Status != status.Starting || paneBooted(m.preview) {
		return nil
	}
	return ringLoader(width, height, "starting up", m.startupPhase)
}

func ringLoader(width, height int, label string, phase int) []string {
	accent := lipgloss.NewStyle().Foreground(statusColor(status.Starting)).Bold(true)
	glow := lipgloss.NewStyle().Foreground(statusColor(status.Starting))
	phase = phase % startupRingPoints
	dot := func(position int) string {
		switch position {
		case phase:
			return accent.Render("●")
		case (phase + startupRingPoints - 1) % startupRingPoints:
			return glow.Render("•")
		default:
			return subtleStyle.Render("·")
		}
	}

	block := []string{
		centerLine(dot(11)+"   "+dot(0)+"   "+dot(1), width),
		centerLine(dot(10)+"       "+dot(2), width),
		centerLine(dot(9)+"       "+dot(3), width),
		centerLine(dot(8)+"       "+dot(4), width),
		centerLine(dot(7)+"   "+dot(6)+"   "+dot(5), width),
		centerLine(valueStyle.Bold(true).Render(label), width),
	}
	if height <= len(block) {
		return block[:height]
	}
	lines := make([]string, height)
	copy(lines[(height-len(block))/2:], block)
	return lines
}

// previewLines is the captured pane, filling every row under the detail
// separator. The captured rows are marked raw and drawn without the
// column's gutters: painting our backdrop behind an agent's own CLI colors
// would replace the background it drew itself, and insetting its output
// would put a margin around a terminal that has its own.
func (m *Model) previewLines(width, height int, gutter string) []contentLine {
	if m.showsConversation() {
		return m.conversationLines(width, height)
	}
	var lines []contentLine
	loader := m.startupLoader(width, height)
	pane := paneExact(m.preview, height, width)
	if len(pane) == 0 {
		// No rows painted means nothing to hit-test: a box left over from
		// the previous session would catch clicks on empty space.
		m.pane.box = paneBox{}
		if loader != nil {
			for _, line := range loader {
				lines = append(lines, contentLine{text: previewLine(line, width), raw: true})
			}
			return lines
		}
		return append(lines, contentLine{text: gutter + mutedStyle.Render("(no output yet)")})
	}
	// Record where these rows land so mouse hit-testing reads the same
	// geometry the paint used.
	m.pane.box = paneBox{
		x:      m.paneOriginX(),
		y:      m.listChromeRows() + m.previewBodyOffset,
		width:  width,
		height: len(pane),
		ok:     true,
	}
	for i, line := range pane {
		if i < len(loader) && loader[i] != "" {
			lines = append(lines, contentLine{text: previewLine(loader[i], width), raw: true})
			continue
		}
		lines = append(lines, contentLine{text: m.renderPaneRow(i, line, width), raw: true})
	}
	// Rows past the capture stay raw too: a painted tail under unpainted
	// output would read as a box drawn around the agent's last line.
	for len(lines) < height {
		lines = append(lines, contentLine{raw: true})
	}
	return lines
}

// detailLabelWidth is the column every fact label in the content head is
// padded to, so the values under it line up as one column.
const detailLabelWidth = 7

// trimmedValue is a rail fact value cut to the columns it gets, for readings
// that grow with the fleet rather than with the terminal.
func trimmedValue(value string) func(int) string {
	return func(room int) string { return cellTruncate(value, room, "…") }
}

// fitColumns lays the richest pair of readings that fits: rights are tried
// from richest to plainest, and for each, the lefts in turn. Nothing fitting
// means the plainest left is trimmed to what the column has.
func fitColumns(lefts, rights []string, width int) string {
	for _, right := range rights {
		for _, left := range lefts {
			if cellWidth(left)+cellWidth(right)+2 <= width {
				return rowColumns(left, right, width)
			}
		}
	}
	// Nothing fits whole, so the plainest left is trimmed to keep the richest
	// reading that still leaves it something readable.
	last := lefts[len(lefts)-1]
	for _, right := range rights {
		if room := width - cellWidth(right) - 2; room >= 8 {
			return rowColumns(cellTruncate(last, room, "…"), right, width)
		}
	}
	return rowColumns(cellTruncate(last, max(width, 1), "…"), "", width)
}

// rosterToolColumn is the column a roster's tool names start at, so the
// roster reads as a table rather than as ragged pairs, and rosterNameMin
// the width under which a name column stops being worth reading.
const (
	rosterToolColumn = 14
	rosterNameMin    = 8
)

// viewGroupAgents lists a group's sessions where a session's pane preview
// would sit, so a group reads as a roster: one row per agent, its name, the
// CLI running it, and what it is doing.
func (m *Model) viewGroupAgents(group string, width, height int) string {
	total := m.groupSessionCount(group)
	if total == 0 {
		return subtleStyle.Render("agents") + "\n" +
			mutedStyle.Render("(none yet — press space to spawn one)")
	}

	type rosterRow struct{ name, tool, state string }
	var rows []rosterRow
	overflow := ""
	shown := 0
	for _, sess := range m.listedAgents() {
		if !inGroupSubtree(sess.Group, group) {
			continue
		}
		if shown >= height-2 && total > shown+1 {
			overflow = subtleStyle.Render(fmt.Sprintf("… %d more", total-shown))
			break
		}
		rows = append(rows, rosterRow{
			name:  statusTint(sess.Status, statusGlyph(sess.Status)) + " " + valueStyle.Render(m.displayName(sess)),
			tool:  subtleText(sess.Tool),
			state: statusTint(sess.Status, statusLabel(sess.Status)) + subtleText(" · "+relSince(lastActivity(sess))),
		})
		shown++
	}

	// The name column is as wide as the longest name allows, bounded by what
	// the tool and state columns need, so tools land on one column and the
	// states share a right edge. A column too narrow for all three gives up
	// the tool first and the state second: a roster of names still answers
	// "who is in this group".
	nameWidth, toolWidth, stateWidth := rosterToolColumn, 0, 0
	for _, row := range rows {
		if w := cellWidth(row.name) + 2; w > nameWidth {
			nameWidth = w
		}
		if w := cellWidth(row.tool); w > toolWidth {
			toolWidth = w
		}
		if w := cellWidth(row.state); w > stateWidth {
			stateWidth = w
		}
	}
	showTool, showState := true, true
	if width-toolWidth-stateWidth-3 < rosterNameMin {
		showTool, toolWidth = false, 0
	}
	if width-stateWidth-2 < rosterNameMin {
		showState, stateWidth = false, 0
	}
	if room := width - toolWidth - stateWidth - 3; nameWidth > room {
		nameWidth = max(room, rosterNameMin)
	}

	head := padRight(subtleStyle.Render("agent"), nameWidth)
	if showTool {
		head += subtleStyle.Render("tool")
	}
	activity := ""
	if showState {
		activity = subtleStyle.Render("last activity")
	}
	lines := []string{rowColumns(head, activity, width)}
	for _, row := range rows {
		// A name trimmed to the column exactly would touch the tool beside
		// it, so the trim leaves the column's last cell as the gap.
		line := padRight(cellTruncate(row.name, max(nameWidth-1, 1), "…"), nameWidth)
		if showTool {
			line += row.tool
		}
		state := row.state
		if !showState {
			state = ""
		}
		lines = append(lines, rowColumns(line, state, width))
	}
	if overflow != "" {
		lines = append(lines, overflow)
	}
	return strings.Join(lines, "\n")
}

// lastActivity is when a session last changed state: the agent answering,
// finishing, erroring, or the moment a prompt set it working. It is what
// "how long since anything happened here" means to someone scanning the
// rail, where uptime says nothing about whether an agent is stuck.
func lastActivity(sess store.Session) time.Time {
	if sess.LastStatusAt.IsZero() {
		return sess.CreatedAt
	}
	return sess.LastStatusAt
}

// viewQuickBar is the docked prompt: enter answers the selected session, or
// spawns a fresh agent when a group is selected.
func (m *Model) viewQuickBar(width int) string {
	label := func(text string) string { return labelStyle.Render(padRight(text, detailLabelWidth)) }
	target := rowColumns(label("target")+mutedStyle.Render("no selection"), "", width)
	if entry, ok := m.selectedRow(); ok {
		if entry.isGroup {
			// Spawning: the tool decides what gets created, so it sits
			// where the eye lands before typing.
			tool := chipStyle.Render(m.quickTool())
			target = fitColumns(
				[]string{label("new") + lipgloss.NewStyle().Foreground(colorAccent2).Render(displayGroup(entry.group))},
				[]string{tool, ""}, width)
		} else {
			sess := entry.sess
			state := lipgloss.NewStyle().Foreground(statusColor(sess.Status)).
				Render(statusGlyph(sess.Status) + " " + statusLabel(sess.Status))
			target = fitColumns(
				[]string{label("answer") + lipgloss.NewStyle().Foreground(colorBright).Bold(true).Render(m.displayName(sess))},
				[]string{state + " " + chipStyle.Render(sess.Tool), state, ""}, width)
		}
	}
	m.quick.input.SetWidth(width)
	rows := m.quickBarRows(width - 2)
	if m.mode == modeFocus {
		rows = min(rows, max(1, m.listBodyHeight()-6))
	}
	m.quick.input.SetHeight(rows)
	// Chips are tokens inside the typed text, so they wrap and reflow with
	// the words around them; painting happens on the rendered prompt.
	bar := target + "\n" + m.quick.renderChips(m.quick.input.View())
	if line := m.quickSnippetLine(width); line != "" {
		bar += "\n" + line
	}
	return bar
}

// quickSnippetLine offers the operator's snippets under the prompt: the
// messages already on a key, so a sentence that has one is not typed again.
//
// One line, and only when a session is selected. On a group the bar spawns
// rather than answers, and a snippet has no pane to reach there -- listing
// them would be offering keys that refuse. The line is truncated rather than
// wrapped: the dock's height is measured from what this returns, so a set of
// snippets long enough to wrap would push the live pane down by however many
// the operator happened to define.
func (m *Model) quickSnippetLine(width int) string {
	entry, ok := m.selectedRow()
	if !ok || entry.isGroup {
		return ""
	}
	rows := m.snippetQuickRows()
	if len(rows) == 0 {
		return ""
	}
	// Joined and cut as plain text, then styled once. Truncating a string
	// that already carries escape sequences cuts them mid-sequence.
	return subtleStyle.Render(truncateTail(strings.Join(rows, "  ·  "), width))
}

// archiveTimeLeft is how long an archived row has before the retention sweep
// deletes it, empty for a row that is not on that clock. Rounded up and
// coarse on purpose: the number is there to say whether a session is about
// to go, and "6d" answers that as well as an exact duration would.
func archiveTimeLeft(sess store.Session) string {
	if !sess.Archived || sess.ArchivedAt.IsZero() {
		return ""
	}
	// Concatenation rather than Sprintf: this runs for every archived row on
	// every frame the archived view paints, and the format verb is a single
	// integer either way.
	left := time.Until(sess.ArchivedAt.Add(archiveRetention))
	switch {
	case left <= 0:
		return "due to go"
	case left < time.Hour:
		return strconv.Itoa(int(left.Minutes())+1) + "m left"
	case left < 24*time.Hour:
		return strconv.Itoa(int(left.Hours())+1) + "h left"
	default:
		return strconv.Itoa(int(left.Hours()/24)+1) + "d left"
	}
}
