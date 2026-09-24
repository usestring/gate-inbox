// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"github.com/usestring/gate-inbox/internal/keymap"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The key map is the one place every binding in the app is written down, so
// it is grouped the way the keys are learned - by what is under the cursor
// or which screen is up - rather than listed alphabetically. Long enough to
// outgrow a terminal, it scrolls, and a search narrows it to the one line
// the reader came for.

type helpState struct {
	scroll     int
	query      string
	searching  bool
	returnMode mode
	// cursor is which binding the screen is on, as an index into the rows a
	// search leaves visible. The key map is where a binding is changed now,
	// so it needs a selection: capturing is set while it is waiting for the
	// key to bind, and notes carries what the last rebind had to say.
	cursor    int
	capturing bool
	// follow is set when the cursor moved and the page has to catch up. Off,
	// the page is what moved and the cursor catches up instead.
	follow bool
	notes  []string
}

// helpKeyColumn is the width the key column is padded to: the widest key in
// the whole catalog plus a gap. Measured rather than fixed, so a binding
// added later cannot render clipped against its own description, and always
// over the whole catalog rather than a search's hits, so narrowing the map
// does not shift the column under the reader. Over the live map rather than
// the defaults, because a chord bound today is wider than the letter it
// replaced.
func (m *Model) helpKeyColumn() int {
	width := 0
	for _, section := range m.resolvedHelp() {
		for _, row := range section.rows {
			if w := cellWidth(row.key); w > width {
				width = w
			}
		}
	}
	return width + 2
}

// helpCardMaxWidth keeps the card readable on a wide terminal: past this the
// eye has to travel too far from a key to its description.
const helpCardMaxWidth = 92

// matchHelp narrows the catalog to the rows whose key or description
// contains the query, dropping the sections left with nothing. A section
// whose own title matches keeps all of its rows: "the name sweep" should
// answer with that screen, not with the rows that happen to spell the word.
func matchHelp(sections []resolvedSection, query string) []resolvedSection {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return sections
	}
	var kept []resolvedSection
	for _, section := range sections {
		if strings.Contains(strings.ToLower(section.title), query) {
			kept = append(kept, section)
			continue
		}
		var rows []resolvedRow
		for _, row := range section.rows {
			if strings.Contains(strings.ToLower(row.key), query) ||
				strings.Contains(strings.ToLower(row.text), query) ||
				strings.Contains(strings.ToLower(string(row.action)), query) {
				rows = append(rows, row)
			}
		}
		if len(rows) > 0 {
			kept = append(kept, resolvedSection{title: section.title, rows: rows})
		}
	}
	return kept
}

func helpRowCount(sections []resolvedSection) int {
	count := 0
	for _, section := range sections {
		for _, row := range section.rows {
			if row.key != "" {
				count++
			}
		}
	}
	return count
}

