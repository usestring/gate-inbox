package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A tier is the operator, or the work itself, saying how much this one
// matters.
//
// Triage orders by how badly a person is needed and then by how long the
// wait has been, which is the right queue for a fleet of equals. A fleet
// rarely is one: the session on the release, or the group holding the
// customer's fix, matters more than a side experiment that happens to have
// been waiting longer. The tier is the second key that lets that be said,
// and it is stored on the session -- or the group, whose tier covers the
// subtree -- because it is a fact about the work rather than about one
// pass, which is what sets it apart from a mute (see mute.go).
//
// It lifts a session within the queue it is in rather than over every
// other: an urgent idle session still waits behind a session that is
// blocked on an answer, because the drain hands over the sessions that need
// a person first and the rail must read in the same order the drain walks.
// Within the sessions that need a person, and again within the idle ones,
// the tiers order highest first and the rest follow in the usual order.
//
// The scale itself lives in internal/priority, because a goal's frontmatter
// and a session's own declaration write the same tiers this key cycles.

// tierOf is the one definition of how much a session matters: its own tier
// or a group's above it, whichever is higher. The sort, the drain and the
// row's marker all read it, so what the rail paints is the order the drain
// walks.
func (m *Model) tierOf(sess store.Session) priority.Tier {
	return store.EffectiveTier(m.priorityGroups, sess)
}

// cyclePrioritySelected walks the row under the cursor one step around the
// tier cycle: the session, or the group whose subtree it should cover.
// Pressing past Low clears it. The tier is written to the store rather than
// kept on the model because it outlives the pass, the process and the
// operator's memory of setting it.
func (m *Model) cyclePrioritySelected() (tea.Model, tea.Cmd) {
	entry, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	m.errBar.text = ""
	if entry.isGroup {
		if entry.isRoot() {
			m.errBar.text = "root is every session — tier a group or a session instead"
			return m, nil
		}
		next := priority.Next(m.priorityGroups[entry.group])
		if err := m.store.SetGroupPriority(entry.group, next); err != nil {
			m.errBar.text = err.Error()
			return m, nil
		}
		if m.priorityGroups == nil {
			m.priorityGroups = map[string]priority.Tier{}
		}
		if next == priority.Unset {
			delete(m.priorityGroups, entry.group)
		} else {
			m.priorityGroups[entry.group] = next
		}
		m.rebuildRows()
		m.requestRefresh()
		return m, nil
	}
	if !entry.isSession() {
		return m, nil
	}
	sess := entry.sess
	// The session's own tier, not the effective one: cycling a row inside a
	// tiered group must move that row's statement, not silently rewrite it
	// to whatever the group already says.
	next := priority.Next(sess.Priority)
	if err := m.store.SetPriority(sess.ID, next); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	// The tier is set in the model straight away, so the row's marker and
	// the triage order move on this frame rather than a poll later; the
	// refresh confirms what the store already holds.
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].Priority = next
		}
	}
	m.rebuildRows()
	m.requestRefresh()
	return m, nil
}

// priorityBadge is a tier's mark as a row draws it.
//
// Urgent takes the errored tint and High the waiting one -- the two colours
// the rail already spends on "look here" -- while the tiers at and below
// the middle stay subtle. A tier is a ranking rather than a state, so
// nothing below High may compete with a status dot for the eye.
func priorityBadge(tier priority.Tier) string {
	glyph := tier.Glyph()
	if glyph == "" {
		return ""
	}
	switch tier {
	case priority.Urgent:
		return statusTint(status.Errored, glyph)
	case priority.High:
		return statusTint(status.Waiting, glyph)
	default:
		return subtleText(glyph)
	}
}

// priorityMarker is the badge a session row wears, or nothing. A session
// inside a tiered group wears the tier it inherits: the queue treats them
// alike, and the group row above says where it came from.
func (m *Model) priorityMarker(sess store.Session) string {
	badge := priorityBadge(m.tierOf(sess))
	if badge == "" {
		return ""
	}
	return " " + badge
}

// priorityLegend names what the next press of p would set, so the footer
// teaches the cycle instead of only reporting where it currently is.
func priorityLegend(current priority.Tier) string {
	return "priority " + priority.Next(current).Label()
}
