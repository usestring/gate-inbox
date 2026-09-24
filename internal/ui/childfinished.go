package ui

import (
	"errors"
	"fmt"
	"time"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Telling a parent its child has come to rest.
//
// A parent hears when its child stops on a question. It hears nothing when
// the child finishes, which is the other half of the same problem: a session
// that fanned out five children and did not block on them has no signal that
// any of them are done, so it either polls list_sessions on a hunch or waits
// for a person to tell it. wait_for_session is the existing answer and it is
// the wrong shape for a fan-out -- it blocks on one named child, so a parent
// that wants to keep working while five run cannot use it, and a parent that
// uses it is doing nothing until that one child returns.
//
// The message is a fact, not a result. What the child actually produced is in
// its own transcript, and read_session is how the parent gets it: a status is
// the board's reading of a screen, so "finished" means the turn ended, never
// that the work is right.
//
// Errored and dead are relayed for the stronger version of the same reason. A
// child that died will not come back on its own, and a parent that never
// learns that waits for work nobody is doing.

// restRelay is what a parent is told about each status a child can come to
// rest in. Keyed by status so a status that grows a new meaning cannot fall
// into a default and be described wrongly, and carrying the whole message
// rather than a phrase: a dead child has no turn that ended and no prompt to
// send to, so the tail that suits the other two is false for it twice over.
var restRelay = map[string]string{
	status.Finished: "has finished its turn. Read what it did with read_session on %[2]s before you rely " +
		"on it: this says the turn ended, not that the work is right. If you still need something from " +
		"it, send_session puts your next instruction in its prompt.",
	status.Errored: "has stopped with an error. Read what it says with read_session on %[2]s: the board " +
		"read a screen, so what actually failed is on that screen and not here. If it can go on from " +
		"there, send_session puts your next instruction in its prompt.",
	status.Dead: "is gone: its pane has exited and it will not come back on its own. Its last screen is " +
		"still on the row -- read_session on %[2]s shows it -- but it will take no message, so " +
		"send_session cannot reach it. revive_session brings the row back with the conversation it held.",
}

// relayChildRest tells sess's parent that sess has come to rest.
//
// On the transition and only the transition, like the question relay beside
// it. The inbox's fingerprint window is the second guard and matters more
// here: this text does not vary, so a child a parent drives turn by turn --
// working, finished, working, finished -- is one message per window rather
// than one per turn.
func (p *poller) relayChildRest(sess store.Session, newStatus string) error {
	ending, atRest := restRelay[newStatus]
	// The spawner hears, not the row's parent: see relayChildQuestion.
	spawner := store.SpawnerOf(sess)
	if !atRest || spawner == "" || sess.Archived {
		return nil
	}
	// A session's own terminal is a child row too, and closing one is not an
	// event: the shell was opened to be used and then closed, by whoever
	// opened it. It reaches rest as dead and nothing else, since a shell
	// matches no status patterns.
	if p.shellTools[sess.Tool] {
		return nil
	}
	parent, err := p.store.Get(spawner)
	if err != nil {
		return ignoreDeletedSession(err)
	}
	if parent.Archived || parent.Status == status.Dead {
		return nil
	}
	body := childRestMessage(sess, ending)
	// Under this child's own subject: a child that finishes, is sent on and
	// finishes again leaves its parent the current notice rather than a
	// stale one queued ahead of it.
	_, _, err = p.store.Enqueue(store.InboxMessage{
		SessionID:   parent.ID,
		SenderID:    sess.ID,
		SenderName:  sess.Name,
		Body:        body,
		Fingerprint: textfmt.Fingerprint(body),
		Subject:     store.ChildRestSubject(sess.ID),
		SentAt:      time.Now(),
	}, store.DefaultInboxLimits)
	if err != nil {
		logging.Info("child rest not relayed to its parent",
			"session", sess.ID, "parent", parent.ID, "status", newStatus, logging.Err(err))
		// A full queue is the one refusal that loses news: the parent is
		// never told this child stopped, and nothing retries. It reaches the
		// operator, since the sender is a child that has just gone quiet and
		// has no turn left to read an error in. The other two lose nothing --
		// a duplicate means the same news is already queued, and the
		// per-minute cap only bites a child relaying repeatedly, which the
		// subject above has already reduced to one live notice.
		if errors.Is(err, store.ErrInboxFull) {
			return fmt.Errorf("%s finished and %s was not told: its queue is full at %d messages it has not read",
				sess.Name, parent.Name, store.DefaultInboxLimits.QueueCap)
		}
		return nil
	}
	logging.Info("relayed a child's rest to its parent",
		"session", sess.ID, "parent", parent.ID, "status", newStatus)
	return nil
}

// childRestMessage is what the parent reads: which child, how it came to
// rest, and where the actual work is. It offers no summary of its own,
// because the board has none to give -- it read a screen.
func childRestMessage(sess store.Session, ending string) string {
	return fmt.Sprintf("%s (session %s), which you spawned, ", sess.Name, sess.ID) +
		fmt.Sprintf(ending, sess.Name, sess.ID)
}
