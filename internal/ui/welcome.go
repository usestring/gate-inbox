// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/keymap"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/usestring/gate-inbox/extension/textfmt"
)

// Nothing has ever told a first-time operator what this program is. The list
// opens empty, `n` is the only key that does anything visible, and the key
// map behind `?` is a reference rather than an introduction: it answers what
// a key does, not which four keys the whole workflow is built out of. This
// card is the introduction, shown once, on a store that has never held a
// session -- so an install already in use is never interrupted by it.
//
// It is also the first-run checklist: which agent CLIs this machine has, the
// five keys the whole workflow is built from, whether agents are already
// running in tmux (the board takes them over once they are idle),
// and n to start a first session straight from the card.

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

// welcomeFiveKeys is the whole workflow in five keys, ahead of the fuller
// sections for when the operator wants more.
func (m *Model) welcomeFiveKeys() welcomeSection {
	list, focus := keymap.ContextList, keymap.ContextFocus
	peek, help := m.cap(list, keymap.LegendPeek), m.cap(list, keymap.Help)
	return welcomeSection{title: "the five keys that matter", rows: welcomeRows(
		m.welcomeRow(keymap.NewSession, "start a session — pick the agent CLI, then type its task"),
		m.welcomeRow(keymap.Open, "focus the session under the cursor; keys go to the agent"),
		[2]string{m.cap(focus, keymap.Leave), "back to this list from inside a session"},
		m.welcomeRow(keymap.Triage, "triage: every session waiting on you, longest-waiting first"),
		[2]string{joinKeys(" / ", peek, help), welcomePeekText(peek, help)},
	)}
}

// welcomePeekText describes the peek and the key map together, naming each by
// its own key so the row still reads once either has moved.
func welcomePeekText(peek, help string) string {
	switch {
	case peek != "" && help != "":
		return peek + " peeks at the keys for this row; " + help + " is the full key map"
	case help != "":
		return "the full key map"
	}
	return "peek at the keys for this row"
}

func (m *Model) welcomeSections() []welcomeSection {
	list, focus := keymap.ContextList, keymap.ContextFocus
	triage := "triage: one queue, longest-blocked first"
	if key := m.cap(focus, keymap.Leave); key != "" {
		triage += "; " + key + " hops to the next"
	}
	return []welcomeSection{
		{title: "start something", rows: welcomeRows(
			m.welcomeRow(keymap.NewSession, "new session in the group under the cursor; it asks which agent"),
			m.welcomeRow(keymap.NewSessionForm, "the same, asking first: name, CLI, directory, first task"),
			m.welcomeRow(keymap.NewGroup, "a group: a folder of sessions with its own default path"),
			m.welcomeRow(keymap.NewTerminal, "a shell under the selected agent, for builds and one-off commands"),
		)},
		{title: "answer one", rows: welcomeRows(
			m.welcomeRow(keymap.Open, "focus it: keys reach the agent while the list stays on screen"),
			m.welcomeRow(keymap.QuickInput, "hotkeys: send a snippet without leaving the list"),
			[2]string{m.cap(focus, keymap.Leave), "from inside a session, back to this list"},
		)},
		{title: "when several are blocked at once", rows: welcomeRows(
			m.welcomeRow(keymap.StatusFilter, "filter the list down to what needs you"),
			m.welcomeRow(keymap.Triage, triage),
			m.welcomeRow(keymap.Priority, "priority: tier this session, or a whole group, to head that queue"),
		)},
		{title: "keep track", rows: welcomeRows(
			m.welcomeRow(keymap.ShowAllWork, "work view: the pull requests and tickets these sessions are on"),
			[2]string{joinKeys(" / ", m.cap(list, keymap.Archive), m.cap(list, keymap.Revive)),
				"kill a session to free its RAM / revive it on its conversation"},
			m.welcomeRow(keymap.Settings, "settings: default CLI, theme, density"),
		)},
	}
}

