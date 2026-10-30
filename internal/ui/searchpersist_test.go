// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func searchModel() *Model {
	m := &Model{width: 120, height: 30, collapsed: map[string]bool{}}
	m.sessions = []store.Session{
		{ID: "1", Name: "api-server", Status: status.Idle},
		{ID: "2", Name: "web-ui", Status: status.Idle},
		{ID: "3", Name: "docs", Status: status.Idle},
	}
	m.rebuildRows()
	return m
}

func railHead(m *Model) string {
	var b strings.Builder
	for _, line := range m.railLines(40, 24) {
		b.WriteString(ansi.Strip(line.text) + "\n")
	}
	return b.String()
}

// A query that outlives its field has to keep saying so: without the row the
// filtered-away sessions read as sessions that are gone.
func TestSearchFieldOutlivesTheOpenField(t *testing.T) {
	m := searchModel()
	m.searching, m.search = true, "api"
	m.rebuildRows()
	if !strings.Contains(railHead(m), "≡") {
		t.Fatal("an open field should be in the rail")
	}
	m.searching = false
	rail := railHead(m)
	if !strings.Contains(rail, "≡") {
		t.Fatalf("a query still filtering should stay in the rail:\n%s", rail)
	}
	if !strings.Contains(rail, "edit") {
		t.Fatalf("a closed field should offer the way back in:\n%s", rail)
	}
}

func TestSearchRailIsCleanWithNoQuery(t *testing.T) {
	if rail := railHead(searchModel()); strings.Contains(rail, "≡") {
		t.Fatalf("no query should mean no field:\n%s", rail)
	}
}

func TestEnterKeepsTheQueryAndEscKeepsItToo(t *testing.T) {
	m := searchModel()
	m.searching, m.search = true, "api"
	m.rebuildRows()
	filtered := len(m.rows)

	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.searching || m.search != "api" || len(m.rows) != filtered {
		t.Fatalf("enter should close the field and keep the filter, got searching=%v query=%q rows=%d",
			m.searching, m.search, len(m.rows))
	}

	// esc on the list reaches no clearing: the query outlives the field,
	// and only deleting the text takes it back.
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.search != "api" || len(m.rows) != filtered {
		t.Fatalf("esc on the list should leave the filter alone, got query=%q rows=%d", m.search, len(m.rows))
	}

	// Deleting the text is what clears: reopen the field, wipe it, close it.
	m.handleKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	m.handleSearchKey(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.search != "" {
		t.Fatalf("ctrl+u should wipe the field, got %q", m.search)
	}
	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.rows) <= filtered {
		t.Fatalf("clearing should bring the sessions back, still %d rows", len(m.rows))
	}
}

func TestEscInTheFieldKeepsTheQuery(t *testing.T) {
	m := searchModel()
	m.searching, m.search = true, "api"
	m.rebuildRows()
	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.searching || m.search != "api" {
		t.Fatalf("esc should close the field and keep the query, got searching=%v query=%q", m.searching, m.search)
	}
	if !strings.Contains(railHead(m), "≡") {
		t.Fatal("a kept query should leave its field behind")
	}
}

func TestFocusSessionClearsSearchForHiddenTarget(t *testing.T) {
	m := searchModel()
	m.search = "api"
	m.rebuildRows()
	if !m.focusSession("2") {
		t.Fatal("search hid the target session")
	}
	selected, ok := m.selected()
	if !ok || selected.ID != "2" || m.search != "" {
		t.Fatalf("selected=%v query=%q, want session 2 with no search", selected.ID, m.search)
	}
}

func TestFocusSessionOpensTheFoldOverASearchMatch(t *testing.T) {
	m := searchModel()
	m.poller = &poller{}
	m.sessions[1].Group = "web"
	m.collapsed["web"] = true
	m.search = "web-ui"
	m.rebuildRows()
	if !m.focusSession("2") {
		t.Fatal("clearing the search left the match folded away")
	}
	selected, ok := m.selected()
	if !ok || selected.ID != "2" || m.collapsed["web"] {
		t.Fatalf("selected=%v folded=%v, want session 2 with its group open", selected.ID, m.collapsed["web"])
	}
}

// A terminal under a child agent sits two child folds down, and the fold on
// the grandparent hides it as surely as the one on its own parent.
func TestFocusSessionOpensEveryChildFoldOverASearchMatch(t *testing.T) {
	m := searchModel()
	m.poller = &poller{}
	m.sessions[1].ParentID = "1"
	m.sessions[2].ParentID = "2"
	m.setChildrenFolded("1", true)
	m.setChildrenFolded("2", true)
	m.search = "docs"
	m.rebuildRows()
	if !m.focusSession("3") {
		t.Fatal("clearing the search left the nested match folded away")
	}
	selected, ok := m.selected()
	if !ok || selected.ID != "3" {
		t.Fatalf("selected=%v, want the nested session 3", selected.ID)
	}
}
