package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// autoProceedSetting is the drain's hands-free handover: with it on, the key
// that answers a focused session also hands it over, so a pass down the queue
// is answering one agent after another rather than answering and then
// remembering to press § between each pair.
//
// On is the default. Walking the queue is what the mode is for, and an
// operator who opened one has already said they mean to walk it, so the
// handover is the promise and stopping on each session is the part worth
// asking for. It was off for as long as it was on the argument that reading
// "done with this one" out of a keystroke is a guess. It no longer is: the
// handover waits for the answer to be seen landing in the session, and a key
// that lands nothing leaves the operator where they are. See landing.go. A
// queue that silently does not advance costs more than a missed handover: it
// reads as the mode being broken rather than as a setting being off, which is
// exactly how it was reported.
const autoProceedSetting = "triage_auto_proceed"

// autoProceedDefaultSetting records that a store has been through the flip
// above, so a deliberate "off" survives it.
//
// The value alone cannot say who wrote it. saveSettings writes every field on
// every save, so a store whose owner only ever changed the theme still
// carries an explicit triage_auto_proceed=off -- and a default that only read
// an UNSET value as on would reach nobody who had opened settings even once.
// So the flip is applied to the value itself, once per store. After it, off
// is the operator's own word and stays.
const (
	autoProceedDefaultSetting = "triage_auto_proceed_default_on"
	autoProceedDefaultDone    = "done"
)

// storedAutoProceed reads the persisted choice, applying the flip to a store
// that has not had it yet.
//
// On for a store that cannot be read: the default is what something with no
// answer should behave as, and the alternative is the silent no-advance this
// flip exists to end. A failed write leaves the marker unset so the next
// start tries again, which is the right way round -- a flip that did not
// persist should be retried, and one applied twice is the same flip.
func storedAutoProceed(st *store.Store) bool {
	migrated, err := st.Setting(autoProceedDefaultSetting)
	if err != nil {
		return true
	}
	if migrated != autoProceedDefaultDone {
		if err := st.SetSetting(autoProceedSetting, "on"); err == nil {
			_ = st.SetSetting(autoProceedDefaultSetting, autoProceedDefaultDone)
		}
		return true
	}
	chosen, err := st.Setting(autoProceedSetting)
	if err != nil {
		return true
	}
	return chosen == "on"
}

// autoProceeds uses the same queue choice as an explicit leave, so submitting
// and skipping agree on whether the operator is walking the sessions.
func (m *Model) autoProceeds() bool {
	return m.autoProceed && m.advancesOnLeave() && m.mode == modeFocus
}

// handOverFocused is the explicit handover: mute the session so the walk
// converges, leave it, and enter the next one that needs a person. The mute
// is what makes the queue shrink as it is drained -- the status the session
// is left showing lags a poll behind whatever was just done in it. See
// mute.go.
func (m *Model) handOverFocused(sess store.Session) tea.Cmd {
	m.mute(sess)
	return m.moveOnFrom(sess)
}

// handOverLanded is auto-proceed's handover, taken once the answer the
// operator gave sess has been seen landing at at. Its mute is keyed to that
// landing rather than to the state sess was left in; see muteUntilSeen.
func (m *Model) handOverLanded(sess store.Session, at time.Time) tea.Cmd {
	m.muteUntilSeen(sess, at)
	return m.moveOnFrom(sess)
}

// moveOnFrom leaves sess and enters the next session that needs a person.
// When the queue is drained the operator lands on the list with the drain
// still open (see triageResume), so work arriving on a later poll joins the
// same queue instead of waiting for another explicit pass.
func (m *Model) moveOnFrom(sess store.Session) tea.Cmd {
	leave := m.leaveFocus()
	if next := m.advanceTriage(sess.ID); next != nil {
		m.triageResume = false
		return tea.Batch(leave, next)
	}
	if m.triage {
		m.triageResume = true
	}
	return leave
}

// focusedStatus is the focused session's status as the board last had it,
// read before a poll pass replaces the rows.
func (m *Model) focusedStatus() (string, bool) {
	if m.mode != modeFocus {
		return "", false
	}
	sess, ok := m.selected()
	return sess.Status, ok
}

// handOverOnWork moves the drain on once a poll shows the focused session
// gone from before to working: the operator answered it and the agent took
// the answer. The answer check watches for the same thing from the key and
// usually gets there first, but it gives up after a few seconds and on any
// hook it reads as a fresh dialog, and the session it gave up on then held
// the operator for as long as the agent worked. The transition is not a
// guess, so it hands over whatever the check decided. A session already
// working when it was entered has not changed and keeps the operator.
func (m *Model) handOverOnWork(before string) tea.Cmd {
	if before == status.Working || !m.triage || !m.autoProceeds() {
		return nil
	}
	sess, ok := m.selected()
	if !ok || sess.Status != status.Working {
		return nil
	}
	return m.handOverFocused(sess)
}
