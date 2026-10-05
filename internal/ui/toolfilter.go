package ui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/store"
)

// toolFilter narrows the list and the triage queue to one harness: the CLI
// named by Model.toolFilter, "" for every harness. It is the tool column's
// answer to the status filter: a fleet running several CLIs drains one
// harness at a time, in either view, without the other harnesses' rows in
// the way. Empty is the default, so a board that never filters reads exactly
// as it did before the filter existed.
func (m *Model) toolFilterActive() bool {
	return m.toolFilter != ""
}

// toolFilterLabel is the short badge word for the header and empty state:
// the harness itself, uppercased the way every other badge reads.
func (m *Model) toolFilterLabel() string {
	return strings.ToUpper(m.toolFilter)
}

// matchesToolFilter reports whether a session's harness is visible under the
// filter. A session is what its tool says it is; shells are tools too, under
// whatever name their block carries.
func (m *Model) matchesToolFilter(sess store.Session) bool {
	return !m.toolFilterActive() || sess.Tool == m.toolFilter
}

// availableToolFilters is every harness the filter can narrow to: the tools
// on the board's rows, in alphabetical order so the cycle lands somewhere
// predictable. The active filter is kept even with no row left on it, so
// cycling out of a harness emptied by kills still reaches the next one.
func (m *Model) availableToolFilters() []string {
	seen := map[string]bool{}
	for _, sess := range m.sessions {
		if sess.Tool == "" || sess.Archived != m.showArchived {
			continue
		}
		seen[sess.Tool] = true
	}
	if m.toolFilterActive() {
		seen[m.toolFilter] = true
	}
	out := make([]string, 0, len(seen))
	for tool := range seen {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

// cycleToolFilter advances the harness filter (all -> each CLI in turn).
// The order is the available tools' alphabetical order; with no tools on the
// board there is nothing to narrow to and the filter stays off.
func (m *Model) cycleToolFilter() tea.Cmd {
	previousKey := ""
	if entry, ok := m.selectedRow(); ok {
		previousKey = rowKey(entry)
	}
	options := m.availableToolFilters()
	if len(options) == 0 {
		m.toolFilter = ""
		m.rebuildRows()
		return m.afterListFilter(previousKey)
	}
	if !m.toolFilterActive() {
		m.toolFilter = options[0]
	} else {
		next := ""
		for i, tool := range options {
			if tool == m.toolFilter && i+1 < len(options) {
				next = options[i+1]
				break
			}
		}
		m.toolFilter = next
	}
	m.rebuildRows()
	return m.afterListFilter(previousKey)
}
