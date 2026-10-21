package ui

import (
	"slices"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// reorderMark is one manual swap as the order it left behind: first now sits
// above second. at is taken after the store committed it.
type reorderMark struct {
	group         bool
	first, second string
	at            time.Time
}

// holdReorders keeps a poll pass that listed the board before a reorder from
// undoing it. Without it the moved row blinks back to its old seat until the
// next pass, and a second press in that window aims at the stale neighbour and
// swaps the row back down. Each mark restores only its own pair's order, so
// applying it to a listing that already has the swap changes nothing.
func (m *Model) holdReorders(listedAt time.Time) {
	kept := m.reorders[:0]
	for _, mark := range m.reorders {
		if listedAt.After(mark.at) {
			continue
		}
		kept = append(kept, mark)
		if mark.group {
			first, second := slices.Index(m.groups, mark.first), slices.Index(m.groups, mark.second)
			if first > second && second >= 0 {
				m.groups[first], m.groups[second] = m.groups[second], m.groups[first]
			}
			continue
		}
		first := slices.IndexFunc(m.sessions, func(sess store.Session) bool { return sess.ID == mark.first })
		second := slices.IndexFunc(m.sessions, func(sess store.Session) bool { return sess.ID == mark.second })
		if first > second && second >= 0 {
			if ordered, err := store.SwapLinkedSessions(m.sessions, mark.first, mark.second); err == nil {
				m.sessions = ordered
			}
		}
	}
	m.reorders = kept
}
