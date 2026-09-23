package ui

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

type conversationView struct {
	targets      map[string]search.Target
	locator      *search.Locator
	busy         bool
	key, stamp   string
	messages     []search.Message
	err          error
	offset       int
	lines        []string
	width, theme int
	dirty        bool
	compact      bool
}

type conversationTickMsg struct{}
type conversationMsg struct {
	key, stamp string
	messages   []search.Message
	err        error
	unchanged  bool
}

func conversationTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return conversationTickMsg{} })
}

func (m *Model) showsConversation() bool {
	sess, ok := m.selected()
	return m.conversation != nil && ok && !m.isShell(sess.Tool) &&
		(m.mode == modeList || m.mode == modeRename || (m.mode == modeFocus && m.gate.on && m.gate.menu))
}

func conversationKey(id, agentID string) string { return id + "\x00" + agentID }

func (m *Model) conversationIdentity(sess store.Session) string {
	id := sess.AgentSessionID
	if id == "" && sess.TmuxPaneID != "" {
		id = m.conversation.targets[sess.ID].AgentID
	}
	return conversationKey(sess.ID, id)
}

func (m *Model) readConversation() tea.Cmd {
	if !m.showsConversation() || m.conversation.busy {
		return nil
	}
	sess, _ := m.selected()
	c := m.conversation
	c.busy = true
	key := m.conversationIdentity(sess)
	matched := c.targets[sess.ID]
	previousKey, previousStamp := c.key, c.stamp
	locator := c.locator
	tool := historyToolFormats(m.cfg)[sess.Tool]
	database := opencodeDBPath()
	return func() tea.Msg {
		msg := conversationMsg{key: key}
		target, ok := locator.Target(sess.ID, tool, sess.Cwd, sess.AgentSessionID)
		if !ok && sess.AgentSessionID == "" && sess.TmuxPaneID != "" && matched.AgentID != "" {
			target, ok = matched, true
		}
		if !ok {
			return msg
		}
		paths := []string{target.Path}
		if target.Tool == search.ToolOpenCode {
			paths = []string{database, database + "-wal"}
		}
		for _, path := range paths {
			info, err := os.Stat(path)
			if err != nil {
				if path == database+"-wal" && os.IsNotExist(err) {
					continue
				}
				msg.err = err
				return msg
			}
			msg.stamp += fmt.Sprintf("%s:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
		}
		if key == previousKey && msg.stamp == previousStamp {
			msg.unchanged = true
			return msg
		}
		msg.messages, msg.err = search.ReadMessages(target, database)
		return msg
	}
}

func (m *Model) applyConversation(msg conversationMsg) {
	c := m.conversation
	if c == nil {
		return
	}
	c.busy = false
	sess, ok := m.selected()
	if !ok || msg.key != m.conversationIdentity(sess) || msg.unchanged {
		return
	}
	if c.key != msg.key {
		c.offset, c.lines = 0, nil
	}
	m.sel = focusSelection{}
	c.key, c.stamp, c.messages, c.err = msg.key, msg.stamp, msg.messages, msg.err
	if msg.err != nil {
		c.stamp = ""
	}
	c.dirty = true
}

func (c *conversationView) wrapped(width int) []string {
	if !c.dirty && c.width == width && c.theme == renderGen {
		return c.lines
	}
	oldHeight := len(c.lines)
	c.lines = nil
	userStyle := newFastStyle(lipgloss.NewStyle().Foreground(colorAccent2).Bold(true))
	for _, message := range c.messages {
		label, style := "Assistant", sectionStyle
		if message.Role == "user" {
			label, style = "You", userStyle
		}
		inner := max(1, width-4)
		text := strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if r != '\n' && unicode.IsControl(r) {
				return -1
			}
			return r
		}, ansi.Strip(message.Text))
		heading := cellTruncate(" "+label+" ", max(1, width-2), "")
		c.lines = append(c.lines, style.Render("╭"+heading+strings.Repeat("─", max(0, width-2-cellWidth(heading)))+"╮"))
		messageLines := strings.Split(ansi.Hardwrap(ansi.Wrap(text, inner, ""), inner, true), "\n")
		if c.compact && len(messageLines) > 4 {
			messageLines = append(messageLines[:4:4], cellTruncate(fmt.Sprintf("… %d more lines", len(messageLines)-4), inner, "…"))
		}
		for _, line := range messageLines {
			c.lines = append(c.lines, style.Render("│")+" "+padRight(valueStyle.Render(line), inner)+" "+style.Render("│"))
		}
		c.lines = append(c.lines, style.Render("╰"+strings.Repeat("─", max(0, width-2))+"╯"), "")
	}
	if c.offset > 0 && c.width == width {
		c.offset += max(0, len(c.lines)-oldHeight)
	}
	c.width, c.theme, c.dirty = width, renderGen, false
	return c.lines
}

func (m *Model) conversationLines(width, height int) []contentLine {
	m.pane.box = paneBox{x: m.paneOriginX(), y: m.listChromeRows() + m.previewBodyOffset, width: width, height: height, ok: true}
	rows := m.conversationRows(width, height)
	lines := make([]contentLine, 0, height)
	for i, row := range rows {
		lines = append(lines, contentLine{text: m.renderPaneRow(i, row, width), raw: true})
	}
	return lines
}

func (m *Model) conversationRows(width, height int) []string {
	c := m.conversation
	sess, _ := m.selected()
	var rows []string
	if c.key == m.conversationIdentity(sess) {
		rows = c.wrapped(width)
	}
	if len(rows) == 0 {
		text := "No user-facing messages yet. Open the terminal to interact."
		if c.key == m.conversationIdentity(sess) && c.err != nil {
			text = "Conversation unavailable: " + c.err.Error()
		}
		return []string{mutedStyle.Render(cellTruncate(text, width, "…"))}
	}
	c.offset = min(c.offset, max(0, len(rows)-height))
	end := len(rows) - c.offset
	start := max(0, end-height)
	return rows[start:end]
}

func (m *Model) toggleConversation() {
	if !m.showsConversation() {
		return
	}
	c := m.conversation
	c.compact, c.dirty, c.offset = !c.compact, true, 0
	m.sel = focusSelection{}
}

func (m *Model) conversationToggleLabel() string {
	if m.conversation != nil && m.conversation.compact {
		return "full view"
	}
	return "shorten"
}

func (m *Model) scrollConversation(lines int) tea.Cmd {
	c := m.conversation
	rows := c.wrapped(m.previewPaneWidth())
	height := m.previewPaneHeight()
	if m.pane.box.ok {
		height = m.pane.box.height
	}
	c.offset = min(max(0, c.offset-lines), max(0, len(rows)-height))
	return nil
}
