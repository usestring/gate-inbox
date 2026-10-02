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
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

type conversationView struct {
	targets      map[string]search.Target
	locator      *search.Locator
	busy         bool
	pendingKey   string
	key, stamp   string
	messages     []search.Message
	usage        search.TokenUsage
	err          error
	offset       int
	lines        []string
	starts       []int
	width, theme int
	dirty        bool
	compact      bool
	// hovered is the group expanded under the pointer in compact mode,
	// -1 when none.
	hovered int
}

type conversationTickMsg struct{}
type conversationMsg struct {
	key, stamp string
	messages   []search.Message
	usage      search.TokenUsage
	err        error
	unchanged  bool
}

func conversationTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return conversationTickMsg{} })
}

func (m *Model) showsConversation() bool {
	sess, ok := m.selected()
	if m.conversation == nil || !ok || m.isShell(sess.Tool) {
		return false
	}
	if m.mode == modeList || m.mode == modeRename {
		return true
	}
	// Focused, the pane belongs to the agent, but the focused-view setting
	// can keep the conversation on screen instead until F3 asks for the
	// terminal back. See focusview.go.
	return m.mode == modeFocus && m.focusView == focusViewConversation
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
	if !m.showsConversation() {
		return nil
	}
	sess, _ := m.selected()
	c := m.conversation
	key := m.conversationIdentity(sess)
	// A read for this very row is already out; a read for a row the cursor
	// has since left is superseded rather than blocking the new one. The
	// guard is per row, not per view: a cursor walk must be free to fetch
	// the row it lands on without waiting out the one before it.
	if c.busy && c.pendingKey == key {
		return nil
	}
	c.busy = true
	c.pendingKey = key
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
		msg.usage = search.ReadTokenUsage(target)
		return msg
	}
}

func (m *Model) applyConversation(msg conversationMsg) {
	c := m.conversation
	if c == nil {
		return
	}
	// Only the read this view is still waiting on clears the pending mark. A
	// frame for a row the cursor has since left must not: it would release
	// the preload the new row is still owed, and the retention that keeps
	// the last conversation on screen reads the mark to know a fetch is out.
	if msg.key == c.pendingKey {
		c.busy, c.pendingKey = false, ""
	}
	sess, ok := m.selected()
	if !ok || msg.key != m.conversationIdentity(sess) || msg.unchanged {
		return
	}
	if c.key != msg.key {
		c.offset, c.lines, c.starts = 0, nil, nil
		c.hovered = -1
	}
	m.sel = focusSelection{}
	c.key, c.stamp, c.messages, c.err = msg.key, msg.stamp, msg.messages, msg.err
	c.usage = msg.usage
	if msg.err != nil {
		c.stamp = ""
	}
	if len(c.messages) == 0 || c.hovered >= len(c.messages) {
		c.hovered = -1
	}
	c.dirty = true
}

func (c *conversationView) wrapped(width int) []string {
	if !c.dirty && c.width == width && c.theme == renderGen {
		return c.lines
	}
	oldHeight := len(c.lines)
	c.lines, c.starts = nil, nil
	// You renders in the errored (red) tone against the assistant's teal
	// accent: Accent vs Accent2 differed only in the blue channel and read
	// as the same box. Sequential turns from one speaker share one box.
	userStyle := newFastStyle(lipgloss.NewStyle().Foreground(colorErrored).Bold(true))
	type convoGroup struct {
		role  string
		label string
		style fastStyle
		texts []string
	}
	var groups []convoGroup
	for _, message := range c.messages {
		label, style := "Assistant", sectionStyle
		if message.Role == "user" {
			label, style = "You", userStyle
		}
		if n := len(groups); n > 0 && groups[n-1].role == message.Role {
			groups[n-1].texts = append(groups[n-1].texts, message.Text)
			continue
		}
		groups = append(groups, convoGroup{role: message.Role, label: label, style: style, texts: []string{message.Text}})
	}
	for gi, group := range groups {
		inner := max(1, width-4)
		text := strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if r != '\n' && unicode.IsControl(r) {
				return -1
			}
			return r
		}, ansi.Strip(strings.Join(group.texts, "\n\n")))
		heading := textfmt.TruncateWidth(" "+group.label+" ", max(1, width-2), "")
		c.starts = append(c.starts, len(c.lines))
		c.lines = append(c.lines, group.style.Render("╭"+heading+strings.Repeat("─", max(0, width-2-textfmt.Width(heading)))+"╮"))
		messageLines := markdownLines(text, inner)
		// Shortened mode keeps every older group to four lines. The newest
		// group stays full, as does whichever group the pointer is over.
		if c.compact && len(messageLines) > 4 && gi != len(groups)-1 && gi != c.hovered {
			messageLines = append(messageLines[:4:4], mutedStyle.Render(textfmt.TruncateWidth(fmt.Sprintf("… %d more lines", len(messageLines)-4), inner, "…")))
		}
		for _, line := range messageLines {
			c.lines = append(c.lines, group.style.Render("│")+" "+padRight(line, inner)+" "+group.style.Render("│"))
		}
		c.lines = append(c.lines, group.style.Render("╰"+strings.Repeat("─", max(0, width-2))+"╯"), "")
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
	identity := m.conversationIdentity(sess)
	var rows []string
	switch {
	case c.key == identity:
		rows = c.wrapped(width)
	case c.busy:
		// A read for the newly selected row is in flight. Keep the last
		// conversation on screen rather than flashing the empty placeholder
		// the key mismatch would draw: it is replaced by the new row's own
		// messages the moment the read lands. The read rides the cursor
		// move itself (see settleCursor), so this window is a message hop
		// rather than the next conversation tick.
		rows = c.wrapped(width)
	}
	rows = m.withQuestionCard(rows, width, height)
	if len(rows) == 0 {
		text := "No user-facing messages yet. Open the terminal to interact."
		if c.key == identity && c.err != nil {
			text = "Conversation unavailable: " + c.err.Error()
		}
		return m.withWorkingRow([]string{mutedStyle.Render(textfmt.TruncateWidth(text, width, "…"))}, width)
	}
	rows = m.withWorkingRow(rows, width)
	c.offset = min(c.offset, max(0, len(rows)-height))
	end := len(rows) - c.offset
	start := max(0, end-height)
	return rows[start:end]
}

