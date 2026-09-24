// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/usestring/gate-inbox/extension/textfmt"
)

func (m *Model) cardWidth() int {
	width := 64
	if m.width >= 28 && width > m.width-4 {
		width = m.width - 4
	}
	return width
}

const (
	cardPaddingX = 3
	// cardChromeX is the two border columns a card spends on its frame.
	cardChromeX = 2
)

// cardBorderStyle is the card's frame: the theme's border tone pulled toward
// the accent, so a dialog reads as the app's own surface rather than as a
// box drawn around it.
func cardBorderStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(mix(current.Border, current.Accent, 0.35)))
}

// card floats a modal on the app backdrop: a framed panel with its title set
// into the top edge and its keys on a foot below a hairline.
func (m *Model) card(title, body string, hint [][2]string) string {
	return m.cardSized(m.cardWidth(), title, body, hint)
}

// cardFlex is card, but the panel grows with its content up to the terminal
// width so long settings rows are not clipped.
func (m *Model) cardFlex(title, body string, hint [][2]string) string {
	return m.cardSized(m.flexCardWidth(title, body, hint), title, body, hint)
}

func cardInnerWidth(width int) int { return width - cardChromeX - 2*cardPaddingX }

// flexCardWidth picks a width that fits every content line, never under the
// default card width and never past the terminal edge.
func (m *Model) flexCardWidth(title, body string, hint [][2]string) int {
	need := m.cardWidth()
	measure := func(s string) {
		for _, line := range strings.Split(s, "\n") {
			if w := lipgloss.Width(line) + cardChromeX + 2*cardPaddingX; w > need {
				need = w
			}
		}
	}
	measure(body)
	measure(cardTitle(title))
	measure(legendInline(hint, 1<<30))
	if m.errBar.text != "" {
		measure(m.statusMessage("▲", "●"))
	}
	if m.width >= 28 && need > m.width-4 {
		need = m.width - 4
	}
	return need
}

func cardTitle(title string) string {
	return lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(title)
}

// cardSized is card at an explicit width, for the key map, whose lines are
// too long to read inside the default column.
func (m *Model) cardSized(width int, title, body string, hint [][2]string) string {
	inner := cardInnerWidth(width)
	border := cardBorderStyle()
	pad := spaces(cardPaddingX)
	edge := border.Render("│")

	row := func(line string) string {
		return paint(edge+pad+padRight(line, inner)+pad+edge, width, blockHex())
	}
	rule := func(left, right string) string {
		return paint(border.Render(left+strings.Repeat("─", width-2)+right), width, blockHex())
	}

	lines := []string{cardTitleRow(width, title, border), row("")}
	for _, line := range strings.Split(body, "\n") {
		lines = append(lines, row(line))
	}
	if m.errBar.text != "" {
		lines = append(lines, row(""), row(m.statusMessage("▲", "●")))
	}
	lines = append(lines, row(""))
	if len(hint) > 0 {
		lines = append(lines, rule("├", "┤"))
		for _, line := range strings.Split(legendInline(hint, inner), "\n") {
			lines = append(lines, row(line))
		}
	}
	lines = append(lines, rule("╰", "╯"))
	return m.centerOnBackdrop(lines)
}

// cardTitleRow sets the title into the top edge, so the frame names the
// dialog instead of spending a content row on it.
func cardTitleRow(width int, title string, border lipgloss.Style) string {
	label := " " + cardTitle(title) + " "
	dashes := width - 4 - lipgloss.Width(label)
	if dashes < 0 {
		dashes = 0
	}
	head := border.Render("╭──") + label + border.Render(strings.Repeat("─", dashes)+"╮")
	return paint(head, width, blockHex())
}

