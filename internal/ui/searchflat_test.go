package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// flatModel is a fleet small enough to read row by row and shaped like the
// one a search has to flatten: sessions at root and nested two deep, a
// child hanging off a parent the query itself misses, and a group whose
// sessions no query here matches. Every path is a fixed string, so nothing
// this fixture renders comes from the machine running it.
func flatModel(t testing.TB) *Model {
	t.Helper()
	now := time.Now()
	m := &Model{
		width: 120, height: 34, mode: modeList,
		collapsed:  map[string]bool{},
		groups:     []string{"backend", "backend/api", "infra"},
		groupPaths: map[string]string{"backend": "/repos", "backend/api": "/repos/api", "infra": "/repos/infra"},
		split:      splitState{ratio: defaultSplitRatio},
		preview:    previewSample,
	}
	m.cfg.Tools = map[string]config.Tool{"claude": {}}
	sess := func(id, name, group, parent, st string, age time.Duration) store.Session {
		return store.Session{
			ID: id, Name: name, Group: group, ParentID: parent,
			Tool: "claude", Status: st, Cwd: "/repos/checkout",
			CreatedAt: now.Add(-age), LastStatusAt: now.Add(-age),
			LaunchPrompt: "run the suite",
		}
	}
	m.sessions = []store.Session{
		sess("s1", "gate-root", "", "", status.Idle, time.Hour),
		sess("s2", "gate-api", "backend/api", "", status.Working, 2*time.Hour),
		sess("s3", "carrier", "backend/api", "", status.Idle, 3*time.Hour),
		sess("s4", "gate-child", "backend/api", "s3", status.Waiting, 4*time.Hour),
		sess("s5", "terraform", "infra", "", status.Idle, 5*time.Hour),
	}
	m.rebuildRows()
	return m
}

// typeSearch enters a query the way the field does, one key at a time, so
// what a test drives is the rebuild after every keystroke rather than one
// rebuild over a query that arrived whole.
func (m *Model) typeSearch(query string) {
	m.searching = true
	for _, r := range query {
		m.handleSearchKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func rowKeys(m *Model) []string {
	keys := make([]string, len(m.rows))
	for i, row := range m.rows {
		keys[i] = rowKey(row)
	}
	return keys
}

// The whole change: a query answers with sessions, so every group row goes,
// root's rollup included. Nothing in the list is a panel to land on.
func TestSearchLeavesNoGroupRow(t *testing.T) {
	m := flatModel(t)
	m.typeSearch("gate")
	if len(m.rows) == 0 {
		t.Fatal("a query with matches should leave rows")
	}
	for i, row := range m.rows {
		if row.isGroup {
			t.Fatalf("row %d is the group %q; a search should carry no group rows, got %v",
				i, row.group, rowKeys(m))
		}
	}
	want := []string{"s:s1", "s:s2", "s:s3", "s:s4"}
	if got := rowKeys(m); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("search rows = %v, want the matches flat in store order %v", got, want)
	}
	if m.rows[0].depth != 0 {
		t.Fatalf("the first match should sit at depth 0, got %d", m.rows[0].depth)
	}
}

// The cursor was deep in the tree when the query was typed. It has to come
// back to the first match rather than to whatever now sits at that index.
func TestSearchPutsTheCursorOnTheFirstMatch(t *testing.T) {
	m := flatModel(t)
	m.selectSessionRow(t, "terraform")
	m.typeSearch("gate")
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want the first match at 0 (rows %v)", m.cursor, rowKeys(m))
	}
	if group, ok := m.selectedGroup(); ok {
		t.Fatalf("cursor landed on the group %q", group)
	}
	sess, ok := m.selected()
	if !ok {
		t.Fatal("cursor should be on a session")
	}
	if sess.Name != "gate-root" {
		t.Fatalf("cursor on %q, want the first match gate-root", sess.Name)
	}
}

// Refining a query that still keeps the session under the cursor leaves it
// there: the reset is a fallback for a row the query dropped, not a jump to
// the top on every keystroke.
func TestRefiningASearchKeepsAStillMatchingCursor(t *testing.T) {
	m := flatModel(t)
	m.typeSearch("gate")
	m.selectSessionRow(t, "gate-api")
	held := m.cursor
	if held == 0 {
		t.Fatal("gate-api should not already head the list, or this proves nothing")
	}
	m.handleSearchKey(tea.KeyPressMsg{Code: '-', Text: "-"})
	if m.search != "gate-" {
		t.Fatalf("query = %q, want gate-", m.search)
	}
	sess, ok := m.selected()
	if !ok || sess.Name != "gate-api" || m.cursor != held {
		t.Fatalf("refining moved the cursor off gate-api: cursor %d (was %d), rows %v",
			m.cursor, held, rowKeys(m))
	}
}

// The right panel is the reason the flattening matters: on a group row it
// paints the rollup and its roster, and that is what Enter used to hand the
// user instead of the session they searched for.
func TestSearchOpensAPanePreviewNotTheRollup(t *testing.T) {
	m := flatModel(t)
	m.selectGroupRow(t, "backend/api")
	rollup := ansi.Strip(m.frame())
	if !strings.Contains(rollup, "last activity") {
		t.Fatalf("a group row should paint the roster, so this test can tell the two apart:\n%s", rollup)
	}

	m.typeSearch("gate")
	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.searching {
		t.Fatal("enter should close the field")
	}
	frame := ansi.Strip(m.frame())
	if strings.Contains(frame, "last activity") {
		t.Fatalf("the group roster is still painted after a search:\n%s", frame)
	}
	if !strings.Contains(frame, "Add a token bucket limiter") {
		t.Fatalf("the panel should be the session's pane preview:\n%s", frame)
	}
	if !strings.Contains(frame, "gate-root") {
		t.Fatalf("the panel should head the first match:\n%s", frame)
	}
}

