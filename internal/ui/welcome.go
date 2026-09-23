// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"github.com/usestring/gate-inbox/internal/keymap"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Nothing has ever told a first-time operator what this program is. The list
// opens empty, `n` is the only key that does anything visible, and the key
// map behind `?` is a reference rather than an introduction: it answers what
// a key does, not which four keys the whole workflow is built out of. This
// card is the introduction, shown once, on a store that has never held a
// session -- so an install already in use is never interrupted by it.
//
// It offers a guided walkthrough as well as the reading, because a tour that
// drives the real board teaches the keys better than a list of them does.
// The walkthrough is not built yet, so the option says so rather than being
// hidden: an operator who wants one should find out that it is coming, and
// leaving the row out would make the card look complete when it is not.

// welcomeSeenSetting marks the introduction as spent. It is set when the card
// is raised rather than when it is dismissed: a first run that quits out of
// the card has still been offered the introduction, and re-raising it on
// every start until somebody presses the right key would be a nag.
const welcomeSeenSetting = "welcome_seen"

type welcomeState struct {
	scroll int
}

// welcomeSection is one part of the tour, titled for what the operator is
// trying to do rather than for the part of the program involved.
type welcomeSection struct {
	title string
	rows  [][2]string
}

// welcomeIntro is what the program is, in the two sentences an operator needs
// before any key means anything.
func welcomeIntro() []string {
	return []string{
		"Every AI coding agent on this machine in one list, with live status, so a session that is " +
			"blocked on you is visible without hunting through terminal tabs.",
		"Each session is your own installed CLI in its own tmux session: your login, your config, " +
			"your MCP servers, and sessions that outlive this program.",
	}
}

func welcomeSections() []welcomeSection {
	return []welcomeSection{
		{title: "start something", rows: [][2]string{
			{"n", "new session in the group under the cursor; it asks which agent"},
			{"ctrl+n", "the same, asking first: name, CLI, directory, first task"},
			{"g", "a group: a folder of sessions with its own default path"},
			{"T", "a shell under the selected agent, for builds and one-off commands"},
		}},
		{title: "answer one", rows: [][2]string{
			{"↵", "focus it: keys reach the agent while the list stays on screen"},
			{"space", "quick prompt: send one message without leaving the list"},
			{"ctrl+q", "from inside a session, back to this list"},
		}},
		{title: "when several are blocked at once", rows: [][2]string{
			{"w", "filter the list down to what needs you"},
			{"i", "triage: one queue, longest-blocked first; ctrl+q hops to the next"},
			{"p", "priority: tier this session, or a whole group, to head that queue"},
		}},
		{title: "keep track", rows: [][2]string{
			{"W", "work view: the pull requests and tickets these sessions are on"},
			{"x / v", "kill a session to free its RAM / revive it on its conversation"},
			{"s", "settings: default CLI, theme, density"},
		}},
	}
}

// welcomeKeyColumn is measured over the whole card rather than fixed, so a
// row added later cannot render clipped against its own description.
func welcomeKeyColumn() int {
	width := 0
	for _, section := range welcomeSections() {
		for _, row := range section.rows {
			if w := cellWidth(row[0]); w > width {
				width = w
			}
		}
	}
	return width + 2
}

// welcomeBodyLines is the whole card as flat lines, which is what fitBody
// scrolls. The choices sit at the end, after the reading they follow from.
// Everything wraps rather than truncating: on a narrow card the half that
// would be cut is the half that says what a key does.
func welcomeBodyLines(inner int) []string {
	column := welcomeKeyColumn()
	if room := inner / 3; column > room {
		column = max(room, 4)
	}
	lines := make([]string, 0, 48)
	for i, paragraph := range welcomeIntro() {
		if i > 0 {
			lines = append(lines, "")
		}
		for _, line := range wrapDescription(paragraph, inner) {
			lines = append(lines, subtleStyle.Render(line))
		}
	}
	for _, section := range welcomeSections() {
		lines = append(lines, "", sectionStyle.Render(section.title))
		for _, row := range section.rows {
			lines = append(lines, welcomeRow("  ", keyStyle.Render(row[0]), valueStyle, row[1], column, inner)...)
		}
	}
	lines = append(lines, "")
	lines = append(lines, welcomeChoices(column, inner)...)
	lines = append(lines, "")
	for _, line := range wrapDescription("? is the complete, current key map, on every screen. The settings screen brings this card back.", inner) {
		lines = append(lines, subtleStyle.Render(line))
	}
	return lines
}

// welcomeRow lays one key against its description, the description wrapping
// under itself rather than under the key. The marker is two columns wide so
// a picked row and a plain one keep the same key column.
func welcomeRow(marker, key string, style fastStyle, description string, column, inner int) []string {
	indent := spaces(column + 2)
	room := max(inner-column-2, 1)
	var lines []string
	for i, line := range wrapDescription(description, room) {
		prefix := indent
		if i == 0 {
			prefix = marker + padRight(key, column)
		}
		lines = append(lines, prefix+style.Render(line))
	}
	return lines
}

