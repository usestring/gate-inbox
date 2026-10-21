package ui

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/usestring/gate-inbox/internal/priority"
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
// It lifts a session among the sessions in the same state rather than over
// every other: an urgent finished session still waits behind a session that
// is blocked on an answer, and an urgent idle one behind both, because the
// drain hands over the most pressing states first and the rail must read in
// the same order the drain walks. Within each state the tiers order highest
// first and the rest follow in the usual order.
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
		m.reportDone(priorityReport(next, baseName(entry.group)))
		return m, m.flashPriority(groupFlashKey(entry.group))
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
	m.reportDone(priorityReport(next, m.displayName(sess)))
	return m, m.flashPriority(sess.ID)
}

// priorityFlashFor is how long a row that just changed tier wears its
// tier spelled out. A glyph that grows by one triangle is easy to miss on
// a busy rail, and in a focused session the rail may not be on screen at
// all, so the status bar says it too.
var priorityFlashFor = 4 * time.Second

type priorityFlash struct {
	key string
	seq uint64
}

type priorityFlashEndMsg struct{ seq uint64 }

func groupFlashKey(group string) string { return "group:" + group }

func (m *Model) flashPriority(key string) tea.Cmd {
	m.priorityFlash = priorityFlash{key: key, seq: m.priorityFlash.seq + 1}
	seq := m.priorityFlash.seq
	return tea.Tick(priorityFlashFor, func(time.Time) tea.Msg { return priorityFlashEndMsg{seq: seq} })
}

// endPriorityFlash drops the flash only if no later press has replaced it.
func (m *Model) endPriorityFlash(msg priorityFlashEndMsg) {
	if msg.seq == m.priorityFlash.seq {
		m.priorityFlash.key = ""
	}
}

func priorityReport(tier priority.Tier, name string) string {
	if tier == priority.Unset {
		return "priority cleared: " + name
	}
	return "priority " + tier.Label() + ": " + name
}

// priorityFlashBadge is the badge a row wears while it flashes: the glyph
// and the tier's name on a solid ground, or "no priority" when the press
// cleared it, so the change reads even where the badge vanished.
func priorityFlashBadge(tier priority.Tier) string {
	ground := priorityColor(tier)
	text := tier.Glyph() + " " + strings.ToUpper(tier.Label())
	if tier == priority.Unset {
		ground = lipgloss.Color("#6b7280")
		text = "NO PRIORITY"
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Background(ground).Render(text + " ")
}

// rowPriorityBadge is the badge for a row given its flash key and tier.
func (m *Model) rowPriorityBadge(key string, tier priority.Tier) string {
	if key != "" && m.priorityFlash.key == key {
		return priorityFlashBadge(tier)
	}
	return priorityBadge(tier)
}

func priorityBadge(tier priority.Tier) string {
	glyph := tier.Glyph()
	if glyph == "" {
		return ""
	}
	return lipgloss.NewStyle().Foreground(priorityColor(tier)).Render(glyph)
}

// priorityColor is green for a raised tier and red for one below the default.
func priorityColor(tier priority.Tier) color.Color {
	if tier.Rank() > priority.Unset.Rank() {
		return lipgloss.Color("#ef4444")
	}
	return lipgloss.Color("#22c55e")
}

// priorityMarker is the badge a session row wears, or nothing. A session
// inside a tiered group wears the tier it inherits: the queue treats them
// alike, and the group row above says where it came from.
func (m *Model) priorityMarker(sess store.Session) string {
	badge := m.rowPriorityBadge(sess.ID, m.tierOf(sess))
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
