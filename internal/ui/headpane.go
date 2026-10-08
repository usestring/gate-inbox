package ui

// An extension's header row with a page of its own, drawn where a session's
// pane is drawn.
//
// A header row (see headrows.go) is a thing in its own right, and an
// extension that sets HeaderPane has a page for it. The page is a view like
// any other extension view, but it is not a card over the list: the cursor
// arriving on the header shows it in the content column, as arriving on a
// session shows its pane, and the keys that focus a session focus it, with
// the same ring and the same ways out. While it is focused every key the
// board does not keep goes to the page.
//
// The board keeps one page per header while the header stands, so what the
// operator half typed into one is still there when they come back to it.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
)

// headPaneOf is the owner's HeaderPane, or nil when its headers have none.
func (m *Model) headPaneOf(owner string) *HeaderPane {
	for _, ui := range m.extUIs {
		if ui.Owner == owner {
			return ui.HeaderPane
		}
	}
	return nil
}

// headPaneAt is the page behind a header row, made the first time it is
// asked for. ok is false for a row that is not a header with a page, and for
// an extension that offered none for this header.
func (m *Model) headPaneAt(entry treeRow) (*openView, bool) {
	if !entry.isHead() {
		return nil, false
	}
	spec := m.headPaneOf(entry.head)
	if spec == nil || spec.View == nil || m.extBridge == nil {
		return nil, false
	}
	key := rowKey(entry)
	if pane, ok := m.headPanes[key]; ok {
		return pane, pane.view != nil
	}
	handle := m.extBridge.newHandle()
	pane := &openView{id: handle.id, owner: entry.head, screen: keymap.Context(spec.Screen)}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				logging.Warn("extension header page panicked", "extension", entry.head, "panic", fmt.Sprint(recovered))
				m.errBar.text = fmt.Sprintf("%s: its page failed: %v", entry.head, recovered)
				pane.view = nil
			}
		}()
		pane.view = spec.View(Press{SessionID: entry.sess.ID, Group: entry.sess.Group}, handle)
	}()
	if m.headPanes == nil {
		m.headPanes = map[string]*openView{}
	}
	// A header that offered no page is remembered as one, so the factory is
	// not asked again on every frame.
	m.headPanes[key] = pane
	return pane, pane.view != nil
}

// cursorHeadPane is the page behind the header row the cursor is on.
func (m *Model) cursorHeadPane() (*openView, bool) {
	entry, ok := m.cursorRow()
	if !ok {
		return nil, false
	}
	return m.headPaneAt(entry)
}

// pruneHeadPanes lets go of the pages whose header has left the tree,
// telling each it was closed.
func (m *Model) pruneHeadPanes() {
	if len(m.headPanes) == 0 {
		return
	}
	standing := map[string]bool{}
	for _, row := range m.rows {
		if row.isHead() {
			standing[rowKey(row)] = true
		}
	}
	for key, pane := range m.headPanes {
		if standing[key] {
			continue
		}
		delete(m.headPanes, key)
		if m.mode == modeHeadPane && m.extView.id == pane.id {
			m.extView = openView{}
			m.mode = modeList
		}
		if pane.view != nil {
			m.tellClosed(*pane, CloseHandle)
		}
	}
}

// settleHeadPanes follows a rebuilt tree: pages whose header has gone are
// let go, and a focused page whose header the cursor is no longer on hands
// the keyboard back to the list.
func (m *Model) settleHeadPanes() {
	m.pruneHeadPanes()
	if m.mode != modeHeadPane {
		return
	}
	if pane, ok := m.cursorHeadPane(); !ok || pane.id != m.extView.id {
		m.leaveHeadPane(false)
	}
}

// focusHeadPane hands the keyboard to the page behind the header row the
// cursor is on. It reports false when that row has no page.
func (m *Model) focusHeadPane() bool {
	pane, ok := m.cursorHeadPane()
	if !ok {
		return false
	}
	m.extView = *pane
	m.mode = modeHeadPane
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				m.failHeadPane(recovered)
			}
		}()
		m.syncViewFields()
	}()
	return true
}

// stashHeadPane writes the focused page's state back to the page it was
// taken from, so its fields keep what was typed into them.
func (m *Model) stashHeadPane() {
	for _, pane := range m.headPanes {
		if pane.id == m.extView.id {
			*pane = m.extView
			return
		}
	}
}

// leaveHeadPane hands the keyboard back to the list, or in a triage walk on
// to the next session that needs a person, the way leaving a focused
// session does.
func (m *Model) leaveHeadPane(advance bool) tea.Cmd {
	m.stashHeadPane()
	m.extView = openView{}
	m.mode = modeList
	if !advance || !m.advancesOnLeave() {
		return nil
	}
	left := ""
	if sess, ok := m.selected(); ok {
		left = sess.ID
	}
	return m.advanceTriage(left)
}

