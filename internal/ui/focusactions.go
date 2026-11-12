package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/clipboard"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/store"
)

// dismissFocused is the list's dismiss key reached from inside the pane, and
// the skip a one-at-a-time drain is missing without it: otherwise a session
// that wants nothing from the operator has to be left first and dismissed
// from the row afterwards, which is two gestures and a trip back to the list.
//
// It is the list's key rather than a second reading of it -- a finished
// session is marked idle, anything else is muted -- and then the handover
// the answer keys already use, so a skip and an answer leave the queue in
// the same state and land in the same next session.
func (m *Model) dismissFocused(sess store.Session) tea.Cmd {
	_, cmd := m.dismissSelected()
	return tea.Batch(cmd, m.handOverFocused(sess))
}

// focusBack reopens the session the drain last stepped past. It is the walk
// `l` uses, reached from inside the pane: a drain advances by muting and
// moving on, so coming back also lifts the mute, and a session skipped by
// mistake is on the queue again rather than merely on screen.
func (m *Model) focusBack() (tea.Model, tea.Cmd) {
	before := m.focusedID
	next, cmd := m.focusLastPane()
	if m.focusedID != "" && m.focusedID != before {
		m.unmute(m.focusedID)
	}
	return next, cmd
}

// copySessionID puts the agent's own conversation id on the clipboard, the id
// a `--resume` or a transcript path is built from. The manager's row id is not
// it: that names the pane, and the operator wants the conversation.
func (m *Model) copySessionID(sess store.Session) (tea.Model, tea.Cmd) {
	id := strings.TrimSpace(sess.AgentSessionID)
	if id == "" {
		m.errBar.text = m.displayName(sess) + " has no agent session id yet"
		return m, nil
	}
	return m, func() tea.Msg {
		if err := writeClipboard(id); err != nil {
			return sessionIDCopiedMsg{err: err}
		}
		return sessionIDCopiedMsg{id: id}
	}
}

// writeClipboard is the seam the copy-session-id key writes through, so a test
// can drive it without a real system clipboard.
var writeClipboard = clipboard.WriteText

// sessionIDCopiedMsg reports a finished clipboard write so the status line can
// confirm it, the way the pane's own copy selection does.
type sessionIDCopiedMsg struct {
	id  string
	err error
}

// boardActionsFromFocus are the list actions a focused pane answers by
// leaving for the board and running them there, the way attach already
// does. They are board-wide or about the row the pane is, so leaving first
// is what the operator would have done by hand; see the catalog's focus
// table for the keys and why each one is safe to take from the agent.
var boardActionsFromFocus = map[keymap.Action]bool{
	keymap.Triage:        true,
	keymap.Search:        true,
	keymap.QuickActions:  true,
	keymap.Settings:      true,
	keymap.NewGroup:      true,
	keymap.NewTerminal:   true,
	keymap.ReviveAll:     true,
	keymap.IngestReviews: true,
	keymap.UndoArchive:   true,
	keymap.RenameSelf:    true,
}

// runBoardActionFromFocus leaves the pane and runs action as the list
// would. The leave comes first so an action that opens a screen or a queue
// of its own -- triage focusing its first session -- opens it from the
// board rather than underneath the pane just left.
func (m *Model) runBoardActionFromFocus(action keymap.Action, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	leave := m.leaveFocus()
	press, _ := msg.(tea.KeyPressMsg)
	model, cmd := m.runListAction(action, press)
	return model, tea.Batch(leave, cmd)
}
