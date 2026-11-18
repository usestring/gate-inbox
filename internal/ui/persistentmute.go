package ui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/store"
)

// A persistent mute is the operator's "keep this out of my triage", stored on
// the session rather than remembered for one pass. It is a different thing
// from the drain's ephemeral mark in mute.go: that one lapses the moment the
// pane changes and is cleared when triage is re-opened, so the walk
// converges; this one holds until somebody unmutes it. A muted session stays
// on the rail reading "muted", sorted last in triage, and the drain walks
// past it instead of handing it over.
//
// The flag reaches the store the same way priority does, because it is a
// fact about the work rather than about one pass, and outlives the process.

// mutedStatusLabel is what a muted row reads in place of its pane status.
// The pane still has a real status under it, and the board keeps reading it;
// the operator has simply said this row is not one they want triage to hand
// over, so the label says the thing that matters.
const mutedStatusLabel = "muted"

// A snooze is a mute with an end. The store holds the end beside the flag
// (store.Session.MutedUntil) and the board's poll lifts it, at that time or
// sooner if the session stops on a dialog or errors, so nothing here keeps
// a timer: everything below only writes the end and reads it back.

// snoozeSteps are the lengths the snooze key walks through before it comes
// back round to unmuted.
var snoozeSteps = []time.Duration{time.Hour, 4 * time.Hour, 24 * time.Hour}

// nextSnooze is the length one more press of the snooze key gives sess, or
// zero when the press unmutes it. It is read off how long the row has left
// rather than remembered, so the walk carries on from wherever a restart,
// the command or an agent's tool left the row. A step is taken while the row
// has at least the first step less than it left to run, so a press just
// after one moves on to the next rather than setting the same one again.
func nextSnooze(sess store.Session, now time.Time) time.Duration {
	if !sess.Muted || sess.MutedUntil.IsZero() {
		return snoozeSteps[0]
	}
	left := sess.MutedUntil.Sub(now)
	for _, step := range snoozeSteps[1:] {
		if left <= step-snoozeSteps[0] {
			return step
		}
	}
	return 0
}

// cycleSnoozeSelected moves the selected session one step along the snooze
// walk: an hour, four, a day, then unmuted.
func (m *Model) cycleSnoozeSelected() (tea.Model, tea.Cmd) {
	sess, ok := m.selected()
	if !ok || sess.Archived {
		return m, nil
	}
	now := time.Now()
	length := nextSnooze(sess, now)
	if length == 0 {
		return m.setMuted(sess, false)
	}
	return m.setMutedUntil(sess, true, now.Add(length))
}

// snoozeLabel is when a snoozed row comes back, as short as the distance
// allows: the clock time today, the weekday within the week, the date after.
func snoozeLabel(until, now time.Time) string {
	until = until.In(now.Location())
	year, month, day := now.Date()
	midnight := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
	switch {
	case until.Before(midnight.AddDate(0, 0, 1)):
		return "until " + until.Format("15:04")
	case until.Before(midnight.AddDate(0, 0, 7)):
		return "until " + until.Format("Mon 15:04")
	}
	return "until " + until.Format("Jan 2")
}

// toggleMuteSelected flips the selected session's persistent mute. Pressing it
// on a muted row brings it back, which is the only way to undo a mute.
func (m *Model) toggleMuteSelected() (tea.Model, tea.Cmd) {
	sess, ok := m.selected()
	if !ok || sess.Archived {
		return m, nil
	}
	return m.setMuted(sess, !sess.Muted)
}

// setMuted writes the persistent mute and moves the model with it. It is the
// one write both the board's own key and the skip key's un-mute go through,
// so every way back lands the same state.
func (m *Model) setMuted(sess store.Session, muted bool) (tea.Model, tea.Cmd) {
	return m.setMutedUntil(sess, muted, time.Time{})
}

// setMutedUntil is setMuted with an end: a zero until is a mute that holds
// until somebody lifts it, and any other makes it a snooze.
func (m *Model) setMutedUntil(sess store.Session, muted bool, until time.Time) (tea.Model, tea.Cmd) {
	var err error
	if until.IsZero() {
		err = m.store.SetMuted(sess.ID, muted)
	} else {
		err = m.store.SetSnoozed(sess.ID, until)
	}
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	// The flag moves on the model straight away so the row's label and the
	// triage queue move on this frame; the refresh confirms the store.
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].Muted, m.sessions[i].MutedUntil = muted, until
		}
	}
	switch {
	case !muted:
		m.errBar.text = m.displayName(sess) + " unmuted"
	case until.IsZero():
		m.errBar.text = m.displayName(sess) + " muted — out of triage until unmuted"
	default:
		m.errBar.text = m.displayName(sess) + " snoozed " + snoozeLabel(until, time.Now()) + " — back sooner if it asks or errors"
	}
	m.rebuildRows()
	m.requestRefresh()
	if muted && m.triage {
		return m, m.moveOnFrom(sess)
	}
	return m, nil
}

// unmuteAllListed lifts the persistent mute from every session in the current
// view. Like restoreAllDead it carries on past a failed write and names the
// first one.
func (m *Model) unmuteAllListed() (tea.Model, tea.Cmd) {
	unmuted := 0
	var firstErr string
	for _, sess := range m.searchedListedSessions() {
		if !sess.Muted || sess.Archived {
			continue
		}
		if err := m.store.SetMuted(sess.ID, false); err != nil {
			if firstErr == "" {
				firstErr = err.Error()
			}
			continue
		}
		for i := range m.sessions {
			if m.sessions[i].ID == sess.ID {
				m.sessions[i].Muted, m.sessions[i].MutedUntil = false, time.Time{}
			}
		}
		unmuted++
	}
	switch {
	case unmuted == 0 && firstErr == "":
		m.errBar.text = "no muted sessions to unmute"
	case firstErr != "":
		m.errBar.text = fmt.Sprintf("unmuted %d, first error: %s", unmuted, firstErr)
	default:
		m.errBar.text = ""
		m.reportDone(fmt.Sprintf("unmuted %d", unmuted))
	}
	m.rebuildRows()
	m.requestRefresh()
	return m, nil
}

// displayStatusLabel prefers the mute or a known failure reason to the
// general pane status.
func (m *Model) displayStatusLabel(sess store.Session) string {
	if sess.Muted {
		// A snoozed row still reads muted, which is what it is and what a
		// search for the word finds, and then says when that stops.
		if !sess.MutedUntil.IsZero() {
			return mutedStatusLabel + " " + snoozeLabel(sess.MutedUntil, time.Now())
		}
		return mutedStatusLabel
	}
	if label, _ := m.claudeAuthNotice(sess); label != "" {
		return label
	}
	return statusLabel(m.shownStatus(sess))
}

// displayStatusGlyph is the mark beside that label. A muted row takes the
// mute glyph in place of its status mark, since the label no longer names
// the pane state.
func (m *Model) displayStatusGlyph(sess store.Session) string {
	if sess.Muted {
		return mutedGlyph()
	}
	return statusGlyph(m.shownStatus(sess))
}

// displayStatusText tints the label: subtle for a mute, which is a queue
// decision rather than a state, and the status colour otherwise.
func (m *Model) displayStatusText(sess store.Session, text string) string {
	if sess.Muted {
		return subtleText(text)
	}
	return statusTint(m.shownStatus(sess), text)
}