// failHeadPane drops a page that panicked, and says so.
func (m *Model) failHeadPane(recovered any) {
	owner := m.extView.owner
	logging.Warn("extension header page panicked", "extension", owner, "panic", fmt.Sprint(recovered))
	for key, pane := range m.headPanes {
		if pane.id == m.extView.id {
			m.headPanes[key] = &openView{id: pane.id, owner: pane.owner}
		}
	}
	m.extView = openView{}
	m.mode = modeList
	m.errBar.text = fmt.Sprintf("%s: its page failed and was closed: %v", owner, recovered)
}

// handleHeadPaneKey answers a key while a page is focused. The focused
// session's ways out leave it too, and so does the page's own close.
func (m *Model) handleHeadPaneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if action, bound := m.action(keymap.ContextFocus, msg); bound {
		switch action {
		case keymap.Leave, keymap.HandOver:
			return m, m.leaveHeadPane(true)
		case keymap.LeaveHard:
			return m, m.leaveHeadPane(false)
		}
	}
	action, bound := m.action(m.extView.screen, msg)
	if bound && action == ActionClose {
		return m, m.leaveHeadPane(false)
	}
	if m.extView.fields != nil {
		if handled, cmd := m.handleViewFieldKey(msg, action, bound); handled {
			m.stashHeadPane()
			return m, cmd
		}
	}
	key := ViewKey{Key: keyName(msg), Text: cleanText(msg.Key().Text)}
	if bound {
		key.Action = string(action)
	}
	m.tellView(key)
	m.stashHeadPane()
	return m, nil
}

// headPaneLines draws the page behind the header row the cursor is on in
// the content column: its title, then its body, then its fields. Focused,
// it records the box the ring is drawn around.
func (m *Model) headPaneLines(width, height int, gutter string) (lines []contentLine, ok bool) {
	pane, ok := m.cursorHeadPane()
	if !ok {
		return nil, false
	}
	focused := m.mode == modeHeadPane && m.extView.id == pane.id
	view := *pane
	if focused {
		m.syncViewFields()
		view = m.extView
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if focused {
				m.failHeadPane(recovered)
			}
			lines, ok = nil, false
		}
	}()
	inner := max(width-2*contentGutter, 1)
	styles := viewStyles()
	title := textfmt.TruncateWidth(cleanText(view.view.Title()), inner, "…")
	head := []string{"", styles[viewStyleKey{ToneMuted, true}].Render(title)}
	if !focused {
		if key := m.tightCap(keymap.ContextList, keymap.Open); key != "" {
			head[1] += mutedStyle.Render("  · " + key + " to focus")
		}
	}
	head = append(head, "")
	room := max(height-len(head), 1)
	var fieldBlock []string
	var fieldStarts []int
	bodyRoom := room
	if focused && m.extView.fields != nil {
		fieldBlock, fieldStarts = m.fieldLines(inner)
		bodyRoom = max(room-len(fieldBlock)-1, room/3)
	}
	rows := view.view.Render(inner, bodyRoom)
	for _, line := range head {
		lines = append(lines, contentLine{text: gutter + line})
	}
	for _, row := range rows[:min(len(rows), bodyRoom)] {
		var b strings.Builder
		for _, span := range row {
			b.WriteString(styles[viewStyleKey{span.Tone, span.Bold}].Render(cleanText(span.Text)))
		}
		lines = append(lines, contentLine{text: gutter + textfmt.TruncateWidth(b.String(), inner, "…")})
	}
	if len(fieldBlock) > 0 {
		used := len(lines) - len(head)
		if used > 0 && room-used > 1 {
			lines = append(lines, contentLine{})
		}
		for _, line := range m.fieldWindow(fieldBlock, fieldStarts, max(height-len(lines), 1)) {
			lines = append(lines, contentLine{text: gutter + line})
		}
	}
	m.pane.box = paneBox{}
	if focused {
		m.pane.box = paneBox{
			x:      m.paneOriginX(),
			y:      m.listChromeRows() + m.previewBodyOffset,
			width:  width,
			height: height,
			ok:     true,
		}
	}
	return lines, true
}

// headPaneLegend is the footer while a page is focused: its screen's keys,
// and the way out the focused session's footer names.
func (m *Model) headPaneLegend() legendSection {
	pairs := [][2]string{{m.capJoinFull(keymap.ContextFocus, " / ", keymap.Leave, keymap.LeaveHard), "back to manager"}}
	if m.extView.fields != nil {
		pairs = append(pairs, m.fieldHint()...)
	}
	pairs = append(pairs, m.extensionViewHint()...)
	return legendSection{title: "Focused", pairs: pairs}
}

// ringed is whether the content column is drawn as a focused pane: a
// session's, or a header's page.
func (m *Model) ringed() bool {
	return m.mode == modeFocus || m.mode == modeHeadPane
}

// headPaneTriage is where a triage walk lands on a session that has a header
// page: the header row over it, focused, rather than the session's pane. ok
// is false when the session has no such header.
func (m *Model) headPaneTriage(index int) bool {
	for i := index - 1; i >= 0 && m.rows[i].isHead() && m.rows[i].sess.ID == m.rows[index].sess.ID; i-- {
		if _, ok := m.headPaneAt(m.rows[i]); ok {
			m.cursor = i
			return m.focusHeadPane()
		}
	}
	return false
}