// welcomeRow is one tour row for a list action. An action the operator has
// no key for is still taught: quick actions runs it by name, so the row names
// that route rather than vanishing from the tour.
func (m *Model) welcomeRow(action keymap.Action, text string) [2]string {
	if key := m.cap(keymap.ContextList, action); key != "" {
		return [2]string{key, text}
	}
	if route := m.hintKey(keymap.ContextList, action); route != "" {
		palette, name, _ := strings.Cut(route, " ")
		return [2]string{palette, name + " — " + text}
	}
	return [2]string{}
}

// welcomeRows drops the rows whose key the operator has taken away: a tour
// row is a key to press, and one with nothing in its key column is not.
func welcomeRows(rows ...[2]string) [][2]string {
	out := rows[:0]
	for _, row := range rows {
		if row[0] != "" {
			out = append(out, row)
		}
	}
	return out
}

// joinKeys joins the keys that are bound, so an unbound half leaves no
// dangling separator.
func joinKeys(sep string, keys ...string) string {
	var bound []string
	for _, key := range keys {
		if key != "" {
			bound = append(bound, key)
		}
	}
	return strings.Join(bound, sep)
}

// welcomeKeyColumn is measured over the whole card rather than fixed, so a
// row added later cannot render clipped against its own description.
func (m *Model) welcomeKeyColumn() int {
	width := 0
	for _, section := range append(m.welcomeSections(), m.welcomeFiveKeys()) {
		for _, row := range section.rows {
			if w := textfmt.Width(row[0]); w > width {
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
func (m *Model) welcomeBodyLines(inner int) []string {
	column := m.welcomeKeyColumn()
	if room := inner / 3; column > room {
		column = max(room, 4)
	}
	lines := make([]string, 0, 64)
	wrap := func(text string, style fastStyle) {
		for _, line := range textfmt.Wrap(text, inner) {
			lines = append(lines, style.Render(line))
		}
	}
	for i, paragraph := range welcomeIntro() {
		if i > 0 {
			lines = append(lines, "")
		}
		wrap(paragraph, subtleStyle)
	}
	lines = append(lines, "", sectionStyle.Render("your agent CLIs"))
	for _, row := range m.welcomeCLIRows() {
		lines = append(lines, welcomeRow("  ", row.mark, valueStyle, row.text, column, inner)...)
	}
	lines = append(lines, "", sectionStyle.Render("agents already running"))
	for _, line := range textfmt.Wrap(m.welcomeRunningLine(), max(inner-2, 8)) {
		lines = append(lines, "  "+valueStyle.Render(line))
	}
	for _, section := range append([]welcomeSection{m.welcomeFiveKeys()}, m.welcomeSections()...) {
		lines = append(lines, "", sectionStyle.Render(section.title))
		for _, row := range section.rows {
			lines = append(lines, welcomeRow("  ", keyStyle.Render(row[0]), valueStyle, row[1], column, inner)...)
		}
	}
	lines = append(lines, "")
	lines = append(lines, m.welcomeChoices(column, inner)...)
	lines = append(lines, "")
	reopen := "settings → welcome guide brings it back"
	if key := m.cap(keymap.ContextList, keymap.Help); key != "" {
		reopen = key + " then w brings it back, and so does settings → welcome guide"
	}
	wrap("This card shows once. "+reopen+". "+
		"The README's \"Stop using it\" section covers quitting, parking every agent and uninstalling.", subtleStyle)
	return lines
}

// welcomeCLIRow is one configured agent CLI and whether it can start.
type welcomeCLIRow struct {
	mark string
	text string
}

// welcomeCLIRows checks each configured agent CLI against PATH, since a CLI
// the board lists but cannot find is the first thing a new install trips on.
func (m *Model) welcomeCLIRows() []welcomeCLIRow {
	names := sortedToolNames(m.cfg)
	if len(names) == 0 {
		return []welcomeCLIRow{{mark: errStyle.Render("✗"), text: "no agent CLI is configured; add a [tools.<name>] block to config.toml"}}
	}
	hidden := map[string]bool{}
	if m.store != nil {
		hidden = m.hiddenTools()
	}
	var rows []welcomeCLIRow
	installed := 0
	for _, name := range names {
		tool := m.cfg.Tools[name]
		switch err := config.CheckInstalled(tool.Command); {
		case err != nil:
			rows = append(rows, welcomeCLIRow{mark: mutedStyle.Render("·"), text: name + " — not found on PATH"})
		case hidden[name]:
			installed++
			rows = append(rows, welcomeCLIRow{mark: mutedStyle.Render("·"), text: name + " — installed, hidden in settings → CLIs"})
		default:
			installed++
			rows = append(rows, welcomeCLIRow{mark: keyStyle.Render("✓"), text: name + " — ready"})
		}
	}
	if installed == 0 {
		rows = append(rows, welcomeCLIRow{mark: errStyle.Render("✗"),
			text: "none is installed: install Claude Code, Codex or OpenCode, then start the board again"})
	}
	return rows
}

// welcomeRunningLine says whether agents were already running in tmux. The
// adopt scan runs alongside the card, so the line reads as looking until it
// has answered.
func (m *Model) welcomeRunningLine() string {
	if !m.adoptFirstDone && m.store != nil && m.tmux != nil {
		return "looking for agents already running in tmux…"
	}
	n := len(m.adoptedCandidates())
	them := plural(n, "it", "them")
	switch {
	case n == 0:
		return "none found in tmux. Agents you start by hand later show up on the board as well."
	case m.outsidePanesMode() == paneRelaunch:
		return fmt.Sprintf("%d found in tmux and put on the board. The board takes %s over as its own sessions once idle, so %s can be named and steered.",
			n, them, plural(n, "it", "they"))
	}
	return fmt.Sprintf("%d found in tmux and shown on the board as-is. O takes %s over as the board's own sessions.", n, them)
}

// welcomeRow lays one key against its description, the description wrapping
// under itself rather than under the key. The marker is two columns wide so
// a picked row and a plain one keep the same key column.
func welcomeRow(marker, key string, style fastStyle, description string, column, inner int) []string {
	indent := spaces(column + 2)
	room := max(inner-column-2, 1)
	var lines []string
	for i, line := range textfmt.Wrap(description, room) {
		prefix := indent
		if i == 0 {
			prefix = marker + padRight(key, column)
		}
		lines = append(lines, prefix+style.Render(line))
	}
	return lines
}

// welcomeChoices is the pair of answers the card ends on.
func (m *Model) welcomeChoices(column, inner int) []string {
	pick := lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
	return append(
		welcomeRow(pick, keyStyle.Render(m.cap(keymap.ContextWelcome, keymap.Close)), valueStyle, "get started", column, inner),
		welcomeRow("  ", keyStyle.Render("n"), valueStyle, "start your first session now", column, inner)...)
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
		{"n", "first session"},
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
	return max(len(m.welcomeBodyLines(cardInnerWidth(helpCardWidth(m.width))))-m.welcomeBodyRoom(), 0)
}

func (m *Model) scrollWelcome(delta int) {
	m.welcome.scroll = min(max(m.welcome.scroll+delta, 0), m.welcomeScrollLimit())
}

func (m *Model) welcomePage() int { return max(m.welcomeBodyRoom()-1, 1) }

func (m *Model) viewWelcome() string {
	width := helpCardWidth(m.width)
	inner := cardInnerWidth(width)
	body := fitBody(m.welcomeBodyLines(inner), m.welcomeBodyRoom(), m.welcome.scroll)
	return m.cardSized(width, "◆ Welcome to Gate Inbox", strings.Join(body, "\n"), m.welcomeHint())
}

func (m *Model) handleWelcomeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if msg.String() == "n" {
		// The shortest way from a first run to a first agent: the card
		// closes and the list's own n takes over, agent picker and all.
		m.errBar.text = ""
		m.closeWelcome()
		model, cmd := m.startNewSession()
		return model, tea.Batch(cmd, m.startStartupTick())
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