func (m *Model) conversationWorking() bool {
	sess, ok := m.selected()
	return ok && sess.Status == status.Working && m.showsConversation()
}

// withWorkingRow copies rows rather than appending in place: they are the
// wrapped cache, and the spinner frame changes on every loader tick.
func (m *Model) withWorkingRow(rows []string, width int) []string {
	if !m.conversationWorking() {
		return rows
	}
	sess, _ := m.selected()
	frame := statusTint(status.Working, startupFrames[m.startupPhase%len(startupFrames)])
	text := statusLabel(sess.Status) + " · " + relSince(lastActivity(sess))
	row := frame + " " + mutedStyle.Render(textfmt.TruncateWidth(text, max(1, width-2), "…"))
	return append(rows[:len(rows):len(rows)], row)
}

func (m *Model) conversationBody(width int) []string {
	return m.withWorkingRow(m.withQuestionCard(m.conversation.wrapped(width), width, m.previewPaneHeight()), width)
}

// withQuestionCard copies rows for the same reason withWorkingRow does: they
// are the wrapped cache.
func (m *Model) withQuestionCard(rows []string, width, height int) []string {
	card := m.previewQuestions(width, height)
	if len(card) == 0 {
		return rows
	}
	return append(rows[:len(rows):len(rows)], card...)
}

func (m *Model) toggleConversation() tea.Cmd {
	if m.mode == modeFocus {
		// Focused, F3 switches between the conversation and the terminal
		// itself rather than between the conversation's two densities: the
		// choice is the focused-view setting, so a deliberate toggle sticks
		// past this visit. Shells have no conversation to show.
		sess, ok := m.selected()
		if !ok || m.isShell(sess.Tool) {
			return nil
		}
		value := focusViewTerminal
		if m.focusView != focusViewConversation {
			value = focusViewConversation
		}
		m.focusView = value
		if m.conversation != nil {
			m.conversation.hovered, m.conversation.dirty = -1, true
		}
		return deferStoreWrite(func() error {
			return m.store.SetSetting(focusViewSetting, value)
		})
	}
	if !m.showsConversation() {
		return nil
	}
	c := m.conversation
	c.compact, c.dirty, c.offset, c.hovered = !c.compact, true, 0, -1
	m.sel = focusSelection{}
	return nil
}

func (m *Model) conversationToggleLabel() string {
	if m.conversation != nil && m.conversation.compact {
		return "full view"
	}
	return "shorten"
}

// focusConversationLabel names what F3 reaches for from inside a focused
// session -- the other side of the focused-view setting.
func (m *Model) focusConversationLabel() string {
	if m.focusView == focusViewConversation {
		return "terminal"
	}
	return "conversation"
}

func (m *Model) scrollConversation(lines int) tea.Cmd {
	c := m.conversation
	rows := m.conversationBody(m.previewPaneWidth())
	height := m.previewPaneHeight()
	if m.pane.box.ok {
		height = m.pane.box.height
	}
	c.offset = min(max(0, c.offset-lines), max(0, len(rows)-height))
	// Scrolling moves the text under a still pointer, so the hovered group
	// would no longer sit under it. Drop it until the pointer moves again.
	if c.hovered != -1 {
		c.hovered, c.dirty = -1, true
	}
	return nil
}

// updateConversationHover expands the shortened group under the pointer,
// reporting whether anything changed. Terminal coordinates map onto the
// same windowed rows the paint used, so the hovered group is the one the
// operator actually sees there. The newest group is already full and the
// question card and spinner own no group, so hovering those clears.
func (m *Model) updateConversationHover(x, y int) bool {
	c := m.conversation
	if c == nil {
		return false
	}
	clear := func() bool {
		if c.hovered != -1 {
			c.hovered, c.dirty = -1, true
			return true
		}
		return false
	}
	if !m.showsConversation() || !c.compact {
		return clear()
	}
	box := m.pane.box
	if !box.ok || box.width <= 0 || box.height <= 0 ||
		x < box.x || x >= box.x+box.width || y < box.y || y >= box.y+box.height {
		return clear()
	}
	body := m.conversationBody(box.width)
	offset := min(c.offset, max(0, len(body)-box.height))
	end := len(body) - offset
	start := max(0, end-box.height)
	lineIdx := start + (y - box.y)
	if lineIdx < 0 || lineIdx >= len(body) || lineIdx >= len(c.lines) {
		return clear()
	}
	group := -1
	for i, s := range c.starts {
		if s <= lineIdx {
			group = i
		} else {
			break
		}
	}
	if group < 0 || group == len(c.starts)-1 {
		return clear()
	}
	if group == c.hovered {
		return false
	}
	c.hovered, c.dirty = group, true
	return true
}
