package ui

import tea "charm.land/bubbletea/v2"

// Two sessions in a conversation with each other -- a build in one, the
// agent that broke it in the other -- are the pair an operator crosses most,
// and the board makes that the most expensive move there is: find the other
// row again through the groups, the folds and whatever the filter is doing,
// every single time. l is the terminal multiplexer's own last-window
// gesture, and it means the same thing here.

// noteFocused records which session focus mode is on and which one it was on
// before that. Held as ids rather than as row indices because the rail is
// rebuilt under the operator constantly -- a poll reorders it, a fold moves
// it, a filter drops half of it -- and an index that survived any of those
// would point at somebody else's session.
func (m *Model) noteFocused(id string) {
	if id == "" || id == m.focusedID {
		return
	}
	m.prevFocusID, m.focusedID = m.focusedID, id
}

// focusLastPane goes back to the session focused before this one, and going
// back again returns here: the pair swaps rather than walking a history, so
// the key is the same one press whichever of the two you are on.
//
// It refuses rather than guesses. A session that has been archived or has
// left the board is gone from the pair, and jumping to whatever row happens
// to sit at its old place would focus the wrong agent -- which on a board
// where the next key goes into a pane is the one outcome worth a refusal.
func (m *Model) focusLastPane() (tea.Model, tea.Cmd) {
	if m.prevFocusID == "" {
		m.errBar.text = "no session focused before this one yet"
		return m, nil
	}
	index := -1
	for i, row := range m.rows {
		if row.isSession() && row.sess.ID == m.prevFocusID {
			index = i
			break
		}
	}
	if index < 0 {
		m.errBar.text = "the session you were on before is not on the board"
		return m, nil
	}
	m.cursor = index
	m.rebuildRows()
	return m.focusSelected()
}
