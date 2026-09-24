package ui

import (
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Whose question is it?
//
// Triage is the queue of what needs a person, and it drew every child that
// had stopped, on the reasoning that a child blocked on a question is exactly
// what the view exists to surface. That was right while nothing else could
// answer one. It is not right now: a child's question goes to its parent, and
// the parent -- which chose the task and wrote the prompt -- answers it. Nine
// children of one live parent put nine rows in front of the operator for nine
// questions they were never going to be the one to answer, and the fan-out's
// own owner is one row further down the same queue.
//
// So a child whose parent is on it stays folded in triage, and one whose
// parent is not still surfaces. Nobody is on it when the parent is dead,
// archived, or waiting on a person itself: a parent standing on its own
// question is in the queue, not working through it.
//
// Nor when the parent's pane has gone. A row keeps the status of its last
// poll, so a session whose CLI has exited reads as idle until something
// notices -- and an idle row with no process behind it answers nothing. Live
// on the board is the test, not the status the row happens to carry.
//
// And only a wait the parent can actually take off it. answer_session types
// keys into a dialog; it cannot grant a permission prompt, which asks whether
// this program may do something and is the operator's to answer, and it has
// nothing to press on a plain-text question. Folding one of those away left
// it hidden from the only person who could answer it, for the whole grace
// period, on the strength of a message telling the parent it could not help.
//
// And the fold is not forever. A parent that has been told and has not acted
// is indistinguishable, from here, from one that never will, so a question
// left standing past ownedGrace goes back to the operator. Hiding it for good
// on the strength of a message somebody may never read would be trading one
// missed question for a silent one.

// ownedGrace is how long a parent gets to deal with its child's question
// before the operator is shown it anyway. Long enough for a parent mid-turn
// to finish and read its inbox, short enough that a wedged one does not sit
// on a blocked child for an afternoon.
const ownedGrace = 15 * time.Minute

// parentOwns reports whether child's question is its parent's to answer right
// now, which is what lets triage leave it out.
func (m *Model) parentOwns(child store.Session, now time.Time, live map[string]bool) bool {
	// Whose question it is, is whose spawn it was. Reading it off the tree
	// folded a grandchild's question away because a root was live, when the
	// session that could actually answer it was somebody else entirely.
	spawner := store.SpawnerOf(child)
	if spawner == "" {
		return false
	}
	parent, ok := m.sessionByID(spawner)
	if !ok || parent.Archived || parent.Status == status.Dead {
		return false
	}
	// A listing that could not be taken says nothing: absent from a scan that
	// never happened is not evidence a pane is gone, and the cost of being
	// wrong here is a hidden question.
	if live != nil && !live[parent.ID] {
		return false
	}
	// A parent blocked on its own question answers nothing until somebody
	// answers that one, so its children are the operator's after all --
	// unless an extension is answering the parent's.
	if m.needsPerson(parent) {
		return false
	}
	if !m.answerableWait[child.ID] {
		return false
	}
	if !child.LastStatusAt.IsZero() && now.Sub(child.LastStatusAt) > ownedGrace {
		return false
	}
	return true
}
