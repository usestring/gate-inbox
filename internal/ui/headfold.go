package ui

// A header that folds the session it heads.
//
// An extension whose header is the thing a session is doing -- a charter over
// the agent working it -- can ask for the header to be that work's row, the
// way a group is its sessions' row: the session and everything under it fold
// behind the header until the operator unfolds it. While folded the header
// stands in for the session. It carries the session's status, the triage
// queue and the jumps hand it over, and the keys that act on a session act on
// the one it stands for.

import (
	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/store"
)

// headOpenPrefix namespaces an unfolded header in the tree's collapsed set,
// beside the group, work and child folds kept there. The set keeps only what
// is true, and a header is folded until someone opens it, so what is kept is
// the opening.
const headOpenPrefix = "headopen:"

const (
	headFoldedMark   = "▸ "
	headUnfoldedMark = "▾ "
)

// foldsHeaders is whether the owner's header rows fold the session they head.
func (m *Model) foldsHeaders(owner string) bool {
	if !m.headsRows(owner) {
		return false
	}
	for _, ui := range m.extUIs {
		if ui.Owner == owner {
			return ui.FoldHeaders
		}
	}
	return false
}

// headFoldable is whether a session has a header over it that folds it.
func (m *Model) headFoldable(sessID string) bool {
	for _, header := range m.extHeaders[sessID] {
		if m.foldsHeaders(header.owner) {
			return true
		}
	}
	return false
}

// headFolded is whether a session is drawn as its header alone. A search
// shows what it matched, so it unfolds every header; so does the cursor
// sitting on something under the session, which has to be drawn to be on.
func (m *Model) headFolded(sess store.Session, query string) bool {
	if query != "" || !m.headFoldable(sess.ID) || m.collapsed[headOpenPrefix+sess.ID] {
		return false
	}
	return !m.cursorAncestors()[sess.ID]
}

// setHeadOpen unfolds or folds the session under a header, and keeps the
// cursor on that session's entry, which a fold turns from its row into its
// header's.
func (m *Model) setHeadOpen(sessID string, open bool) {
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	if open {
		m.collapsed[headOpenPrefix+sessID] = true
	} else {
		delete(m.collapsed, headOpenPrefix+sessID)
	}
	m.persistCollapsed()
	m.rebuildRows()
	if !open {
		m.focusSession(sessID)
	}
}

// headFoldAction answers the fold keys on a folding header: the step-in key
// unfolds it, the step-out key folds it. In triage the open key on a folded
// one opens the session it stands for -- triage hands over sessions, and the
// header is that session's place in the queue.
func (m *Model) headFoldAction(entry treeRow, action keymap.Action) (tea.Model, tea.Cmd, bool) {
	if !m.foldsHeaders(entry.head) {
		return m, nil, false
	}
	switch action {
	case keymap.StepIn:
		if entry.stands {
			m.setHeadOpen(entry.sess.ID, true)
		}
		return m, nil, true
	case keymap.StepOut:
		if !entry.stands {
			m.setHeadOpen(entry.sess.ID, false)
		}
		return m, nil, true
	case keymap.Open:
		if m.triage && entry.stands {
			// The session's own warning before it is opened stands,
			// whichever row it is opened from.
			if m.holdOpen(action) {
				return m, nil, true
			}
			model, cmd := m.focusSelected()
			return model, cmd, true
		}
	}
	return m, nil, false
}

// foldUnderHead folds a session row back behind its header, for the step-out
// key on the row itself. It reports whether there was a header to fold under.
func (m *Model) foldUnderHead(entry treeRow) bool {
	if !entry.isSession() || !m.headFoldable(entry.sess.ID) || !m.collapsed[headOpenPrefix+entry.sess.ID] {
		return false
	}
	m.setHeadOpen(entry.sess.ID, false)
	return true
}

// openHeadsAbove unfolds every folding header over a session's ancestors, so
// a jump to a session under a folded one arrives at its row. The session's
// own header stays as it is: folded, it is the session's entry already.
func (m *Model) openHeadsAbove(sess store.Session) {
	parents := make(map[string]string, len(m.sessions))
	for _, s := range m.sessions {
		parents[s.ID] = s.ParentID
	}
	for id, seen := sess.ParentID, map[string]bool{}; id != "" && !seen[id]; id = parents[id] {
		seen[id] = true
		if m.headFoldable(id) {
			if m.collapsed == nil {
				m.collapsed = map[string]bool{}
			}
			m.collapsed[headOpenPrefix+id] = true
		}
	}
}