// A parent the query missed still comes along to carry its matching child,
// and the flattening does not tear the two apart.
func TestSearchKeepsACarriedParentWithItsChild(t *testing.T) {
	m := flatModel(t)
	m.typeSearch("gate-child")
	want := []string{"s:s3", "s:s4"}
	if got := rowKeys(m); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want the carried parent then its match %v", got, want)
	}
	for i, row := range m.rows {
		if row.isGroup {
			t.Fatalf("row %d is a group row: %v", i, rowKeys(m))
		}
	}
	if m.rows[0].depth != 0 || m.rows[1].depth != 1 {
		t.Fatalf("the child should still nest under its parent, depths %d and %d",
			m.rows[0].depth, m.rows[1].depth)
	}
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.cursor)
	}
}

// A query that matches nothing has nothing to show. It used to leave root's
// rollup standing, which reads as a result.
func TestSearchWithNoMatchesLeavesNothing(t *testing.T) {
	m := flatModel(t)
	m.typeSearch("nosuchsession")
	if len(m.rows) != 0 {
		t.Fatalf("rows = %v, want none", rowKeys(m))
	}
	if _, ok := m.selectedRow(); ok {
		t.Fatal("an empty list should select nothing")
	}
	frame := ansi.Strip(m.frame())
	if !strings.Contains(frame, "Select a session to inspect it.") {
		t.Fatalf("an empty result should say so rather than paint a panel:\n%s", frame)
	}
	// Root used to pin a row no matter what, so a list with no rows at all
	// is a state the keys never saw before.
	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.cursor != 0 {
		t.Fatalf("walking an empty list moved the cursor to %d", m.cursor)
	}
}

// Clearing the query puts the tree back exactly as it was, root row first.
func TestClearingTheSearchRestoresTheTree(t *testing.T) {
	m := flatModel(t)
	before := rowKeys(m)
	m.typeSearch("gate")
	m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	// Through handleKey rather than clearSearch: the list-level binding is
	// half of what brings the tree back.
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.search != "" {
		t.Fatalf("esc should clear the query, got %q", m.search)
	}
	if got := rowKeys(m); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("cleared rows = %v, want the tree back %v", got, before)
	}
	if len(m.rows) == 0 || !m.rows[0].isRoot() {
		t.Fatalf("root's rollup should be back at the head: %v", rowKeys(m))
	}
}

// Triage already drains the tree, and it keeps root. A query on top of it
// drops root too, and still hands over the queue's own order.
func TestSearchInTriageDropsRootAndKeepsTheQueueOrder(t *testing.T) {
	m := flatModel(t)
	m.triage = true
	m.rebuildRows()
	// Triage already drops root on its own: one queue has nothing for a
	// rollup of the ungrouped sessions to head. Adding a search to it must
	// not bring the row back.
	if len(m.rows) == 0 {
		t.Fatal("triage built no rows to search over")
	}
	for i, row := range m.rows {
		if row.isGroup {
			t.Fatalf("row %d is a group row in triage: %v", i, rowKeys(m))
		}
	}

	// Parked on the one session no query here matches, so the cursor has
	// somewhere wrong to stay. It heads the queue now that triage drops
	// root, so what proves the cursor moved is the row under it after the
	// search rather than its index; TestSearchPutsTheCursorOnTheFirstMatch
	// holds the index case.
	m.selectSessionRow(t, "terraform")
	if key := rowKey(m.rows[m.cursor]); key != "s:s5" {
		t.Fatalf("cursor parked on %s, want the row the query drops", key)
	}
	m.typeSearch("gate")
	for i, row := range m.rows {
		if row.isGroup {
			t.Fatalf("row %d is a group row under triage + search: %v", i, rowKeys(m))
		}
	}
	// Store order is s1, s2, s3, s4. Triage ranks the two idle parents
	// oldest first and drops the working one behind them, and the child
	// still rides under the parent that carried it.
	want := []string{"s:s3", "s:s4", "s:s1", "s:s2"}
	if got := rowKeys(m); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want triage order %v", got, want)
	}
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want the head of the queue", m.cursor)
	}
	if key := rowKey(m.rows[m.cursor]); key != "s:s3" {
		t.Fatalf("cursor sits on %s, want the head of the searched queue", key)
	}
}

// The fold column is width the rail reserves for the rows on screen. A
// search prunes them, so a result set with nothing to fold has to give the
// column back rather than inherit the whole tree's answer.
func TestSearchRecountsTheFoldColumn(t *testing.T) {
	m := fleetModel(t, 87, 200, 50)
	if !m.railWorkColumn {
		t.Fatal("the fleet fixture should have work to fold")
	}
	// session-01 carries no pull request and no ticket, and no other
	// session's name, tool, group or status contains the string.
	m.typeSearch("session-01")
	if len(m.rows) != 1 {
		t.Fatalf("rows = %v, want the one match", rowKeys(m))
	}
	if m.railWorkColumn {
		t.Fatal("a result set with nothing to fold should drop the fold column")
	}
}
