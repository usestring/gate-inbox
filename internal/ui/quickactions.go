package ui

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/keymap"
)

// Quick actions is the list's command palette: every action the list answers,
// found by typing what it does rather than remembering where it lives. ↵ runs
// it through the same dispatch its key reaches, so nothing here decides what
// an action means.
//
// It is also how the keys get learned. Every row prints the key the action is
// on now and the name keys.toml calls it, and running one from here leaves a
// note saying which key would have done it in one press.

// quickActionsHidden are list actions the palette leaves out: cursor and
// scroll steps, which nobody runs by name, and the keys that only mean
// something as a held or repeated press.
var quickActionsHidden = map[keymap.Action]bool{
	keymap.CursorUp:        true,
	keymap.CursorDown:      true,
	keymap.CursorTop:       true,
	keymap.CursorBottom:    true,
	keymap.StepIn:          true,
	keymap.StepOut:         true,
	keymap.PreviewUp:       true,
	keymap.PreviewDown:     true,
	keymap.PreviewPageUp:   true,
	keymap.PreviewPageDown: true,
	keymap.PreviewTop:      true,
	keymap.PreviewBottom:   true,
	keymap.LegendPeek:      true,
	keymap.QuickActions:    true,
}

// quickActionsRecentMax is how many recently run actions lead an empty
// palette.
const quickActionsRecentMax = 5

type quickActions struct {
	input  textinput.Model
	cursor int
	// recent is the actions run from here, newest first. It outlives the
	// palette closing so the next open starts on what was just used.
	recent []keymap.Action
}

type quickActionEntry struct {
	action keymap.Action
	key    string
	label  string
}

func (m *Model) openQuickActions() {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "type what you want to do"
	input.CharLimit = 80
	input.Focus()
	m.actions.input = input
	m.actions.cursor = 0
	m.errBar.text = ""
	m.mode = modeQuickActions
}

// quickActionEntries is every action the palette offers, in the order the key
// map lists them, with the key each is on now.
func (m *Model) quickActionEntries() []quickActionEntry {
	var out []quickActionEntry
	for _, binding := range m.km().Bindings(keymap.ContextList) {
		if quickActionsHidden[binding.Action] {
			continue
		}
		out = append(out, quickActionEntry{
			action: binding.Action,
			key:    m.cap(keymap.ContextList, binding.Action),
			label:  binding.Label,
		})
	}
	return out
}

