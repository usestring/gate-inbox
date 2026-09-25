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
// running in tmux (the reopen card that follows asks what to do with them),
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
func welcomeFiveKeys() welcomeSection {
	return welcomeSection{title: "the five keys that matter", rows: [][2]string{
		{"n", "start a session: pick the agent CLI, then type its task"},
		{"↵", "focus the session under the cursor; keys go to the agent"},
		{"ctrl+q", "back to this list from inside a session"},
		{"i", "triage: every session waiting on you, longest-waiting first"},
		{"? / H", "? peeks at the keys for this row; H is the full key map"},
	}}
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
	for _, section := range append(welcomeSections(), welcomeFiveKeys()) {
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
	column := welcomeKeyColumn()
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
	for _, section := range append([]welcomeSection{welcomeFiveKeys()}, welcomeSections()...) {
		lines = append(lines, "", sectionStyle.Render(section.title))
		for _, row := range section.rows {
			lines = append(lines, welcomeRow("  ", keyStyle.Render(row[0]), valueStyle, row[1], column, inner)...)
		}
	}
	lines = append(lines, "")
	lines = append(lines, welcomeChoices(column, inner)...)
	lines = append(lines, "")
	wrap("This card shows once. H then w brings it back, and so does settings → welcome guide. "+
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
	n := len(m.outsidePaneCandidates(true))
	undecided := len(m.outsidePaneCandidates(false))
	them := plural(n, "it", "them")
	switch {
	case n == 0:
		return "none found in tmux. Agents you start by hand later show up on the board as well."
	case undecided > 0 && m.outsidePanesMode() == reopenAsk:
		return fmt.Sprintf("%d found in tmux and put on the board as-is. After this card the board asks whether to keep %s, relaunch %s into the board, or leave %s out.",
			n, them, them, them)
	}
	return fmt.Sprintf("%d found in tmux and shown on the board as-is. O relaunches %s into the board or leaves %s out.", n, them, them)
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
func welcomeChoices(column, inner int) []string {
	pick := lipgloss.NewStyle().Foreground(colorAccent).Render("❯ ")
	return append(
		welcomeRow(pick, keyStyle.Render("↵"), valueStyle, "get started", column, inner),
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