// helpBodyLines lays the catalog out as one scrollable column: a titled rule
// per section, then its bindings in two aligned columns.
//
// anchors comes back beside the lines: for each rebindable row, in order,
// the line its key landed on. That is what lets the cursor move by binding
// rather than by line -- a row whose description wrapped over three lines is
// one stop, not three -- and what tells the scroll where the cursor is.
func (m *Model) helpBodyLines(sections []resolvedSection, width int, query string) ([]string, []int) {
	keyColumn := m.helpKeyColumn()
	if room := width / 3; keyColumn > room {
		keyColumn = max(room, 4)
	}
	var lines []string
	var anchors []int
	for i, section := range sections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, divider(section.title, width))
		for _, row := range section.rows {
			// A row with no key is a note about the one above it, so it
			// recedes into the description column instead of claiming a
			// binding of its own.
			indent := spaces(keyColumn)
			room := max(width-keyColumn, 1)
			if row.key == "" && !row.rebindable {
				for _, line := range wrapDescription(row.text, room) {
					lines = append(lines, indent+subtleStyle.Render(line))
				}
				continue
			}
			selected := false
			if row.rebindable {
				selected = len(anchors) == m.help.cursor
				anchors = append(anchors, len(lines))
			}
			cap, text := row.key, row.text
			if cap == "" {
				// An action the operator unbound keeps its row: it is the
				// only place left that can offer the key back.
				cap = "—"
			}
			if selected && m.help.capturing {
				text = "press the key to bind — esc cancels"
			}
			// The description wraps rather than truncating: on a narrow card
			// the half that gets cut is the half that says what the key does.
			style := keyStyle
			if !row.available {
				style = subtleStyle
			}
			key := padRight(style.Render(cap), keyColumn)
			if selected {
				// Padded before it is rendered, not after: the selected row
				// is a band across the card, and padding a rendered cap
				// leaves the band starting mid-row at the description.
				style = selectedKeyStyle
				key = style.Render(padRight(cap, keyColumn))
			}
			for i, line := range wrapDescription(text, room) {
				prefix := key
				if i > 0 {
					prefix = indent
				}
				body := highlightMatch(line, query, room)
				if !row.available {
					body = subtleStyle.Render(cellTruncate(line, max(room, 1), "…"))
				}
				if selected {
					body = selectedTextStyle.Render(cellTruncate(line, max(room, 1), "…"))
				}
				lines = append(lines, prefix+body)
			}
		}
	}
	return lines, anchors
}

// wrapDescription breaks a row's description to the column it has, keeping
// whole words where it can and splitting one that is longer than the column.
func wrapDescription(text string, width int) []string {
	return strings.Split(ansi.Wrap(text, max(width, 1), ""), "\n")
}

// highlightMatch renders a description with the matched run picked out, so
// a search lands the eye on the word it found instead of on the row.
func highlightMatch(text, query string, width int) string {
	text = cellTruncate(text, max(width, 1), "…")
	query = strings.TrimSpace(query)
	if query == "" {
		return mutedStyle.Render(text)
	}
	// Case folding can change a string's byte length, so the run to paint is
	// as long as the folded query, and a fold that moved the offsets past the
	// original drops the highlight instead of slicing out of range.
	folded := strings.ToLower(query)
	at := strings.Index(strings.ToLower(text), folded)
	if at < 0 || at+len(folded) > len(text) {
		return mutedStyle.Render(text)
	}
	hit := lipgloss.NewStyle().Foreground(colorBright).Bold(true)
	return mutedStyle.Render(text[:at]) + hit.Render(text[at:at+len(folded)]) +
		mutedStyle.Render(text[at+len(folded):])
}

func helpCardWidth(terminalWidth int) int {
	width := helpCardMaxWidth
	if terminalWidth >= 28 && width > terminalWidth-4 {
		width = terminalWidth - 4
	}
	return width
}

// helpBodyRoom is the rows of catalog the card can show: the frame's own
// chrome, the search line when one is up, and the error row come off the
// terminal's height first.
func (m *Model) helpBodyRoom() int {
	inner := cardInnerWidth(helpCardWidth(m.width))
	// Title, the blank under it, the blank above the rule, the rule itself,
	// the hint - which wraps on a narrow card - and the bottom rule.
	room := m.height - 5 - lipgloss.Height(legendInline(m.helpHint(), inner))
	if m.helpSearchActive() {
		room -= 2
	}
	if m.errBar.text != "" {
		room -= 2
	}
	// The notes under the body are drawn after it, so they come off the
	// body's own budget rather than off the bottom of the terminal.
	room -= len(m.help.notes) + len(m.keyProblems)
	return max(room, 1)
}

// resolvedRow is a help row with the key it is bound to now filled in.
type resolvedRow struct {
	key  string
	text string
	// ctx and action are set on a row the cursor can rebind; rebindable
	// says so without every reader having to know that an empty action is
	// what a prose row carries.
	ctx        keymap.Context
	action     keymap.Action
	rebindable bool
	available  bool
}

type resolvedSection struct {
	title string
	rows  []resolvedRow
}