// centerOnBackdrop floats a block of pre-painted lines in the middle of
// the app frame, filling the rest with the backdrop.
func (m *Model) centerOnBackdrop(box []string) string {
	width := maxLineWidth(box)
	height := max(m.height, len(box))
	left := max((m.width-width)/2, 0)
	frameWidth := max(m.width, left+width)
	top := max((height-len(box))/2, 0)
	frame := make([]string, 0, height)
	for i := 0; i < height; i++ {
		row := ""
		if i >= top && i-top < len(box) {
			row = paint("", left, backdropHex()) + box[i-top]
		}
		frame = append(frame, paint(row, frameWidth, backdropHex()))
	}
	return strings.Join(frame, "\n")
}

func maxLineWidth(lines []string) int {
	width := 0
	for _, line := range lines {
		if w := lipgloss.Width(line); w > width {
			width = w
		}
	}
	return width
}

// fitBody returns a scrollable window without letting a short terminal eat
// the modal border or hints. Continuation rows make hidden content explicit.
func fitBody(body []string, room, offset int) []string {
	if room < 1 {
		room = 1
	}
	if room >= len(body) {
		return body
	}
	maxOffset := max(0, len(body)-room)
	offset = min(max(offset, 0), maxOffset)
	window := append([]string(nil), body[offset:min(offset+room, len(body))]...)
	above := offset > 0
	below := offset+room < len(body)
	if len(window) == 1 && above && below {
		window[0] = subtleStyle.Render("↕ more…")
		return window
	}
	if above {
		window[0] = subtleStyle.Render("↑ more above…")
	}
	if below {
		window[len(window)-1] = subtleStyle.Render("↓ more below…")
	}
	return window
}

func (m *Model) viewForm() string {
	m.form.prompt.input.SetHeight(textareaRows(m.form.prompt.input, m.formValueWidth()-2, formPromptMaxRows))

	var b strings.Builder
	b.WriteString(formField("tool", m.viewToolField(), m.form.focus == fieldTool))
	b.WriteString(formField("model", m.form.model.View(), m.form.focus == fieldModel))
	b.WriteString(formField("name", m.form.name.View(), m.form.focus == fieldName))
	// Chips are tokens inside the typed text, so they wrap and reflow with
	// the words around them; painting happens on the rendered prompt.
	b.WriteString(formField("prompt", m.form.prompt.renderChips(m.form.prompt.input.View()), m.form.focus == fieldPrompt))
	b.WriteString(formField("group", groupBadge(displayGroup(m.form.groups[m.form.groupIndex].path)), m.form.focus == fieldGroup))

	// The picker trims its own trailing newline, so the break belongs here:
	// without it the dir field lands on the end of the last group row.
	if m.form.focus == fieldGroup {
		b.WriteString("\n" + m.viewGroupPicker() + "\n")
	}

	b.WriteString(formField("dir", m.form.dir.View(), m.form.focus == fieldDir))
	if m.form.focus == fieldDir && m.pathSugg.showing() {
		b.WriteString(m.viewPathSuggestions() + "\n")
	}

	hint := [][2]string{{"tab/↑↓", "move"}, {"←→", "change"}, {"↵", "create"}, {"esc", "cancel"}}
	if m.form.focus == fieldTool {
		hint = [][2]string{{"type", "pick a CLI"}, {"←→", "change"}, {"tab/↑↓", "move"}, {"↵", "create"}}
	}
	if m.form.focus == fieldPrompt {
		hint = [][2]string{{"ctrl+v", "paste an image"}, {"tab/↑↓", "move"}, {"↵", "create"}, {"esc", "cancel"}}
	}
	if m.form.focus == fieldGroup {
		hint = [][2]string{{"←→", "pick group"}, {"tab/↑↓", "move"}, {"↵", "create"}, {"esc", "cancel"}}
	}
	if m.form.focus == fieldDir && m.pathSugg.showing() {
		hint = pathSuggestHint(m.pathSugg)
	}
	return m.card("◆ New Session", strings.TrimRight(b.String(), "\n"), hint)
}