// welcomeChoices is the pair of answers the card ends on. The walkthrough is
// drawn dimmed and labelled rather than omitted, so what it will be is clear
// and what it is today is not overstated.
func welcomeChoices(column, inner int) []string {
	pick := lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
	return append(
		welcomeRow(pick, keyStyle.Render("↵"), valueStyle, "get started", column, inner),
		welcomeRow("  ", subtleStyle.Render("t"), mutedStyle, "take the guided walkthrough — not built yet", column, inner)...)
}

// welcomeIsFirstRun reports a store that has never held a session. An install
// already in use is not a first run however new this card is to it, and the
// introduction interrupting a working board would be the wrong trade.
func (m *Model) welcomeIsFirstRun() bool {
	sessions, err := m.store.ListSessions(true)
	if err != nil {
		// Unreadable is treated as "already in use": a broken read must not
		// raise a modal over whatever the operator opened the manager to do.
		return false
	}
	return len(sessions) == 0
}

// maybeOpenWelcome raises the introduction on a first run. Init arms it, so a
// Model built directly never asks, and the seen flag is written here rather
// than on dismissal.
func (m *Model) maybeOpenWelcome() {
	if !m.welcomeArmed || m.store == nil || m.mode != modeList {
		return
	}
	m.welcomeArmed = false
	seen, err := m.store.Setting(welcomeSeenSetting)
	if err != nil || seen != "" {
		return
	}
	// Spent whether or not the card is shown: an install already in use when
	// this shipped must not be offered the introduction the first time it
	// happens to start with an empty board.
	m.markWelcomeSeen()
	if m.welcomeIsFirstRun() {
		m.openWelcome()
	}
}

func (m *Model) markWelcomeSeen() {
	if err := m.store.SetSetting(welcomeSeenSetting, "1"); err != nil {
		m.errBar.text = "saving the welcome flag: " + err.Error()
	}
}

func (m *Model) openWelcome() {
	m.welcome = welcomeState{}
	m.mode = modeWelcome
}

// closeWelcome always lands on the list: the card is raised from a first
// start and from settings, and settings saves and closes on the way in.
func (m *Model) closeWelcome() {
	m.welcome = welcomeState{}
	m.mode = modeList
}

func (m *Model) welcomeHint() [][2]string {
	return [][2]string{
		{m.navCap(keymap.ContextWelcome), "scroll"},
		{m.cap(keymap.ContextWelcome, keymap.Close), "get started"},
		{"t", "walkthrough"},
		{m.cap(keymap.ContextWelcome, keymap.Help), "keys"},
	}
}

func (m *Model) welcomeBodyRoom() int {
	inner := cardInnerWidth(helpCardWidth(m.width))
	// Title, the blank under it, the blank above the rule, the rule itself,
	// the hint - which wraps on a narrow card - and the bottom rule.
	room := m.height - 5 - lipgloss.Height(legendInline(m.welcomeHint(), inner))
	if m.errBar.text != "" {
		room -= 2
	}
	return max(room, 1)
}

func (m *Model) welcomeScrollLimit() int {
	return max(len(welcomeBodyLines(cardInnerWidth(helpCardWidth(m.width))))-m.welcomeBodyRoom(), 0)
}

func (m *Model) scrollWelcome(delta int) {
	m.welcome.scroll = min(max(m.welcome.scroll+delta, 0), m.welcomeScrollLimit())
}

func (m *Model) welcomePage() int { return max(m.welcomeBodyRoom()-1, 1) }

func (m *Model) viewWelcome() string {
	width := helpCardWidth(m.width)
	inner := cardInnerWidth(width)
	body := fitBody(welcomeBodyLines(inner), m.welcomeBodyRoom(), m.welcome.scroll)
	return m.cardSized(width, "◆ Welcome to Gate Inbox", strings.Join(body, "\n"), m.welcomeHint())
}

func (m *Model) handleWelcomeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if msg.String() == "t" {
		// Refusing in place rather than closing: the card is what explains
		// the alternative, so leaving it would take the answer away with it.
		m.errBar.text = "the guided walkthrough is not built yet — " +
			m.cap(keymap.ContextWelcome, keymap.Help) + " opens the full key map"
		return m, nil
	}
	action, bound := m.action(keymap.ContextWelcome, msg)
	if !bound {
		return m, nil
	}
	switch action {
	case keymap.Close:
		m.errBar.text = ""
		m.closeWelcome()
		return m, m.startStartupTick()
	case keymap.Help:
		m.openHelp()
		return m, nil
	case keymap.CursorUp:
		m.scrollWelcome(-1)
	case keymap.CursorDown:
		m.scrollWelcome(1)
	case keymap.PageUp:
		m.scrollWelcome(-m.welcomePage())
	case keymap.PageDown:
		m.scrollWelcome(m.welcomePage())
	case keymap.Top:
		m.welcome.scroll = 0
	case keymap.Bottom:
		m.welcome.scroll = m.welcomeScrollLimit()
	}
	return m, nil
}
