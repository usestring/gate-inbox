package ui

import tea "charm.land/bubbletea/v2"

// Two sessions in a conversation with each other -- a build in one, the
// agent that broke it in the other -- are the pair an operator crosses most,
// and the board makes that the most expensive move there is: find the other
// row again through the groups, the folds and whatever the filter is doing,
// every single time. l is the terminal multiplexer's own last-window
// gesture, and it means the same thing here, extended to a short walk
// rather than a single swap.

// lastPaneHistoryLimit is how many previously focused sessions the board
// remembers. Pressing l repeatedly walks back through them, newest first,
// so a run across several panes is unwindable without re-finding each row.
const lastPaneHistoryLimit = 10

// noteFocused records which session focus mode is on and pushes the one it
// was on before that onto the walk. Held as ids rather than as row indices
// because the rail is rebuilt under the operator constantly -- a poll
// reorders it, a fold moves it, a filter drops half of it -- and an index
// that survived any of those would point at somebody else's session.
func (m *Model) noteFocused(id string) {
	if id == "" || id == m.focusedID {
		return
	}
	if m.focusedID != "" {
		// Drop older visits to the pane being entered, so the walk never
		// loops back through the pane it just arrived on: without this a
		// return across two panes ping-pongs through duplicate entries
		// rather than walking further back.
		kept := make([]string, 0, len(m.focusHistory)+1)
		for _, prev := range m.focusHistory {
			if prev != id {
				kept = append(kept, prev)
			}
		}
		kept = append(kept, m.focusedID)
		if len(kept) > lastPaneHistoryLimit {
			kept = append([]string(nil), kept[len(kept)-lastPaneHistoryLimit:]...)
		}
		m.focusHistory = kept
	}
	m.focusedID = id
	m.syncPrevFocus()
}

// syncPrevFocus keeps the one-step read in step with the walk.
func (m *Model) syncPrevFocus() {
	if len(m.focusHistory) == 0 {
		m.prevFocusID = ""
		return
	}
	m.prevFocusID = m.focusHistory[len(m.focusHistory)-1]
}

// focusLastPane goes back to the session focused before this one, and
// pressing it again keeps walking back through the ones before that, up to
// the limit noteFocused remembers.
//
// It refuses rather than guesses. A session that has been archived or has
// left the board is skipped rather than landed on, and jumping to whatever
// row happens to sit at its old place would focus the wrong agent -- which
// on a board where the next key goes into a pane is the one outcome worth
// a refusal.
func (m *Model) focusLastPane() (tea.Model, tea.Cmd) {
	if len(m.focusHistory) == 0 {
		m.errBar.text = "no session focused before this one yet"
		return m, nil
	}
	destIndex, index := -1, -1
	for i := len(m.focusHistory) - 1; i >= 0; i-- {
		id := m.focusHistory[i]
		if id == m.focusedID {
			continue
		}
		for r, row := range m.rows {
			if row.isSession() && row.sess.ID == id {
				destIndex, index = i, r
				break
			}
		}
		if destIndex >= 0 {
			break
		}
	}
	if destIndex < 0 {
		m.errBar.text = "the sessions you were on before are not on the board"
		return m, nil
	}
	destID := m.focusHistory[destIndex]
	remaining := append([]string(nil), m.focusHistory[:destIndex]...)
	m.cursor = index
	m.rebuildRows()
	result, cmd := m.focusSelected()
	if m.mode != modeFocus || m.focusedID != destID {
		return result, cmd
	}
	m.focusHistory = remaining
	m.syncPrevFocus()
	return result, cmd
}