// viewToolField renders the CLI picker: the selection between its arrows,
// then the filter the operator is typing and the CLIs it still allows. The
// alternatives are shown rather than hidden because the field is now the
// first thing the card focuses, and a picker whose other values are invisible
// reads as a label.
func (m *Model) viewToolField() string {
	if len(m.form.toolNames) == 0 {
		return subtleStyle.Render("(none configured)")
	}
	field := subtleStyle.Render("\u25c2 ") + valueStyle.Render(m.selectedToolName()) + subtleStyle.Render(" \u25b8")
	if m.form.focus != fieldTool {
		return field
	}
	field += "  " + m.form.toolFilter.View()
	matches := m.formToolMatches()
	if len(matches) == 0 {
		return field + "  " + mutedStyle.Render("(no CLI matches)")
	}
	// The alternatives are only worth showing while they fit on the row:
	// the card does not wrap a field value, so a long list would run past
	// the border instead of informing anyone. Whatever is cut is still one
	// arrow key away.
	rest := fitNames(matches, m.selectedToolName(), m.formValueWidth()-lipgloss.Width(field)-2)
	if rest == "" {
		return field
	}
	return field + "  " + mutedStyle.Render(rest)
}

func groupBadge(path string) string {
	return lipgloss.NewStyle().Foreground(colorAccent2).Render(path)
}

func pathSuggestHint(pc pathComplete) [][2]string {
	switch {
	case pc.browsing:
		return [][2]string{{"↑↓", "pick"}, {"←→", "out/in"}, {"↵", "use"}, {"esc", "close"}}
	case pc.auto:
		// The listing is only offered here: the keys that leave the field
		// still leave it, so the hint stays the form's own until ↓ enters.
		return [][2]string{{"↓", "browse"}, {"tab", "move"}, {"↵", "create"}, {"esc", "cancel"}}
	default:
		return [][2]string{{"↑↓", "pick"}, {"tab", "complete"}, {"↵", "create"}, {"esc", "close"}}
	}
}

// pathWindow is how many directories the dropdown shows at once. The listing
// behind it scrolls, so a deep tree is walked without the card growing.
const pathWindow = 6

