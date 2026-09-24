package ui

import (
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/store"
)

// Floating a block to the head of its group.
//
// An extension can launch a helper whose role floats its parent: a helper
// holding a question for the operator, say, is only worth launching if the
// operator sees the work it belongs to. So while such a helper is
// unarchived, the whole top-level block it sits in -- the top-level session
// and everything drawn nested under it -- moves to the head of its group.
// Nothing else moves: the floated blocks keep their order among themselves,
// and so does the rest. The store knows nothing of this; it is how the list
// is drawn, not where a session is filed.

// floatedRoots is the top-level session of every block holding the parent
// of an unarchived helper whose role floats it.
func floatedRoots(sessions []store.Session) map[string]bool {
	byID := make(map[string]store.Session, len(sessions))
	for _, sess := range sessions {
		byID[sess.ID] = sess
	}
	var roots map[string]bool
	for _, sess := range sessions {
		if sess.Archived || sess.ParentID == "" || sess.Role == "" {
			continue
		}
		if !sessionhooks.Role(sess.Role).FloatParent {
			continue
		}
		if roots == nil {
			roots = map[string]bool{}
		}
		roots[blockRoot(sess.ParentID, byID)] = true
	}
	return roots
}

// blockRoot is the top-level session that id is drawn under: its furthest
// ancestor still on the board. The walk is bounded, so a cycle the store
// should never hold cannot hang a redraw.
func blockRoot(id string, byID map[string]store.Session) string {
	for range childDepthCap + 1 {
		sess, ok := byID[id]
		if !ok || sess.ParentID == "" {
			return id
		}
		if _, ok := byID[sess.ParentID]; !ok {
			return id
		}
		id = sess.ParentID
	}
	return id
}

// floatBlocks moves the sessions in a floated block to the head of their
// group, keeping every other order. sessions may hold several groups in any
// interleaving: each group's floated sessions take its first positions, so
// the groups stay where they were. A session is in a floated block when its
// root is in roots, reading roots from all, which may hold sessions the
// slice does not.
func floatBlocks(sessions, all []store.Session, roots map[string]bool) []store.Session {
	if len(roots) == 0 {
		return sessions
	}
	byID := make(map[string]store.Session, len(all))
	for _, sess := range all {
		byID[sess.ID] = sess
	}
	floats := func(sess store.Session) bool { return roots[blockRoot(sess.ID, byID)] }
	positions := map[string][]int{}
	var groups []string
	for i, sess := range sessions {
		if _, seen := positions[sess.Group]; !seen {
			groups = append(groups, sess.Group)
		}
		positions[sess.Group] = append(positions[sess.Group], i)
	}
	out := make([]store.Session, len(sessions))
	for _, group := range groups {
		at := positions[group]
		ordered := make([]store.Session, 0, len(at))
		for _, i := range at {
			if floats(sessions[i]) {
				ordered = append(ordered, sessions[i])
			}
		}
		for _, i := range at {
			if !floats(sessions[i]) {
				ordered = append(ordered, sessions[i])
			}
		}
		for k, i := range at {
			out[i] = ordered[k]
		}
	}
	return out
}
