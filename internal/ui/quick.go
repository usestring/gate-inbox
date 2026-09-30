// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/snippets"
)

// openQuickMode docks the hotkey menu under the preview. It has no input:
// every row is a snippet, and the key beside it sends that snippet to the
// selected session. Free text is the focused session's job, where the
// operator is typing into the agent itself.
func (m *Model) openQuickMode() {
	m.errBar.text = ""
	m.quick = quickState{active: true, closeAfterSend: m.quickCloseAfterSend()}
}

// handleQuickKey runs while the hotkey menu is docked in the sidebar: arrows
// keep moving the selection on the list, a snippet's key sends it, and the
// key that opened the menu, or esc, closes it. Nothing is typed, so a key
// that names no snippet does nothing rather than reaching some other binding
// behind the menu.
func (m *Model) handleQuickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	context := keymap.ContextList
	if action, bound := m.action(context, msg); bound {
		switch action {
		case keymap.ToggleConversation:
			m.toggleConversation()
			return m, nil
		case keymap.QuickInput:
			m.quick.active = false
			return m, nil
		case keymap.Rescind:
			if m.canRescindLatestSubmission() {
				return m.rescindLatestSubmission()
			}
		}
	}
	switch msg.String() {
	case "esc":
		m.quick.active = false
		return m, nil
	case "up":
		return m, m.moveCursor(-1)
	case "down":
		return m, m.moveCursor(1)
	}
	snip, ok := m.snippetFor(msg.String())
	if !ok {
		snip, ok = m.quickSnippetFor(msg.String())
	}
	if !ok {
		return m, nil
	}
	// Closing costs nothing even when the send was refused: there is no
	// half-written text to lose, and the refusal stays in the error bar.
	if m.quick.closeAfterSend {
		m.quick.active = false
	}
	return m.sendSnippetToSelected(snip)
}

// quickSnippetFor reads a key pressed in the menu as the snippet it names
// without its chord: c for ^alt+c, § for alt+§. The menu is the one place a
// bare letter is free to mean a snippet, because nothing else there is
// listening for it.
func (m *Model) quickSnippetFor(key string) (snippets.Snippet, bool) {
	for _, snip := range m.snips.Snippets {
		if snip.Key == key {
			return snip, true
		}
	}
	return snippets.Snippet{}, false
}

// quickCloseAfterSend reports whether the hotkey menu should dismiss itself
// once a snippet is sent. Staying open is the default; a stored "close"
// choice opts in. A store error is surfaced but still yields the default.
func (m *Model) quickCloseAfterSend() bool {
	chosen, err := m.store.Setting(quickCloseSetting)
	if err != nil {
		m.errBar.text = "reading hotkey menu setting: " + err.Error()
		return false
	}
	return chosen == "close"
}
