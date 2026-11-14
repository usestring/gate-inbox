package ui

import (
	"strings"

	"github.com/usestring/gate-inbox/internal/search"
	"github.com/usestring/gate-inbox/internal/store"
)

// Title search narrows the filter to what names a session: the name the
// operator or the namer gave it and the title generated for it. It leaves
// out the opening prompt a row falls back to, the pane, and the transcript,
// which together match almost any word a busy fleet has typed. Tab toggles
// it while the search field is open.

// matchesTitle is a literal hit on the session's name or generated title.
// query is already lowercased and trimmed.
func (m *Model) matchesTitle(sess store.Session, query string) bool {
	if search.Match(strings.ToLower(sess.Name), query) {
		return true
	}
	if m.awaitingRename(sess) {
		return false
	}
	title := strings.TrimSpace(m.titles[sess.ID])
	return title != "" && search.Match(strings.ToLower(title), query)
}

// searchMatches is whether the filter keeps a row for query, in whichever
// scope the search field is set to.
func (m *Model) searchMatches(sess store.Session, query string) bool {
	if m.searchTitleOnly {
		return m.matchesTitle(sess, query)
	}
	if matchesSearch(sess, m.sessionSummary(sess), query, m.searchText[sess.ID]) {
		return true
	}
	_, ok := m.historyHit(sess)
	return ok
}
