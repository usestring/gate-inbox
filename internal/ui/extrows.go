package ui

// What an extension can say about the list's rows beyond a badge: a header
// line drawn above a session, a session left out of the browsing tree, a
// session whose questions somebody other than the operator answers, and a
// filter the operator can narrow the list with.
//
// Like badges, these are pushed from any goroutine and read by the loop from
// a snapshot, so no extension code runs while the tree is built -- except a
// filter's Keep, which is the question the filter is, and is asked of each
// session while the operator has that filter on.

import (
	"fmt"
	"maps"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// ExtensionFilter is a narrowing of the list an extension adds, toggled by a
// key of its own on the list. While it is on, the list shows only the
// sessions Keep keeps.
type ExtensionFilter struct {
	Action string
	Keys   []string
	Label  string
	// Badge is the word the list's header shows while the filter is on.
	Badge string
	// Keep is called on the event loop, once per session each time the
	// list is built.
	Keep func(store.Session) bool
}

// listFilter is one extension filter as the model holds it.
type listFilter struct {
	owner string
	ExtensionFilter
	on bool
	// failed is set once Keep has panicked, so the log says so once.
	failed bool
}

// Group replaces owner's header over one session's row. An empty header
// clears it.
func (b *ExtensionBridge) Group(owner, sessionID string, header []Span) {
	clean := make([]Span, 0, len(header))
	for _, span := range header {
		span.Text = cleanText(span.Text)
		if span.Text != "" {
			clean = append(clean, span)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(clean) == 0 {
		delete(b.headers[owner], sessionID)
	} else {
		if b.headers[owner] == nil {
			b.headers[owner] = map[string][]Span{}
		}
		b.headers[owner][sessionID] = clean
	}
	b.changedLocked()
}

// Hide sets whether owner hides one session from the browsing tree.
func (b *ExtensionBridge) Hide(owner, sessionID string, hidden bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	setMark(b.hidden, owner, sessionID, hidden)
	b.changedLocked()
}

// Own sets whether owner answers one session's questions.
func (b *ExtensionBridge) Own(owner, sessionID string, owned bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	setMark(b.owned, owner, sessionID, owned)
	b.changedLocked()
}

func setMark(marks map[string]map[string]bool, owner, sessionID string, on bool) {
	if !on {
		delete(marks[owner], sessionID)
		return
	}
	if marks[owner] == nil {
		marks[owner] = map[string]bool{}
	}
	marks[owner][sessionID] = true
}

// applyRowMarks takes a snapshot of what the extensions set. Hidden and owned
// rows change which rows the tree holds, so a change to either rebuilds it;
// badges and headers only change how rows paint.
func (m *Model) applyRowMarks(marks rowMarks) {
	m.extBadges, m.extHeaders = marks.badges, marks.headers
	if maps.Equal(m.extHidden, marks.hidden) && maps.Equal(m.extOwned, marks.owned) {
		return
	}
	m.extHidden, m.extOwned = marks.hidden, marks.owned
	m.rebuildRows()
}

// ownedByExtension reports a session whose questions an extension answers:
// it is off the operator's queue whatever its pane says.
func (m *Model) ownedByExtension(sessionID string) bool {
	return m.extOwned[sessionID]
}

// hiddenByExtension reports a session an extension keeps out of the browsing
// tree.
func (m *Model) hiddenByExtension(sessionID string) bool {
	return m.extHidden[sessionID]
}

// filterSetting is where a filter's state is kept between runs, the way the
// triage mode and the archive view keep theirs.
func filterSetting(f *listFilter) string {
	return "extension_filter." + f.owner + "." + f.Action
}

func (m *Model) newListFilter(owner string, filter ExtensionFilter) *listFilter {
	listed := &listFilter{owner: owner, ExtensionFilter: filter}
	if m.store != nil {
		if value, err := m.store.Setting(filterSetting(listed)); err == nil {
			listed.on = value == "on"
		}
	}
	return listed
}

// toggleListFilter turns one extension filter on or off.
func (m *Model) toggleListFilter(f *listFilter) tea.Cmd {
	previousKey := ""
	if entry, ok := m.selectedRow(); ok {
		previousKey = rowKey(entry)
	}
	f.on = !f.on
	value := "off"
	if f.on {
		value = "on"
	}
	m.errBar.text = ""
	m.rebuildRows()
	st, key := m.store, filterSetting(f)
	if st == nil {
		return m.afterListFilter(previousKey)
	}
	return tea.Batch(
		deferStoreWrite(func() error { return st.SetSetting(key, value) }),
		m.afterListFilter(previousKey),
	)
}

// extensionFiltersOn reports whether any extension filter is narrowing the
// list.
func (m *Model) extensionFiltersOn() bool {
	for _, f := range m.extFilters {
		if f.on {
			return true
		}
	}
	return false
}

// extensionFiltersKeep is whether every extension filter that is on keeps
// sess. A Keep that panics keeps everything, so a broken filter shows the
// whole list rather than an empty one.
func (m *Model) extensionFiltersKeep(sess store.Session) bool {
	for _, f := range m.extFilters {
		if f.on && f.Keep != nil && !f.keep(sess) {
			return false
		}
	}
	return true
}

func (f *listFilter) keep(sess store.Session) (kept bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if !f.failed {
				f.failed = true
				logging.Warn("extension filter panicked; it keeps every session",
					"extension", f.owner, "action", f.Action, "panic", fmt.Sprint(recovered))
			}
			kept = true
		}
	}()
	return f.Keep(sess)
}

// extensionFilterBadges are the header badges for the filters that are on,
// each with the key that lifts it.
func (m *Model) extensionFilterBadges(badge func(label, key, action string)) {
	for _, f := range m.extFilters {
		if !f.on {
			continue
		}
		label := badgeText(f.Badge)
		if label == "" {
			label = badgeText(f.Label)
		}
		badge(strings.ToUpper(cellTruncate(label, 16, "…")), m.tightCap(keymap.ContextList, keymap.Action(f.Action)), "show all")
	}
}

// extensionHeaderLines are the header lines the extensions set over a
// session's row, each indented to the row's own guides and cut to width.
func (m *Model) extensionHeaderLines(entry treeRow, width, index int) []string {
	if !entry.isSession() {
		return nil
	}
	headers := m.extHeaders[entry.sess.ID]
	if len(headers) == 0 {
		return nil
	}
	lead := spaces(railInset) + m.treeGuidesAbove(index)
	styles := viewStyles()
	lines := make([]string, 0, len(headers))
	for _, header := range headers {
		var b strings.Builder
		for _, span := range header {
			b.WriteString(styles[viewStyleKey{span.Tone, span.Bold}].Render(span.Text))
		}
		lines = append(lines, cellTruncate(lead+b.String(), width, "…"))
	}
	return lines
}

// treeGuidesAbove is the tree's guides on a line drawn just above the row at
// index: every level that runs past the row carries on through the line, and
// the row's own level does too, since the row is the branch it runs into.
func (m *Model) treeGuidesAbove(index int) string {
	if index < 0 || index >= len(m.rows) {
		return ""
	}
	depth := m.rows[index].depth
	var guides strings.Builder
	for slot := 1; slot <= depth; slot++ {
		if slot == depth || m.slotContinues(index, slot) {
			guides.WriteString(subtleText("│  "))
		} else {
			guides.WriteString("   ")
		}
	}
	return guides.String()
}