// helpCatalog is the key map as this run shows it: the static sections, then
// the operator's own snippets.
func (m *Model) helpCatalog() []helpSection {
	return append(append(helpSections(), m.extensionHelpSections()...), m.snippetHelpSection())
}

// resolvedHelp is the catalog with every binding row asking the key map what
// it is bound to. This is the whole reason the screen prints live keys: no
// row holds one.
func (m *Model) resolvedHelp() []resolvedSection {
	catalog := m.helpCatalog()
	current := m.currentLegendRow()
	out := make([]resolvedSection, 0, len(catalog))
	for _, section := range catalog {
		rows := make([]resolvedRow, 0, len(section.rows))
		for _, row := range section.rows {
			if row.action == "" {
				rows = append(rows, resolvedRow{key: row.key, text: row.text})
				continue
			}
			rows = append(rows, resolvedRow{
				key:        keymap.Display(m.km().Key(row.ctx, row.action)),
				text:       row.text,
				ctx:        row.ctx,
				action:     row.action,
				rebindable: true,
				available:  m.applies(row.ctx, row.action, current),
			})
		}
		out = append(out, resolvedSection{title: section.title, rows: rows})
	}
	return out
}

// helpBindings is every rebindable row on screen, in the order the cursor
// walks them. Read off the same narrowed sections the body renders, so a
// search that hides a row also takes it out of the cursor's way.
func (m *Model) helpBindings() []resolvedRow {
	var rows []resolvedRow
	for _, section := range matchHelp(m.resolvedHelp(), m.help.query) {
		for _, row := range section.rows {
			if row.rebindable {
				rows = append(rows, row)
			}
		}
	}
	return rows
}

func (m *Model) helpSearchActive() bool {
	return m.help.searching || m.help.query != ""
}

func (m *Model) helpScrollLimit() int {
	sections := matchHelp(m.resolvedHelp(), m.help.query)
	body, _ := m.helpBodyLines(sections, cardInnerWidth(helpCardWidth(m.width)), m.help.query)
	return max(0, len(body)-m.helpBodyRoom())
}

func (m *Model) viewHelp() string {
	width := helpCardWidth(m.width)
	inner := cardInnerWidth(width)
	sections := matchHelp(m.resolvedHelp(), m.help.query)

	var head []string
	if m.helpSearchActive() {
		head = append(head, m.helpSearchLine(sections), "")
	}

	body, anchors := m.helpBodyLines(sections, inner, m.help.query)
	m.followHelpCursor(anchors, len(body))
	if len(body) == 0 {
		body = []string{subtleStyle.Render("no key matches that")}
	}
	lines := append(head, fitBody(body, m.helpBodyRoom(), m.help.scroll)...)
	// What the last rebind had to say, and anything keys.toml asked for and
	// did not get. Both belong on this screen and nowhere else: a refused
	// override is otherwise a key that does nothing with its explanation in
	// a process nobody can see.
	for _, note := range append(append([]string{}, m.help.notes...), m.keyProblems...) {
		lines = append(lines, subtleStyle.Render(cellTruncate("· "+note, inner, "…")))
	}

	return m.cardSized(width, m.helpTitleCap()+" Keys", strings.Join(lines, "\n"), m.helpHint())
}

// helpSearchLine is the search's own row: what was typed, and how much of
// the map still answers to it.
func (m *Model) helpSearchLine(sections []resolvedSection) string {
	line := keyStyle.Render("search ") + valueStyle.Render(m.help.query)
	if m.help.searching {
		line += lipgloss.NewStyle().Foreground(colorAccent).Render("▏")
	}
	count := helpRowCount(sections)
	label := " keys"
	if count == 1 {
		label = " key"
	}
	return line + subtleStyle.Render(fmt.Sprintf("   %d%s", count, label))
}

