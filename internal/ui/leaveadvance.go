package ui

import "github.com/usestring/gate-inbox/internal/store"

// Walking the queue -- answer a session, leave it, land in the next one that
// needs a person -- is already built, and is reachable exactly one way: arm
// triage with i first, then leave with ctrl+q. Nothing on the board says so.
// Somebody who answers a session and presses ctrl+q lands back on the list
// every time and concludes the manager cannot do it, which is what happened
// in the thread this setting comes from.
//
// So the walk gets a setting of its own. It is the same advance triage
// drives; what changes is that ctrl+q reaches it without the queue having to
// be armed first.

const (
	leaveSetting = "on_leaving"
	// leaveToList is the default and today's behaviour: ctrl+q returns to
	// the board and stops there.
	leaveToList = "list"
	// leaveToNext carries on into the next session needing a person.
	leaveToNext = "next"
)

// leaveModes is the setting's cycle order.
var leaveModes = []string{leaveToList, leaveToNext}

func storedLeaveMode(st *store.Store) string {
	chosen, err := st.Setting(leaveSetting)
	if err != nil {
		return leaveToList
	}
	return normalizeLeaveMode(chosen)
}

func normalizeLeaveMode(chosen string) string {
	for _, mode := range leaveModes {
		if chosen == mode {
			return mode
		}
	}
	return leaveToList
}

// advancesOnLeave reports whether ctrl+q out of a focused session goes on to
// the next one needing a person rather than back to the board. Triage is a
// queue being drained on purpose and always advances; the setting is what
// gives the same walk to somebody who has not armed one.
func (m *Model) advancesOnLeave() bool {
	return m.triage || m.leaveMode == leaveToNext
}

// leaveAdvanceHint is what the focused footer calls ctrl+q when it does not
// merely return. Triage names its scope because the rail's badge is off
// screen while a pane is focused; the setting has no scope to name.
func (m *Model) leaveAdvanceHint() string {
	if m.triage && m.triageScope != "" {
		return "mute this, next in " + baseName(m.triageScope)
	}
	return "mute this, next needing input"
}
