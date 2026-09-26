package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

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

// autoProceeds reports whether this keystroke is inside the one situation the
// setting describes: a queue being drained, from inside a session.
//
// Outside triage there is no queue to proceed along -- advanceTriage walks
// triage's own order -- and outside focus the keys this hooks do not reach a
// pane at all.
func (m *Model) autoProceeds() bool {
	// The gate is the setting armed on purpose and for as long as the mode
	// lasts, so it does not consult the stored one: a drain the operator
	// asked for by name is not something they should then have to have
	// turned on in settings beforehand. See gate.go.
	if m.gate.on {
		return m.mode == modeFocus
	}
	return m.autoProceed && m.triage && m.mode == modeFocus
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
func (m *Model) moveOnFrom(sess store.Session) tea.Cmd {
	leave := m.leaveFocus()
	if next := m.advanceTriage(sess.ID); next != nil {
		return tea.Batch(leave, next)
	}
	// Nothing left to hand over. Outside the gate the empty rail the
	// operator lands on is the answer; the gate has put that rail away, and
	// a drain that has run dry is the drain being over, so it ends here and
	// says so rather than leaving an armed mode with no queue in it.
	if m.gate.on {
		drained := m.disarmGate()
		m.errBar.text = "gate drained — nothing else is waiting on you or idle"
		return tea.Batch(leave, drained)
	}
	return leave
}