func (m *Model) helpHint() [][2]string {
	if m.help.capturing {
		return [][2]string{{"any key", "bind it"}, {"esc", "cancel"}}
	}
	if m.help.searching {
		return [][2]string{{"type", "search"}, {"↵", "done"}, {"↑↓", "scroll"}, {"esc", "clear"}}
	}
	if m.help.query != "" {
		return [][2]string{{m.navCap(keymap.ContextList), "select"}, {"↵", "rebind"}, {"r", "default"},
			{"esc", "clear search"}, {"q/H", "close"}}
	}
	return [][2]string{
		{m.navCap(keymap.ContextList), "select"}, {"↵", "rebind"}, {"r", "default"},
		{"pgup/pgdn", "page"}, {"/", "search"}, {"esc/q/H", "close"},
	}
}

// helpTitleCap is the key the card prints in its own title. It is read from
// the live map rather than written as a literal, so a rebind of Help -- or the
// H default this screen also answers to -- is named on the frame the reader is
// looking at instead of the key it had when the title was written.
func (m *Model) helpTitleCap() string {
	if cap := keymap.Display(m.km().Key(keymap.ContextList, keymap.Help)); cap != "" {
		return cap
	}
	return "?"
}

func (m *Model) openHelp() {
	m.help = helpState{returnMode: m.mode}
	m.mode = modeHelp
}

func (m *Model) closeHelp() {
	back := m.help.returnMode
	m.help = helpState{}
	m.mode = back
}

// followHelpCursor keeps the selected binding on screen and the cursor on a
// row that exists: a search narrows the list under it, and a rebind can move
// the row it is on.
func (m *Model) followHelpCursor(anchors []int, body int) {
	if len(anchors) == 0 {
		m.help.cursor = 0
		return
	}
	m.help.cursor = min(max(m.help.cursor, 0), len(anchors)-1)
	room := m.helpBodyRoom()
	line := anchors[m.help.cursor]
	if m.help.follow {
		// The cursor moved: the page goes to it.
		switch {
		case line < m.help.scroll:
			m.help.scroll = line
		case line >= m.help.scroll+room:
			m.help.scroll = line - room + 1
		}
		m.help.scroll = min(max(m.help.scroll, 0), max(0, body-room))
		m.help.follow = false
		return
	}
	// The page moved instead -- a page key, a search, a card that resized --
	// so the cursor goes to it. Leaving the selection off screen would leave
	// ↵ rebinding a row nobody is looking at.
	if line >= m.help.scroll && line < m.help.scroll+room {
		return
	}
	for i, anchor := range anchors {
		if anchor >= m.help.scroll && anchor < m.help.scroll+room {
			m.help.cursor = i
			return
		}
	}
}

// moveHelpCursor steps the selection over the bindings on screen.
func (m *Model) moveHelpCursor(delta int) {
	count := len(m.helpBindings())
	if count == 0 {
		return
	}
	m.help.cursor = min(max(m.help.cursor+delta, 0), count-1)
	m.help.follow = true
	m.help.notes = nil
}

func (m *Model) scrollHelp(delta int) {
	m.help.scroll = min(max(m.help.scroll+delta, 0), m.helpScrollLimit())
}

// helpPage is how far a page key scrolls: the rows fitBody leaves showing
// between its "more above" and "more below" markers, less one kept for
// context. A step of room-1 put one row under a marker at every boundary,
// and a binding that landed there was on no page at all.
func (m *Model) helpPage() int {
	return max(m.helpBodyRoom()-3, 1)
}