// quickActionMatches narrows the entries to the query. Every word typed has to
// fuzzy match the label, the action's name or its key, and the best matches
// come first. An empty query lists everything, recently run actions first.
func (m *Model) quickActionMatches() []quickActionEntry {
	entries := m.quickActionEntries()
	query := strings.ToLower(strings.TrimSpace(m.actions.input.Value()))
	if query == "" {
		return withRecentFirst(entries, m.actions.recent)
	}
	type scored struct {
		entry quickActionEntry
		score int
	}
	var hits []scored
	for _, entry := range entries {
		if score, ok := quickActionScore(entry, query); ok {
			hits = append(hits, scored{entry, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]quickActionEntry, len(hits))
	for i, hit := range hits {
		out[i] = hit.entry
	}
	return out
}

func quickActionScore(entry quickActionEntry, query string) (int, bool) {
	// A query that is the key itself ranks that action first: typing "n" to
	// check what n does should answer with n.
	exactKey := 0
	if strings.EqualFold(entry.key, query) {
		exactKey = 1 << 20
	}
	haystack := util.ToChars([]byte(strings.ToLower(
		entry.label + " " + strings.ReplaceAll(string(entry.action), "_", " ") + " " + entry.key)))
	score := 0
	for _, term := range strings.Fields(query) {
		match, _ := algo.FuzzyMatchV2(false, false, true, &haystack, []rune(term), false, nil)
		if match.Start < 0 {
			if exactKey > 0 {
				return exactKey, true
			}
			return 0, false
		}
		score += match.Score
	}
	return score + exactKey, true
}

func withRecentFirst(entries []quickActionEntry, recent []keymap.Action) []quickActionEntry {
	if len(recent) == 0 {
		return entries
	}
	out := make([]quickActionEntry, 0, len(entries))
	for _, action := range recent {
		for _, entry := range entries {
			if entry.action == action {
				out = append(out, entry)
			}
		}
	}
	for _, entry := range entries {
		if !slices.Contains(recent, entry.action) {
			out = append(out, entry)
		}
	}
	return out
}

func (m *Model) handleQuickActionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	matches := m.quickActionMatches()
	switch msg.String() {
	case "esc":
		m.mode = modeList
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "enter":
		if len(matches) == 0 {
			return m, nil
		}
		return m.runQuickAction(matches[min(m.actions.cursor, len(matches)-1)])
	case "up", "ctrl+p", "shift+tab":
		if len(matches) > 0 {
			m.actions.cursor = (m.actions.cursor - 1 + len(matches)) % len(matches)
		}
		return m, nil
	case "down", "ctrl+n", "tab":
		if len(matches) > 0 {
			m.actions.cursor = (m.actions.cursor + 1) % len(matches)
		}
		return m, nil
	}
	before := m.actions.input.Value()
	var cmd tea.Cmd
	m.actions.input, cmd = m.actions.input.Update(msg)
	if m.actions.input.Value() != before {
		m.actions.cursor = 0
	}
	return m, cmd
}

// runQuickAction closes the palette and runs the action as its key would from
// the list: a status jump, the artifact row's refusals, then the list's own
// switch.
func (m *Model) runQuickAction(entry quickActionEntry) (tea.Model, tea.Cmd) {
	m.mode = modeList
	m.rememberQuickAction(entry.action)
	m.errBar.text = ""
	model, cmd := m.dispatchQuickAction(entry.action)
	if m.errBar.text == "" {
		if entry.key != "" {
			m.reportDone("next time press " + entry.key + " for " + string(entry.action))
		} else {
			m.reportDone(string(entry.action) + " has no key: bind one from the key map (" +
				m.cap(keymap.ContextList, keymap.Help) + ")")
		}
	}
	return model, cmd
}

func (m *Model) dispatchQuickAction(action keymap.Action) (tea.Model, tea.Cmd) {
	if jump, isJump := statusJumps[action]; isJump {
		return m.jumpToStatus(jump)
	}
	if model, cmd, answered := m.artifactRowAction(action, true); answered {
		return model, cmd
	}
	return m.runListAction(action, tea.KeyPressMsg{})
}

func (m *Model) rememberQuickAction(action keymap.Action) {
	recent := []keymap.Action{action}
	for _, previous := range m.actions.recent {
		if previous != action && len(recent) < quickActionsRecentMax {
			recent = append(recent, previous)
		}
	}
	m.actions.recent = recent
}

// quickActionsCardMaxWidth leaves room for the label and the action's name on
// one line without stretching across a wide terminal.
const quickActionsCardMaxWidth = 96

func (m *Model) viewQuickActions() string {
	width := quickActionsCardMaxWidth
	if m.width >= 28 && width > m.width-4 {
		width = m.width - 4
	}
	inner := cardInnerWidth(width)
	m.actions.input.SetWidth(max(4, inner-4))
	m.actions.input.SetCursor(m.actions.input.Position())

	matches := m.quickActionMatches()
	if m.actions.cursor >= len(matches) {
		m.actions.cursor = max(0, len(matches)-1)
	}
	var b strings.Builder
	b.WriteString(keyStyle.Render("❯ ") + m.actions.input.View() + "\n\n")
	if len(matches) == 0 {
		b.WriteString(mutedStyle.Render("no action matches") + "\n")
	}

	keyColumn := 0
	for _, entry := range matches {
		keyColumn = max(keyColumn, textfmt.Width(entry.key))
	}
	keyColumn = max(keyColumn, 1) + 2
	nameColumn := 0
	for _, entry := range matches {
		nameColumn = max(nameColumn, textfmt.Width(string(entry.action)))
	}
	labelColumn := inner - 2 - keyColumn - nameColumn - 2
	if labelColumn < 16 {
		// Too narrow for the name: the label is what says what the row does.
		nameColumn = 0
		labelColumn = inner - 2 - keyColumn
	}

	rows := max(3, min(14, m.height-12))
	start := 0
	if m.actions.cursor >= rows {
		start = m.actions.cursor - rows + 1
	}
	end := min(len(matches), start+rows)
	for i := start; i < end; i++ {
		entry := matches[i]
		selected := i == m.actions.cursor
		marker := "  "
		label := mutedStyle
		if selected {
			marker = keyStyle.Render("❯ ")
			label = valueStyle
		}
		key := entry.key
		keyText := keyStyle.Render(key)
		if key == "" {
			keyText = subtleStyle.Render("—")
		}
		line := marker + padRight(keyText, keyColumn) +
			padRight(label.Render(textfmt.TruncateWidth(entry.label, max(1, labelColumn), "…")), labelColumn)
		if nameColumn > 0 {
			line += "  " + subtleStyle.Render(string(entry.action))
		}
		b.WriteString(line + "\n")
	}
	if hidden := len(matches) - end; hidden > 0 {
		b.WriteString(subtleStyle.Render("  … "+strconv.Itoa(hidden)+" more") + "\n")
	}
	return m.cardSized(width, "▸ Quick actions", strings.TrimRight(b.String(), "\n"),
		[][2]string{{"type", "filter"}, {"↑↓", "pick"}, {"↵", "run"}, {"esc", "close"}})
}
