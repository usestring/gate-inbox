package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A mute is the operator saying "I have dealt with this one, walk past it".
//
// A drain only converges if the queue shrinks as it is walked, and triage's
// does not on its own: answering a session does not clear its status this
// instant, because the poller has to see the pane change first. So without
// a mute the third ctrl+q hands back the first session, still reading
// "waiting", and the walk cycles over work already done.
//
// A mute is deliberately not a stored flag on the session. It says nothing
// about the session; it records what the operator has already looked at, so
// it lives for as long as the drain does and no longer.
type muteMark struct {
	// status and at are the state the session was in when it was muted.
	// A mute covers that state only: the moment the session moves on --
	// a new question, a new error, a run that finishes -- it is a thing
	// the operator has not seen, and the mute lapses on its own. Nothing
	// has to remember to clear it.
	status string
	at     time.Time
}

// mute silences one session in the state it is in now. Muting a session
// twice is not an error: the second call re-reads the state, which is what
// makes muting a session that has since moved on cover the new state.
func (m *Model) mute(sess store.Session) {
	if m.muted == nil {
		m.muted = map[string]muteMark{}
	}
	m.muted[sess.ID] = muteMark{status: sess.Status, at: sess.LastStatusAt}
}

func (m *Model) unmute(id string) {
	delete(m.muted, id)
}

// isMuted reports whether triage should walk past this session, dropping a
// mark the session has outgrown as it goes so the map cannot accumulate
// stale entries for sessions that keep working. It is called from the rail's
// render as well as from the advance, which is why the drop happens here
// rather than in a sweep: one definition, and the marker the row paints can
// never disagree with the queue ctrl+q walks.
func (m *Model) isMuted(sess store.Session) bool {
	mark, ok := m.muted[sess.ID]
	if !ok {
		return false
	}
	if sess.Status == mark.status && !sess.LastStatusAt.After(mark.at) {
		return true
	}
	delete(m.muted, sess.ID)
	return false
}

// clearMutes starts a fresh pass. Entering triage is the gesture that asks
// for one -- the whole queue, from the top -- so the marks a previous drain
// left do not silently hide rows from a user who has just asked to see what
// needs them.
func (m *Model) clearMutes() {
	m.muted = nil
}

// pruneMutes drops marks for sessions the board no longer holds, so a long
// run of killed and archived sessions cannot grow the map without bound.
// Marks lapse on their own while a session lives; this is only for the ones
// that leave before they ever do.
func (m *Model) pruneMutes(sessions []store.Session) {
	if len(m.muted) == 0 {
		return
	}
	live := make(map[string]bool, len(sessions))
	for _, sess := range sessions {
		live[sess.ID] = true
	}
	for id := range m.muted {
		if !live[id] {
			delete(m.muted, id)
		}
	}
}

// dismissSelected is the row's "I have handled this" key. On a finished
// session it does what it always did -- marks it idle and acked, which is a
// real state change the store keeps -- and on a session that is waiting,
// errored or idle, where there is no state the manager may change on the
// operator's behalf, it mutes instead. Both readings are the same request:
// take this off my queue. Pressed a second time on a muted row it un-mutes,
// which is the only way back for a row silenced by mistake.
func (m *Model) dismissSelected() (tea.Model, tea.Cmd) {
	sess, ok := m.selected()
	if !ok || sess.Archived {
		return m, nil
	}
	m.errBar.text = ""
	if m.isMuted(sess) {
		m.unmute(sess.ID)
		return m, nil
	}
	if sess.Status == status.Finished {
		if err := m.store.AcknowledgeFinished(sess.ID); err != nil {
			m.errBar.text = err.Error()
			return m, nil
		}
		m.requestRefresh()
		return m, nil
	}
	if !m.triageWalkable(sess) {
		m.errBar.text = m.displayName(sess) + " is not waiting on you — nothing to dismiss"
		return m, nil
	}
	m.mute(sess)
	return m, nil
}

// triageHandoffKey is a one-press alias for ctrl+q, for a keyboard where the
// chord is awkward to reach: § sits unused on the ISO layout, and a drain is
// one gesture repeated until the queue is empty.
//
// Focused it is the alias whether or not triage is on, so the one key always
// means the same thing -- hand over in triage, back to the manager outside it
// -- rather than reaching the agent in one mode and not the other. That costs
// the pane a § it will almost never want, and buys an exit the operator does
// not have to think about. On the list it stays triage-only: outside a queue
// there is nothing to hand over, and nothing to leave.
const triageHandoffKey = "§"

// handOverSelected is the handover from the list. Focused, ctrl+q's handover
// is the way on through the queue; from the list the same request had no key
// at all -- "." mutes but stays put -- so a drain could only be walked from
// inside a session. This mutes the row the cursor is on and enters the next
// session that needs a person, which is the same gesture the focused key
// performs, minus the leaving.
func (m *Model) handOverSelected() (tea.Model, tea.Cmd) {
	if !m.triage {
		return m, nil
	}
	sess, ok := m.selected()
	if !ok || sess.Archived {
		return m, nil
	}
	m.errBar.text = ""
	// Only a session the drain would hand over is on the queue this walks,
	// so muting anything else would silence a row it was never going to
	// reach. Pressing on from such a row is still a request for the next
	// one that does need somebody, and an empty leftID scans from the top.
	leftID := ""
	if m.triageWalkable(sess) {
		m.mute(sess)
		leftID = sess.ID
	}
	if cmd := m.advanceTriage(leftID); cmd != nil {
		return m, cmd
	}
	// The queue is drained. Saying so is worth a line here in a way it is not
	// focused, where the same ending drops the operator back onto the list
	// and the empty rail is the answer; from the list nothing moves. A
	// session that refused to be entered has already said why, and that is
	// the more useful answer of the two.
	if m.errBar.text == "" {
		m.errBar.text = "nothing else in the queue is waiting on you or idle"
	}
	return m, nil
}
