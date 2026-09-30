package ui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

type paneJump struct {
	input      textinput.Model
	selectedID string
	returnMode mode
	returnID   string
	live       map[string]bool
}

func (m *Model) openPaneJump() tea.Cmd {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "name, CLI, group or status"
	input.CharLimit = 100
	cmd := input.Focus()
	m.paneJump = paneJump{input: input, returnMode: m.mode, live: m.livePanes()}
	matches := m.paneJumpMatches()
	if len(matches) > 0 {
		m.paneJump.selectedID = matches[0].ID
		if sess, ok := m.selected(); ok && slices.ContainsFunc(matches, func(match store.Session) bool { return match.ID == sess.ID }) {
			m.paneJump.selectedID = sess.ID
		}
	}
	if sess, ok := m.selected(); ok {
		m.paneJump.returnID = sess.ID
	}
	m.mode = modePaneJump
	return cmd
}

func (m *Model) paneJumpMatches() []store.Session {
	query := strings.ToLower(strings.TrimSpace(m.paneJump.input.Value()))
	var matches []store.Session
	for _, sess := range m.sessions {
		if sess.Archived || sess.Status == status.Dead || !m.paneJump.live[sess.ID] {
			continue
		}
		if query == "" || matchesMetadata(sess, query) {
			matches = append(matches, sess)
		}
	}
	return matches
}

func (m *Model) handlePaneJumpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = m.paneJump.returnMode
		if m.mode == modeFocus {
			if sess, ok := m.sessionByID(m.paneJump.returnID); !ok || sess.Status == status.Dead || !m.tmux.Exists(sess.ID) || !m.focusSession(sess.ID) {
				return m, m.leaveFocus()
			}
			return m, m.cursorBlink()
		}
		return m, nil
	case "enter":
		return m.submitPaneJump()
	case "up", "shift+tab", "down", "tab":
		matches := m.paneJumpMatches()
		if len(matches) == 0 {
			return m, nil
		}
		at := slices.IndexFunc(matches, func(sess store.Session) bool { return sess.ID == m.paneJump.selectedID })
		delta := 1
		if msg.String() == "up" || msg.String() == "shift+tab" {
			delta = -1
		}
		m.paneJump.selectedID = matches[(at+delta+len(matches))%len(matches)].ID
		return m, nil
	}
	before := m.paneJump.input.Value()
	var cmd tea.Cmd
	m.paneJump.input, cmd = m.paneJump.input.Update(msg)
	if m.paneJump.input.Value() != before {
		m.paneJump.selectedID = ""
		if matches := m.paneJumpMatches(); len(matches) > 0 {
			m.paneJump.selectedID = matches[0].ID
		}
	}
	return m, cmd
}

func (m *Model) submitPaneJump() (tea.Model, tea.Cmd) {
	matches := m.paneJumpMatches()
	at := slices.IndexFunc(matches, func(sess store.Session) bool { return sess.ID == m.paneJump.selectedID })
	if at < 0 {
		m.errBar.text = "no live pane selected"
		return m, nil
	}
	sess := matches[at]
	if !m.tmux.Exists(sess.ID) {
		delete(m.paneJump.live, sess.ID)
		m.errBar.text = "pane closed: choose another"
		return m, nil
	}
	var leave tea.Cmd
	if m.paneJump.returnMode == modeFocus {
		leave = m.leaveFocus()
	}
	m.mode = modeList
	m.search, m.searching = "", false
	m.statusFilter = statusFilterAll
	m.showArchived = false
	var triage tea.Cmd
	if m.triage && !m.inTriageScope(sess.Group) {
		triage = m.toggleTriage()
	}
	m.unmute(sess.ID)
	for id := sess.ParentID; id != ""; {
		parent, ok := m.sessionByID(id)
		if !ok {
			break
		}
		m.setChildrenFolded(parent.ID, false)
		id = parent.ParentID
	}
	index, shown := m.revealSession(sess)
	if !shown {
		m.errBar.text = "pane is no longer on the board"
		return m, tea.Batch(leave, triage)
	}
	m.persistCollapsed()
	model, focus, _ := m.enterJumpRow(index)
	return model, tea.Batch(leave, triage, focus)
}

func (m *Model) viewPaneJump() string {
	width := min(96, max(24, m.width-4))
	inner := cardInnerWidth(width)
	m.paneJump.input.SetWidth(max(4, inner-4))
	m.paneJump.input.SetCursor(m.paneJump.input.Position())
	matches := m.paneJumpMatches()
	selected := slices.IndexFunc(matches, func(sess store.Session) bool { return sess.ID == m.paneJump.selectedID })
	rows := max(1, min(10, (m.height-12)/2))
	start := max(0, selected-rows+1)
	end := min(len(matches), start+rows)
	var b strings.Builder
	b.WriteString(keyStyle.Render("❯ ") + m.paneJump.input.View() + "\n\n")
	if len(matches) == 0 {
		b.WriteString(mutedStyle.Render("no live panes match") + "\n")
	}
	for i := start; i < end; i++ {
		sess := matches[i]
		marker, style := "  ", mutedStyle
		if i == selected {
			marker, style = keyStyle.Render("❯ "), valueStyle
		}
		b.WriteString(marker + style.Render(textfmt.TruncateWidth(m.displayName(sess), max(1, inner-2), "…")) + "\n")
		metadata := sess.Tool + " · " + sess.Status
		if sess.Group != rootGroup {
			metadata += " · " + sess.Group
		}
		b.WriteString("  " + subtleStyle.Render(textfmt.TruncateWidth(metadata, max(1, inner-2), "…")) + "\n")
	}
	b.WriteString(subtleStyle.Render(fmt.Sprintf("%d live panes", len(matches))))
	return m.cardSized(width, "▸ Jump to pane", b.String(),
		[][2]string{{"type", "search"}, {"↑↓", "pick"}, {"↵", "focus"}, {"esc", "cancel"}})
}
