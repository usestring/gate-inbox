// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/snippets"
)

// openQuickMode docks the hotkey menu under the preview. It has no input:
// every row is a snippet, and the key beside it sends that snippet. Free
// text is the focused session's job, where the operator is typing into the
// agent itself.
//
// From inside a focused session the same menu answers that session, so the
// snippets are one key away without leaving the pane. fromFocus remembers
// which session the menu belongs to; the list cursor it would otherwise
// follow is not what is on screen there.
func (m *Model) openQuickMode() {
	m.errBar.text = ""
	m.quick = quickState{active: true, fromFocus: m.mode == modeFocus, closeAfterSend: m.quickCloseAfterSend()}
}

// handleQuickKey runs while the hotkey menu is docked in the sidebar: arrows
// keep moving the selection on the list, a snippet's key sends it, and the
// key that opened the menu, or esc, closes it. Nothing is typed, so a key
// that names no snippet does nothing rather than reaching some other binding
// behind the menu.
func (m *Model) handleQuickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.quick.fromFocus {
		return m.handleFocusQuickKey(msg)
	}
	context := keymap.ContextList
	if action, bound := m.action(context, msg); bound {
		switch action {
		case keymap.ToggleConversation:
			return m, m.toggleConversation()
		case keymap.QuickInput:
			m.quick.active = false
			return m, nil
		case keymap.Rescind:
			if m.canRescindLatestSubmission() {
				return m.rescindLatestSubmission()
			}
		}
	}
	if candidates := m.autoSuggestions(); len(candidates) > 0 {
		if msg.String() == "ctrl+y" {
			no := false
			model, cmd := m.sendSnippetToSelected(snippets.Snippet{Text: candidates[0], AutoSubmit: &no})
			m.quick.suggestions = nil
			m.autoSuggestSeq++
			return model, cmd
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
	snip, ok := m.menuSnippetFor(msg.String())
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

// handleFocusQuickKey runs the same menu opened from inside a focused
// session. The target is the focused session, never the list cursor, so the
// arrows that retarget the menu on the list do nothing here: moving the
// cursor under a session being typed into would aim the next key at a row
// nobody is looking at.
func (m *Model) handleFocusQuickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if action, bound := m.action(keymap.ContextFocus, msg); bound {
		switch action {
		case keymap.QuickInput:
			m.quick.active = false
			return m, nil
		case keymap.ToggleConversation:
			return m, m.toggleConversation()
		case keymap.Rescind:
			if m.canRescindLatestSubmission() {
				return m.rescindLatestSubmission()
			}
		}
	}
	if candidates := m.autoSuggestions(); len(candidates) > 0 {
		if msg.String() == "ctrl+y" {
			no := false
			model, cmd := m.sendSnippetToFocused(snippets.Snippet{Text: candidates[0], AutoSubmit: &no})
			m.quick.suggestions = nil
			m.autoSuggestSeq++
			return model, cmd
		}
	}
	if msg.String() == "esc" {
		m.quick.active = false
		return m, nil
	}
	snip, ok := m.menuSnippetFor(msg.String())
	if !ok {
		return m, nil
	}
	if m.quick.closeAfterSend {
		m.quick.active = false
	}
	return m.sendSnippetToFocused(snip)
}

// menuSnippetFor reads a key pressed in the menu as the snippet it names:
// the menu is the one place a bare key is free to mean a snippet, because
// nothing else there is listening for it.
func (m *Model) menuSnippetFor(key string) (snippets.Snippet, bool) {
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