// viewPathSuggestions renders the directory listing under a focused path
// field: the children of wherever the field points, as a window that follows
// the highlight.
func (m *Model) viewPathSuggestions() string {
	pc := m.pathSugg
	if !pc.active() {
		// Browsing a directory that holds no others. The walk is still on, so
		// it says so rather than vanishing and leaving ←→ unexplained.
		return "      " + mutedStyle.Render("(no directories here)")
	}
	start := 0
	if pc.index >= pathWindow {
		start = pc.index - pathWindow + 1
	}
	end := min(start+pathWindow, len(pc.suggestions))
	var b strings.Builder
	for i := start; i < end; i++ {
		marker := "  "
		style := mutedStyle
		// An offered listing highlights nothing: nothing is picked out of it
		// until an arrow key says so.
		if i == pc.index && pc.capturing() {
			marker = lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
			style = groupNameStyle
		}
		b.WriteString("      " + marker + style.Render(truncateTail(filepath.Base(pc.suggestions[i])+"/", 40)) + "\n")
	}
	if remaining := len(pc.suggestions) - end; remaining > 0 {
		b.WriteString("        " + subtleStyle.Render(fmt.Sprintf("+%d more", remaining)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) viewGroupPicker() string {
	var b strings.Builder
	for i, opt := range m.form.groups {
		selected := i == m.form.groupIndex
		marker := "  "
		if selected {
			marker = lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
		}
		label := displayGroup(opt.path)
		if opt.sessID != "" {
			label = strings.Repeat("  ", opt.depth) + opt.name
		} else if opt.path != "" {
			label = strings.Repeat("  ", opt.depth) + baseName(opt.path)
		}
		style := mutedStyle
		if selected {
			style = groupNameStyle
		}
		b.WriteString("  " + marker + style.Render(label) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) viewGroupForm() string {
	var b strings.Builder
	b.WriteString(formField("name", m.groupForm.name.View(), m.groupForm.focus == gfName))
	b.WriteString(formField("parent", groupBadge(displayGroup(m.selectedGroupPath())), m.groupForm.focus == gfParent))
	b.WriteString(formField("path", m.groupForm.path.View(), m.groupForm.focus == gfPath))
	if m.groupForm.focus == gfPath && m.pathSugg.showing() {
		b.WriteString(m.viewPathSuggestions() + "\n")
	}
	if m.groupForm.focus == gfParent {
		b.WriteString("\n" + m.viewGroupPicker())
	}
	title, submit := "✦ New Group", "create"
	if m.groupForm.editing != "" {
		title, submit = "✦ Edit Group", "save"
	}
	hint := [][2]string{{"tab/↑↓", "move"}, {"↵", submit}, {"esc", "cancel"}}
	if m.groupForm.focus == gfParent {
		hint = [][2]string{{"←→", "pick parent"}, {"tab/↑↓", "move"}, {"↵", submit}, {"esc", "cancel"}}
	}
	if m.groupForm.focus == gfPath && m.pathSugg.showing() {
		hint = pathSuggestHint(m.pathSugg)
	}
	return m.card(title, strings.TrimRight(b.String(), "\n"), hint)
}

// settingsLabelColumn is the label width a card wide enough to afford it uses.
const settingsLabelColumn = 18

func (m *Model) viewSettings() string {
	if m.settings.cliPicker {
		return m.viewCLIPicker()
	}
	density := "compact"
	if m.settings.comfortableRows {
		density = "comfortable"
	}
	quickClose := "stay open"
	if m.settings.quickCloseSend {
		quickClose = "close"
	}
	focusKey := "↵ focus · A attach"
	if !m.settings.enterFocuses {
		focusKey = "↵ attach · A focus"
	}
	backdrop := "inherit"
	if m.settings.backdropSync {
		backdrop = "match theme"
	}
	if current.Name == "oled" {
		// OLED paints the whole terminal black whatever this says; see
		// backdropSyncing.
		backdrop = "black (oled)"
	}
	autoProceed := "off"
	if m.settings.autoProceed {
		autoProceed = "on"
	}
	toolValue := ""
	if len(m.settings.toolNames) > 0 {
		toolValue = m.settings.toolNames[m.settings.toolIndex]
	}
	// The label column yields on a narrow card. Held at its full width it
	// leaves too little for the value, and the value is the part that says
	// what the setting currently is.
	labelColumn := settingsLabelColumn
	if room := cardInnerWidth(m.cardWidth()) / 2; labelColumn > room {
		labelColumn = max(room, 8)
	}
	lead := func(field int, name string) string {
		marker := "  "
		labelStyle := valueStyle
		if m.settings.field == field {
			marker = lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
			labelStyle = annotationStyle
		}
		// Truncate a column short so a cut label keeps a gap before the value.
		return marker + padRight(labelStyle.Render(textfmt.TruncateWidth(name, labelColumn-1, "…")), labelColumn)
	}
	row := func(field int, name, value string) string {
		return lead(field, name) + subtleStyle.Render("◂ ") + valueStyle.Render(value) + subtleStyle.Render(" ▸")
	}
	// An action row: enter runs it, so it carries no picker arrows.
	actionRow := func(field int, name, action string) string {
		return lead(field, name) + keyStyle.Render("↵") + mutedStyle.Render(" "+action)
	}
	themeScope := "shared default"
	if m.themeDevice != "" {
		themeScope = m.themeDevice
	}
	body := row(settingsFieldTool, "default tool", toolValue) + "\n" +
		row(settingsFieldAccountRouting, "routing mode", m.settings.routingValue()) + "\n" +
		row(settingsFieldNewSessionAgent, "new session agent", normalizeNewSessionAgent(m.settings.newSessionAgent)) + "\n" +
		row(settingsFieldTheme, "theme", themes[m.settings.themeIndex].Name) + "  " +
		themeSwatch(themes[m.settings.themeIndex]) + "  " + mutedStyle.Render(themeScope) + "\n" +
		row(settingsFieldBackdrop, "terminal background", backdrop) + "\n" +
		row(settingsFieldDensity, "list density", density) + "\n" +
		row(settingsFieldLayout, "layout", normalizeLayout(m.settings.layout)) + "\n" +
		row(settingsFieldPalette, "colour", normalizePalette(m.settings.palette)) + "\n" +
		row(settingsFieldGlyphs, "status marks", normalizeGlyphs(m.settings.glyphs)+"  "+statusGlyph("waiting")+statusGlyph("finished")+statusGlyph("errored")) + "\n" +
		row(settingsFieldArchiveConfirm, "ask before ending", normalizeArchiveConfirm(m.settings.archiveConfirm)) + "\n" +
		row(settingsFieldListSort, "sort", normalizeListSort(m.settings.listSort)) + "\n" +
		row(settingsFieldChrome, "key hints", normalizeChrome(m.settings.chrome)) + "\n" +
		row(settingsFieldLeave, "on leaving a session", normalizeLeaveMode(m.settings.leaveMode)) + "\n" +
		row(settingsFieldQuickClose, "after quick send", quickClose) + "\n" +
		row(settingsFieldFocusKey, "session keys", focusKey) + "\n" +
		row(settingsFieldAutoProceed, "triage auto proceed", autoProceed) + "\n" +
		actionRow(settingsFieldSnippets, "snippets", "edit quick replies") + "\n" +
		actionRow(settingsFieldCLIs, "CLIs", "show or hide for new sessions") + "\n" +
		actionRow(settingsFieldGuide, "welcome guide", "read the first-run introduction again") + "\n" +
		m.settingsVersionRow(lead)
	hint := [][2]string{{"↑↓", "field"}, {"←→", "change"}, {"↵/esc", "save"}}
	switch m.settings.field {
	case settingsFieldSnippets:
		hint = [][2]string{{"↑↓", "field"}, {"↵", "edit snippets"}, {"esc", "save"}}
	case settingsFieldCLIs:
		hint = [][2]string{{"↑↓", "field"}, {"↵", "manage CLIs"}, {"esc", "save"}}
	case settingsFieldGuide:
		hint = [][2]string{{"↑↓", "field"}, {"↵", "show the guide"}, {"esc", "save"}}
	}
	return m.cardFlex("▣ Settings", body, hint)
}

// settingsVersionRow reports the running build. It carries no action: this
// binary is built from a branch, so there is nothing to update to.
func (m *Model) settingsVersionRow(lead func(int, string) string) string {
	return lead(settingsFieldVersion, "version") + valueStyle.Render(m.version)
}

func (m *Model) viewCLIPicker() string {
	var b strings.Builder
	for i, name := range m.settings.cliNames {
		marker := "  "
		labelStyle := valueStyle
		if m.settings.cliCursor == i {
			marker = lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
			labelStyle = annotationStyle
		}
		box := "[x]"
		if m.settings.cliHidden[name] {
			box = "[ ]"
		}
		b.WriteString(marker)
		b.WriteString(labelStyle.Render(box + " " + name))
		b.WriteByte('\n')
	}
	hint := [][2]string{{"↑↓", "move"}, {"space/↵", "toggle"}, {"esc", "back"}}
	// Fixed card width: short checkbox rows must not stretch a wide empty panel.
	return m.card("▣ CLIs", strings.TrimRight(b.String(), "\n"), hint)
}

// themeSwatch previews a palette as a run of blocks, so a theme can be
// picked by eye rather than by name.
func themeSwatch(t Theme) string {
	var b strings.Builder
	for _, hex := range []string{t.Accent, t.Accent2, t.Working, t.Waiting, t.Finished, t.Errored} {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render("█"))
	}
	return b.String()
}

func (m *Model) viewMove() string {
	return m.card("⇄ Move", m.viewGroupPicker(),
		[][2]string{{"↑↓", "pick"}, {"↵", "move"}, {"esc", "cancel"}})
}

func formField(label, value string, focused bool) string {
	return formFieldAt(label, value, focused, formLabelWidth)
}

// formFieldAt is formField with the label column named, for a card whose
// labels do not fit the nine columns every other card's do.
//
// The width has to be given rather than measured per label, because the
// values line up on it: a column sized to each row in turn would step in and
// out down the card. A label longer than the column wraps mid-word, which is
// what this exists to let a card avoid.
func formFieldAt(label, value string, focused bool, labelWidth int) string {
	marker := "  "
	style := labelStyle
	if focused {
		marker = lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
		style = annotationStyle
	}
	lines := strings.Split(value, "\n")
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s%s %s\n", marker, style.Width(labelWidth).Render(label), lines[0]))
	for _, line := range lines[1:] {
		b.WriteString(spaces(formMarkerWidth+labelWidth+1) + line + "\n")
	}
	return b.String()
}

// viewAgentPick draws the one-question card n opens: the box, holding the CLI
// the last spawn used, and under it the CLIs still matching what has been
// typed over it, with the one enter would start picked out.
//
// The matches are drawn rather than hidden behind the arrows, because the box
// opens prefilled and a prefilled field with no visible alternatives reads as
// a label rather than as a question. The selection is drawn among them because
// once the box holds a filter rather than a name -- "co" -- nothing else on
// the card says which CLI that resolved to.
func (m *Model) viewAgentPick() string {
	if len(m.agentPick.names) == 0 {
		return m.card("◆ New Session", subtleStyle.Render("(no CLIs enabled)"),
			[][2]string{{"esc", "cancel"}})
	}
	inner := m.formValueWidth()
	// Inputs reserve the cursor cell that renders past the last character,
	// and textinput recomputes its scroll window only inside Update, SetValue
	// or SetCursor -- so the width is followed by a cursor set, as the form's
	// own fields are.
	m.agentPick.input.SetWidth(max(4, inner-3))
	m.agentPick.input.SetCursor(m.agentPick.input.Position())

	var b strings.Builder
	b.WriteString(formField("agent", m.agentPick.input.View(), true))
	matches := m.agentPickMatches()
	if len(matches) == 0 {
		b.WriteString(spaces(formLabelColumn) + mutedStyle.Render("(no CLI matches)") + "\n")
	} else {
		b.WriteString(spaces(formLabelColumn) + agentPickRow(matches, m.agentPickName(), inner) + "\n")
	}
	return m.card("◆ New Session", strings.TrimRight(b.String(), "\n"),
		[][2]string{{"type", "pick a CLI"}, {"←→", "change"}, {"↵", "start"}, {"esc", "cancel"}})
}

// agentPickRow lays the matching CLIs on one line, the selected one in the
// value tone and the rest muted, cut to what budget columns hold.
//
// The card does not wrap a field value, so a long list would run past the
// border instead of informing anyone; whatever is cut is still one arrow key
// away. The selected name is measured first so it is the one thing the cut can
// never take -- a row that dropped it would leave the card silent about what
// enter starts.
func agentPickRow(names []string, selected string, budget int) string {
	shown := make(map[string]bool, len(names))
	width := 0
	for _, name := range names {
		if name == selected {
			width += lipgloss.Width(name)
			shown[name] = true
		}
	}
	for _, name := range names {
		if shown[name] {
			continue
		}
		w := lipgloss.Width(name)
		if len(shown) > 0 {
			w += 3
		}
		if width+w > budget {
			break
		}
		width += w
		shown[name] = true
	}
	parts := make([]string, 0, len(shown))
	for _, name := range names {
		switch {
		case !shown[name]:
		case name == selected:
			parts = append(parts, valueStyle.Render(name))
		default:
			parts = append(parts, mutedStyle.Render(name))
		}
	}
	return strings.Join(parts, mutedStyle.Render(" \u00b7 "))
}

// fitNames joins the names other than the selected one for as far as budget
// columns allow, and returns them empty when there is nothing left to show.
func fitNames(names []string, selected string, budget int) string {
	rest := make([]string, 0, len(names))
	width := 0
	for _, name := range names {
		if name == selected {
			continue
		}
		width += lipgloss.Width(name)
		if len(rest) > 0 {
			width += 3
		}
		if width > budget {
			break
		}
		rest = append(rest, name)
	}
	return strings.Join(rest, " \u00b7 ")
}
