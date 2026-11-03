package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
)

func (m *Model) focusConversationRows(width, height int) []string {
	c := m.conversation
	c.liveStart, c.liveTop, c.liveCount = 0, 0, 0
	if m.mode != modeFocus || height <= 0 {
		return m.conversationRows(width, height)
	}
	live, start, full := m.conversationInteraction(width, height)
	if len(live) == 0 {
		return m.conversationRows(width, height)
	}
	room := height - len(live)
	var rows []string
	if !full && room > 0 {
		rows = m.conversationRows(width, room)
		for len(rows) < room {
			rows = append(rows, "")
		}
	}
	c.liveStart, c.liveTop, c.liveCount = start, len(rows), len(live)
	return append(rows, live...)
}

func (m *Model) conversationInteraction(width, height int) ([]string, int, bool) {
	sess, ok := m.selected()
	if !ok || m.preview == "" || m.pane.forID != sess.ID {
		return nil, 0, false
	}
	raw, windowStart := paneWindow(m.preview, height, m.paneCaretRow())
	full := func() ([]string, int, bool) {
		return paneExact(m.preview, height, width, m.paneCaretRow()), windowStart, true
	}
	if m.engine == nil || !m.pane.cursor.ok {
		return full()
	}
	clean := ansi.Strip(m.preview)
	if state, matched := m.engine.RuleMatch(sess.Tool, clean); matched && state == status.Waiting {
		return full()
	}
	rows := strings.Split(strings.TrimSuffix(clean, "\n"), "\n")
	caret := m.pane.cursor.y
	if caret < 0 || caret >= len(rows) {
		return full()
	}
	start := -1
	for y := caret; y >= max(0, caret-12); y-- {
		if _, input := m.engine.InputPrefix(sess.Tool, rows[y]); input {
			start = y
			continue
		}
		if start >= 0 || y < caret && strings.TrimSpace(rows[y]) == "" {
			break
		}
	}
	// Unknown overlays must stay actionable even when they have no composer marker.
	if start < 0 {
		return full()
	}
	if start > 0 && strings.Trim(rows[start-1], " ─━╭╮┌┐") == "" {
		start--
	}
	start = max(start, windowStart)
	live := raw[start-windowStart:]
	for i, row := range live {
		live[i] = expandPaneTabs(row, width)
	}
	return live, start, false
}

func (m *Model) livePaneRow(row int) bool {
	if !m.showsConversation() {
		return true
	}
	c := m.conversation
	return m.mode == modeFocus && row >= c.liveTop && row < c.liveTop+c.liveCount
}

func (m *Model) conversationStream(width int) []string {
	sess, ok := m.selected()
	if !ok || m.preview == "" || m.pane.forID != sess.ID || m.engine == nil {
		return nil
	}
	clean := ansi.Strip(m.preview)
	if state, matched := m.engine.RuleMatch(sess.Tool, clean); matched && state == status.Waiting {
		return nil
	}
	raw := strings.Split(strings.TrimSuffix(clean, "\n"), "\n")
	end := len(raw)
	if live, start, full := m.conversationInteraction(width, len(raw)); !full && len(live) > 0 {
		end = start
	}
	for end > 0 && strings.TrimSpace(raw[end-1]) == "" {
		end--
	}
	height := m.previewPaneHeight()
	if m.pane.box.ok {
		height = m.pane.box.height
	}
	count := min(8, max(0, height/3-1), end)
	if count == 0 {
		return nil
	}
	rows := []string{mutedStyle.Render(previewLine("── Live output ──", width))}
	for _, row := range raw[end-count : end] {
		rows = append(rows, previewLine(expandPaneTabs(row, width), width))
	}
	return rows
}
