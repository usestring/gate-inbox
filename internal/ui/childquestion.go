package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Telling a parent its child has stopped on a question.
//
// Nesting a spawn under the session that asked for it drew a tree and did
// nothing else: the parent was never told when the work it delegated stopped,
// so a fan-out of nine could sit on nine questions while the session that
// wanted the answers went on believing they were running. The question landed
// in front of a person instead -- one who did not choose the task, did not
// write the prompt, and has to reconstruct both before they can answer.
//
// So the row's own parent hears about it. This is a message, not an
// escalation: it goes through the inbox every agent already reads, is
// delivered when the parent is at rest like any other, and says how to
// answer. If the parent never gets to it the question still stands on the
// board for a person, exactly as before -- nothing here takes the operator
// out of the loop, it just stops making them the first resort.
//
// A wait this cannot read is relayed too, and says less. A permission prompt
// asks whether this program may do something, which is the operator's to
// answer and not a parent's, and a plain-text question has no options to pick
// -- answer_session takes neither. But "your child is stopped and you cannot
// answer it" is still the thing the parent has to know: told nothing, it goes
// on waiting on work that has stopped, which is the whole failure this file
// exists to end. What the pane holds is deliberately left out of that message:
// these screens carry live API keys, which is why even the log does not take
// pane text by default.

// relayChildQuestion tells sess's parent that sess is holding a question.
//
// Called on the transition into waiting, and only then: a dialog that stands
// for an hour is one message, not one per poll pass. The inbox's dedupe
// window is the second guard, on the fingerprint of the question itself, so a
// child that goes waiting, is answered, and asks the same thing again inside
// the window is not relayed twice either.
func (p *poller) relayChildQuestion(sess store.Session, newStatus, pane string) error {
	// The session that spawned it, not the row it is drawn under: the tree
	// carries one level, so a grandchild's questions all went to a root that
	// had not assigned the work and could not answer them either.
	spawner := store.SpawnerOf(sess)
	if newStatus != status.Waiting || spawner == "" || sess.Archived {
		return nil
	}
	parent, err := p.store.Get(spawner)
	if err != nil {
		return ignoreDeletedSession(err)
	}
	if parent.Archived || parent.Status == status.Dead {
		return nil
	}
	// A dialog this can read is relayed with its question and the call that
	// answers it; anything else is relayed as the fact of the stop alone.
	body := childWaitMessage(sess)
	if held, ok := dialog.Parse(pane); ok {
		body = childQuestionMessage(sess, held)
	}
	_, _, err = p.store.Enqueue(store.InboxMessage{
		SessionID:   parent.ID,
		SenderID:    sess.ID,
		SenderName:  sess.Name,
		Body:        body,
		Fingerprint: textfmt.Fingerprint(body),
		SentAt:      time.Now(),
	}, store.DefaultInboxLimits)
	if err != nil {
		// A full or rate-limited queue is the inbox working: the parent is
		// already holding more than it has read. The question keeps standing
		// on the board, which is where it would have been anyway.
		logging.Info("child question not relayed to its parent",
			"session", sess.ID, "parent", parent.ID, logging.Err(err))
		return nil
	}
	logging.Info("relayed a child's question to its parent",
		"session", sess.ID, "parent", parent.ID)
	return nil
}

// childQuestionMessage is what the parent reads: whose question it is, what
// was asked in the words the pane shows, and the one call that answers it.
func childQuestionMessage(sess store.Session, held dialog.Dialog) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s (session %s), which you spawned, has stopped on a question and is "+
		"waiting for an answer:\n\n", sess.Name, sess.ID)
	out.WriteString(strings.TrimRight(held.Question(), "\n"))
	fmt.Fprintf(&out, "\n\nAnswer it with answer_session on session %s -- the text of the option "+
		"to pick, or your own words to type instead. It is your fan-out, so answer it yourself "+
		"where the task you gave it settles the question, and only put it to the operator where "+
		"it genuinely needs them.", sess.ID)
	return out.String()
}

// childWaitMessage is what the parent reads about a stop this cannot answer
// for it: which child, that answer_session will not take it, and the two
// things the parent can actually do about it. It names no part of the pane,
// so a permission prompt quoting a command with a key in it does not travel
// into another session's context.
//
// Its text does not vary with the screen, so the inbox's fingerprint window
// collapses a child that stops, is answered and stops again inside that
// window into one message. That is the same bargain the question path takes,
// and it errs the right way: the row is on the board throughout.
func childWaitMessage(sess store.Session) string {
	return fmt.Sprintf("%s (session %s), which you spawned, has stopped and is waiting for input. "+
		"It is holding a permission prompt or a question with no options to pick, so answer_session "+
		"cannot answer it and only a person at its pane can. If you are blocked on what it was doing, "+
		"put that to the operator yourself rather than going on waiting; otherwise carry on without it.",
		sess.Name, sess.ID)
}