func (m *Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.help.searching {
		return m.handleHelpSearchKey(msg)
	}
	// While the screen is waiting for a key to bind, every key is that key.
	// Nothing below may claim one: the whole point is that any key can be
	// bound, esc included -- which is why esc is read first and means cancel
	// rather than binding itself.
	if m.help.capturing {
		return m.captureRebind(msg)
	}
	switch msg.String() {
	case "esc":
		if m.help.query != "" {
			m.help.query = ""
			m.help.scroll = 0
			return m, nil
		}
		m.closeHelp()
		return m, m.startStartupTick()
	case "q", "?", "H", "shift+h":
		m.closeHelp()
		return m, m.startStartupTick()
	case "enter":
		return m.armRebind()
	case "r":
		return m.resetSelectedBinding()
	case "/":
		m.help.searching = true
	case "up", "k":
		m.moveHelpCursor(-1)
	case "down", "j":
		m.moveHelpCursor(1)
	case "pgup", "ctrl+u":
		m.scrollHelp(-m.helpPage())
	case "pgdown", "ctrl+d":
		m.scrollHelp(m.helpPage())
	case "g", "home":
		m.help.scroll = 0
	case "G", "end":
		m.help.scroll = m.helpScrollLimit()
	}
	return m, nil
}

// selectedBinding is the row the rebind cursor is on.
func (m *Model) selectedBinding() (resolvedRow, bool) {
	rows := m.helpBindings()
	if m.help.cursor < 0 || m.help.cursor >= len(rows) {
		return resolvedRow{}, false
	}
	return rows[m.help.cursor], true
}

// armRebind puts the screen into capture: the next key pressed becomes the
// selected action's key. It is ↵ because ↵ is what opens the thing under a
// cursor everywhere else here; the key that used to close this screen is now
// q, esc and ? -- three ways out, which is two more than it needs.
func (m *Model) armRebind() (tea.Model, tea.Cmd) {
	row, ok := m.selectedBinding()
	if !ok {
		m.help.notes = []string{"nothing here to rebind"}
		return m, nil
	}
	m.help.capturing = true
	m.help.notes = []string{"rebinding " + string(row.action) + " — press the key, esc to cancel"}
	return m, nil
}

// captureRebind takes the key the operator pressed and binds it.
//
// One key replaces the whole binding rather than being added to it. The
// second and third spellings a default carries are the same press reported
// differently by different terminals, and there is no way to ask an operator
// for those: they press one key, and the one they pressed is what the map
// records. keyName is what it is recorded as, so a chord binds as the chord
// rather than as the character its terminal reported.
func (m *Model) captureRebind(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.help.capturing = false
	if msg.String() == "esc" {
		m.help.notes = []string{"left as it was"}
		return m, nil
	}
	row, ok := m.selectedBinding()
	if !ok {
		return m, nil
	}
	key := keyName(msg)
	if notes := m.rebind(row.ctx, row.action, []string{key}); len(notes) > 0 {
		m.help.notes = notes
		return m, nil
	}
	m.help.notes = []string{string(row.action) + " is now " + keymap.Display(key)}
	return m, nil
}

// resetSelectedBinding puts the selected action back on the keys it shipped
// with.
func (m *Model) resetSelectedBinding() (tea.Model, tea.Cmd) {
	row, ok := m.selectedBinding()
	if !ok {
		return m, nil
	}
	m.help.notes = append([]string{string(row.action) + " is back on its default"},
		m.resetBinding(row.ctx, row.action)...)
	return m, nil
}

// handleHelpSearchKey types into the search. Scrolling stays live while it
// is up, so a query with more hits than the card can show is still readable
// without leaving the field.
func (m *Model) handleHelpSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.help.searching = false
	case "esc":
		m.help.searching = false
		m.help.query = ""
		m.help.scroll = 0
	case "backspace":
		if runes := []rune(m.help.query); len(runes) > 0 {
			m.help.query = string(runes[:len(runes)-1])
			m.help.scroll = 0
		}
	case "up":
		m.scrollHelp(-1)
	case "down":
		m.scrollHelp(1)
	case "pgup":
		m.scrollHelp(-m.helpPage())
	case "pgdown":
		m.scrollHelp(m.helpPage())
	default:
		// v2 fills Text only for printable keys, which is exactly the
		// KeyRunes-or-space pair the v1 type switch matched here.
		if text := msg.Key().Text; text != "" {
			m.help.query += text
			m.help.scroll = 0
		}
	}
	return m, nil
}
