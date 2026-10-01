package ui

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

type panePicker struct {
	input     textinput.Model
	cursor    int
	fromFocus bool
}

type panePickEntry struct {
	sess  store.Session
	score int
}

func (m *Model) openPanePicker() {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "name, group, tool, or status"
	input.CharLimit = 80
	input.Focus()
	m.panePicker = panePicker{input: input, fromFocus: m.mode == modeFocus}
	m.errBar.text = ""
	m.mode = modePanePicker
}

func (m *Model) panePickMatches() []panePickEntry {
	query := strings.TrimSpace(m.panePicker.input.Value())
	var entries []panePickEntry
	for _, sess := range m.listedSessions() {
		if sess.Archived || sess.Status == status.Dead {
			continue
		}
		if m.triage && !m.inTriageScope(sess.Group) {
			continue
		}
		if m.search != "" && !matchesSearch(sess, strings.ToLower(m.search), m.searchText[sess.ID]) {
			if _, ok := m.historyHit(sess); !ok {
				continue
			}
		}
		if query == "" {
			entries = append(entries, panePickEntry{sess: sess})
			continue
		}
		if score, ok := fuzzyMetadataScore(sess, query); ok {
			entries = append(entries, panePickEntry{sess: sess, score: score})
		}
	}
	if query != "" {
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].score > entries[j].score })
	}
	return entries
}

func (m *Model) handlePanePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	matches := m.panePickMatches()
	switch msg.String() {
	case "esc":
		if m.panePicker.fromFocus {
			m.mode = modeFocus
			return m, tea.ClearScreen
		} else {
			m.mode = modeList
		}
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "enter":
		if len(matches) == 0 {
			return m, nil
		}
		sess := matches[min(m.panePicker.cursor, len(matches)-1)].sess
		index, ok := m.revealSession(sess)
		if !ok {
			m.errBar.text = "pane is no longer in this view"
			return m, nil
		}
		leaveCmd := tea.Cmd(nil)
		if m.panePicker.fromFocus {
			leaveCmd = m.leaveFocus()
		}
		m.mode = modeList
		m.cursor = index
		m.clearPreviewState()
		m.previewGen++
		model, focusCmd := m.focusSelected()
		cmd := tea.Batch(leaveCmd, focusCmd, m.schedulePreview())
		if m.panePicker.fromFocus {
			return model, tea.Batch(cmd, tea.ClearScreen)
		}
		return model, cmd
	case "up", "ctrl+p", "shift+tab":
		if len(matches) > 0 {
			m.panePicker.cursor = (m.panePicker.cursor - 1 + len(matches)) % len(matches)
		}
		return m, nil
	case "down", "ctrl+n", "tab":
		if len(matches) > 0 {
			m.panePicker.cursor = (m.panePicker.cursor + 1) % len(matches)
		}
		return m, nil
	}
	before := m.panePicker.input.Value()
	var cmd tea.Cmd
	m.panePicker.input, cmd = m.panePicker.input.Update(msg)
	if m.panePicker.input.Value() != before {
		m.panePicker.cursor = 0
	}
	return m, cmd
}

func (m *Model) viewPanePicker() string {
	width := 88
	if m.width >= 28 && width > m.width-4 {
		width = m.width - 4
	}
	inner := cardInnerWidth(width)
	m.panePicker.input.SetWidth(max(4, inner-4))
	matches := m.panePickMatches()
	if m.panePicker.cursor >= len(matches) {
		m.panePicker.cursor = max(0, len(matches)-1)
	}
	var b strings.Builder
	b.WriteString(keyStyle.Render("❯ ") + m.panePicker.input.View() + "\n\n")
	if len(matches) == 0 {
		b.WriteString(mutedStyle.Render("no pane matches") + "\n")
	}
	rows := max(3, min(14, m.height-12))
	start := 0
	if m.panePicker.cursor >= rows {
		start = m.panePicker.cursor - rows + 1
	}
	for i := start; i < min(len(matches), start+rows); i++ {
		sess := matches[i].sess
		group := sess.Group
		if group == rootGroup {
			group = "root"
		}
		label := m.displayName(sess) + "  ·  " + group + "  ·  " + sess.Tool + "  ·  " + sess.Status
		marker := "  "
		style := mutedStyle
		if i == m.panePicker.cursor {
			marker = keyStyle.Render("❯ ")
			style = valueStyle
		}
		b.WriteString(marker + style.Render(textfmt.TruncateWidth(label, max(1, inner-2), "…")) + "\n")
	}
	if hidden := len(matches) - min(len(matches), start+rows); hidden > 0 {
		b.WriteString(subtleStyle.Render("  … more panes") + "\n")
	}
	return m.cardSized(width, "▸ Jump to pane", strings.TrimRight(b.String(), "\n"),
		[][2]string{{"type", "filter"}, {"↑↓", "pick"}, {"↵", "focus"}, {"esc", "close"}})
}
