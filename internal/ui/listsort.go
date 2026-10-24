package ui

import (
	"github.com/usestring/gate-inbox/internal/keymap"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/store"
)

// The board has only ever had one order: the one the move keys wrote. That
// is the right default -- a board somebody arranged stays arranged -- and it
// is the wrong only option, because on a fleet of any size the sessions that
// matter are the ones just touched, and they sit wherever they were last
// dragged.
//
// Sorting is per group rather than over the whole board. The groups are the
// structure the operator built; reordering inside them answers "which of
// these did I touch last" without throwing that away.

const (
	listSortSetting = "list_sort"
	// listSortManual is the order the move keys write, and the default.
	listSortManual = "manual"
	// listSortActivity is most recently active first.
	listSortActivity = "activity"
	// listSortRecent is the session the operator last handed something
	// first. Activity is the wrong clock for "what was I just working in":
	// every turn another session or an extension starts moves it, so a
	// fleet's busiest panes float to the top whether anyone looked at them.
	listSortRecent = "recent"
	// listSortName sorts by name, for a board read as a directory.
	listSortName = "name"
)

// listSortModes is the setting's cycle order.
var listSortModes = []string{listSortManual, listSortActivity, listSortRecent, listSortName}

func storedListSort(st *store.Store) string {
	chosen, err := st.Setting(listSortSetting)
	if err != nil {
		return listSortManual
	}
	return normalizeListSort(chosen)
}

func normalizeListSort(chosen string) string {
	for _, mode := range listSortModes {
		if chosen == mode {
			return mode
		}
	}
	return listSortManual
}

// sortsList reports whether an order other than the stored one is in force,
// which is also what makes the move keys inert.
func (m *Model) sortsList() bool {
	if m.showArchived {
		return true
	}
	mode := normalizeListSort(m.listSort)
	return mode != listSortManual
}

// sortGroupSessions puts one group's top-level sessions in the chosen order.
// Children are not in this slice -- they ride with their parent when the row
// is emitted -- so sorting here cannot separate a session from its work.
//
// Every comparison ends on the id, so the order is total and a redraw two
// seconds later cannot reshuffle rows that compare equal. The rail is
// rebuilt on every poll, and a board that reorders under a reaching hand is
// worse than one in the wrong order.
func (m *Model) sortGroupSessions(sessions []store.Session) {
	if m.showArchived {
		// The archive is a log, not a board: what was filed last is what
		// the operator is most likely looking for, whatever order the
		// active list is in.
		sort.SliceStable(sessions, func(i, j int) bool {
			a, b := sessions[i], sessions[j]
			if !a.ArchivedAt.Equal(b.ArchivedAt) {
				return a.ArchivedAt.After(b.ArchivedAt)
			}
			if !a.LastStatusAt.Equal(b.LastStatusAt) {
				return a.LastStatusAt.After(b.LastStatusAt)
			}
			return a.ID < b.ID
		})
		return
	}
	switch normalizeListSort(m.listSort) {
	case listSortActivity:
		sort.SliceStable(sessions, func(i, j int) bool {
			a, b := sessions[i], sessions[j]
			if !a.LastStatusAt.Equal(b.LastStatusAt) {
				return a.LastStatusAt.After(b.LastStatusAt)
			}
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.After(b.CreatedAt)
			}
			return a.ID < b.ID
		})
	case listSortRecent:
		sort.SliceStable(sessions, func(i, j int) bool {
			a, b := sessions[i], sessions[j]
			// The same clock smart triage counts a wait from: a session
			// nobody has touched ranks by when it was created, so a new
			// launch lands near the top rather than under every old one.
			ta, tb := triageWaitingSince(a), triageWaitingSince(b)
			if !ta.Equal(tb) {
				return ta.After(tb)
			}
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.After(b.CreatedAt)
			}
			return a.ID < b.ID
		})
	case listSortName:
		sort.SliceStable(sessions, func(i, j int) bool {
			a, b := sessions[i], sessions[j]
			na, nb := strings.ToLower(a.Name), strings.ToLower(b.Name)
			if na != nb {
				return na < nb
			}
			return a.ID < b.ID
		})
	}
}

// listSortRefusal is why the move keys do nothing right now. They stay bound
// rather than being taken away: the write would land in the store and show
// up the moment the order goes back to manual, so a key that silently wrote
// one would read as the board ignoring it.
func (m *Model) listSortRefusal() string {
	if m.showArchived {
		return "the archive is ordered by when each session was archived — " + m.pressTo(keymap.ContextList, keymap.ArchivedView, "go back to the active list before reordering")
	}
	switch normalizeListSort(m.listSort) {
	case listSortActivity:
		return "the list is ordered by last activity — " + m.pressTo(keymap.ContextList, keymap.Settings, "open settings") + " and set sort to manual before reordering"
	case listSortRecent:
		return "the list is ordered by your last interaction — " + m.pressTo(keymap.ContextList, keymap.RecentSort, "toggle the recent sort off") + " before reordering"
	case listSortName:
		return "the list is ordered by name — press " + m.cap(keymap.ContextList, keymap.Settings) + " and set sort to manual before reordering"
	}
	return ""
}

// toggleRecentSort flips the board between the recent order and whatever
// order it was in before, and persists the result like the settings row
// does, so the toggle and the panel never disagree about the sort. A board
// that opened on recent goes back to manual, the default.
func (m *Model) toggleRecentSort() tea.Cmd {
	if m.showArchived {
		return nil
	}
	previousKey := ""
	if entry, ok := m.selectedRow(); ok {
		previousKey = rowKey(entry)
	}
	current := normalizeListSort(m.listSort)
	next := listSortRecent
	if current == listSortRecent {
		next = normalizeListSort(m.listSortBeforeRecent)
		if next == listSortRecent {
			next = listSortManual
		}
	} else {
		m.listSortBeforeRecent = current
	}
	m.listSort = next
	if err := m.store.SetSetting(listSortSetting, next); err != nil {
		m.errBar.text = err.Error()
	}
	m.rebuildRows()
	return m.afterListFilter(previousKey)
}
