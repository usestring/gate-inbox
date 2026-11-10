package ui

import (
	"time"

	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A chore rest is a session at rest whose latest turn was a board chore
// rather than work: a prompt-cache keep-alive that an extension typed in and
// the agent answered with "ok". The turn ends in a fresh finish, which
// re-arms the row as unseen news, lapses the mute the operator's last visit
// left on it, and -- because the extension keeping it warm also publishes the
// cache's expiry as a queue deadline -- used to put it at the head of the
// queue. A board with a dozen warm sessions then handed the same few over
// again and again while the sessions asking questions waited behind them.
//
// Nothing new is in such a finish, so triage reads it as the lowest walkable
// state: still on the rail and still reachable at the end of a drain, but
// never ahead of a question, a real finish or an idle row, and never a reason
// for the drain to pick itself back up. The mark lapses on its own the moment
// anything else reaches the session: the operator's own turn, or a message
// from another session or the operator.

// choreRestsOf reads, for each session, whether the latest message typed into
// it was a chore delivered after the operator's last turn on it, keyed to when
// it was delivered.
func choreRestsOf(sessions []store.Session, delivered map[string]store.InboxMessage) map[string]time.Time {
	out := map[string]time.Time{}
	for _, sess := range sessions {
		msg, ok := delivered[sess.ID]
		if !ok || msg.SenderID == store.HumanSenderID {
			continue
		}
		if search.ClassifyTurn(msg.Body) != search.TurnChore {
			continue
		}
		if !msg.DeliveredAt.After(sess.OperatorAt) {
			continue
		}
		out[sess.ID] = msg.DeliveredAt
	}
	return out
}

// isChoreRest is whether triage should read sess as a chore rest: at rest,
// with no extension rank or escalation saying what the queue is about, and
// its latest turn a chore. See choreRestsOf.
func (m *Model) isChoreRest(sess store.Session) bool {
	if _, ok := m.choreRests[sess.ID]; !ok {
		return false
	}
	if sess.Status != status.Finished && sess.Status != status.Idle {
		return false
	}
	att := m.extAttention[sess.ID]
	return att.Rank.tier() == "" && !att.NeedsPerson
}

// choreSince is whether a mute on sess's state at at has seen everything the
// session holds: its latest turn was a chore delivered after at, and the
// operator has given it no turn since at. A turn of theirs after the mute
// started real work, whose finish the chore now sits on top of.
func (m *Model) choreSince(sess store.Session, at time.Time) bool {
	delivered, ok := m.choreRests[sess.ID]
	return ok && m.isChoreRest(sess) && !delivered.Before(at) && !sess.OperatorAt.After(at)
}
