package ui

import (
	"fmt"
	"strings"

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
	if err := m.store.SetMuted(sess.ID, muted); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	// The flag moves on the model straight away so the row's label and the
	// triage queue move on this frame; the refresh confirms the store.
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].Muted = muted
		}
	}
	if muted {
		m.errBar.text = m.displayName(sess) + " muted — out of triage until unmuted"
	} else {
		m.errBar.text = m.displayName(sess) + " unmuted"
	}
	m.rebuildRows()
	m.requestRefresh()
	return m, nil
}

// unmuteAllListed lifts the persistent mute from every session in the current
// view. Like restoreAllDead it carries on past a failed write and names the
// first one.
func (m *Model) unmuteAllListed() (tea.Model, tea.Cmd) {
	unmuted := 0
	var firstErr string
	query := strings.ToLower(strings.TrimSpace(m.search))
	for _, sess := range m.listedSessions() {
		if query != "" && !matchesSearch(sess, query, m.searchText[sess.ID]) {
			if _, ok := m.historyHit(sess); !ok {
				continue
			}
		}
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
				m.sessions[i].Muted = false
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

// displayStatusLabel is the state a row wears: "muted" when the operator has
// silenced it, otherwise its pane status.
func (m *Model) displayStatusLabel(sess store.Session) string {
	if sess.Muted {
		return mutedStatusLabel
	}
	return statusLabel(sess.Status)
}

// displayStatusGlyph is the mark beside that label. A muted row takes the
// mute glyph in place of its status mark, since the label no longer names
// the pane state.
func (m *Model) displayStatusGlyph(sess store.Session) string {
	if sess.Muted {
		return mutedGlyph()
	}
	return statusGlyph(sess.Status)
}

// displayStatusText tints the label: subtle for a mute, which is a queue
// decision rather than a state, and the status colour otherwise.
func (m *Model) displayStatusText(sess store.Session, text string) string {
	if sess.Muted {
		return subtleText(text)
	}
	return statusTint(sess.Status, text)
}
