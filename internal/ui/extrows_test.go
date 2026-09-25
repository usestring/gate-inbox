package ui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// rowMarksModel is childModel with a bridge from two extensions, delivering
// straight to the test.
func rowMarksModel(t *testing.T, uis ...ExtensionUI) (*Model, *ExtensionBridge) {
	t.Helper()
	m := childModel(t)
	bridge := NewExtensionBridge([]string{"batch", "other"})
	m.InstallExtensions(uis, bridge)
	bridge.Attach(func(tea.Msg) {})
	return m, bridge
}

// An owned session is off the operator's queue: not waiting on them, not in
// the drain, not under the attention filter, and not a reason to keep its
// parent there -- until it is let go.
func TestAnOwnedSessionLeavesTheOperatorsQueue(t *testing.T) {
	m, bridge := rowMarksModel(t)
	c2, _ := m.sessionByID("c2")
	if !m.needsPerson(c2) || !m.triageWalkable(c2) {
		t.Fatal("a waiting child should be on the queue before anything owns it")
	}
	bridge.Own("batch", "c2", true)
	m.Update(extensionBadgesMsg{})
	if m.needsPerson(c2) || m.triageWalkable(c2) {
		t.Fatal("an owned session is still on the operator's queue")
	}

	m.statusFilter = statusFilterAttention
	m.selectSessionRow(t, "unrelated")
	listed := func() string {
		m.rebuildRows()
		var ids []string
		for _, sess := range m.listedSessions() {
			ids = append(ids, sess.ID)
		}
		return joined(ids)
	}
	if got := listed(); strings.Contains(got, "c2") || !strings.Contains(got, "p1") || !strings.Contains(got, "c3") {
		t.Fatalf("attention lists %s, want c3 and the parent it keeps up, not the owned c2", got)
	}
	bridge.Own("batch", "c3", true)
	m.Update(extensionBadgesMsg{})
	if got := listed(); strings.Contains(got, "p1") {
		t.Fatalf("attention lists %s: a parent whose waiting children are all owned is not waiting on anybody", got)
	}

	bridge.Own("batch", "c2", false)
	bridge.Own("batch", "c3", false)
	m.Update(extensionBadgesMsg{})
	if !m.needsPerson(c2) {
		t.Fatal("a session let go is still owned")
	}
}

// A session one extension owns stays owned while another lets go of it
// only for itself.
func TestOwnershipIsPerExtension(t *testing.T) {
	m, bridge := rowMarksModel(t)
	bridge.Own("batch", "c2", true)
	bridge.Own("other", "c2", true)
	bridge.Own("other", "c2", false)
	m.Update(extensionBadgesMsg{})
	if !m.ownedByExtension("c2") {
		t.Fatal("one extension letting go released another's claim")
	}
}

// A hidden session is gone from the browsing tree, children and all, and
// still found by a search.
func TestAHiddenSessionLeavesOnlyTheBrowsingTree(t *testing.T) {
	m, bridge := rowMarksModel(t)
	m.setChildrenFolded("p1", false)
	m.rebuildRows()
	if got := joined(rowIDs(m)); got != "p1,c1,c2,c3,s9" {
		t.Fatalf("rows = %s before anything is hidden", got)
	}
	bridge.Hide("batch", "c1", true)
	bridge.Hide("batch", "s9", true)
	m.Update(extensionBadgesMsg{})
	if got := joined(rowIDs(m)); got != "p1,c2,c3" {
		t.Fatalf("rows = %s, want the hidden child and session gone", got)
	}
	bridge.Hide("batch", "p1", true)
	m.Update(extensionBadgesMsg{})
	if got := joined(rowIDs(m)); got != "" {
		t.Fatalf("rows = %s, want a hidden parent to take its children with it", got)
	}

	m.search = "unrelated"
	m.rebuildRows()
	if got := joined(rowIDs(m)); got != "s9" {
		t.Fatalf("search rows = %s, want the hidden match", got)
	}
	m.search = ""
	bridge.Hide("batch", "p1", false)
	bridge.Hide("batch", "c1", false)
	bridge.Hide("batch", "s9", false)
	m.Update(extensionBadgesMsg{})
	if got := joined(rowIDs(m)); got != "p1,c1,c2,c3,s9" {
		t.Fatalf("rows = %s after everything was shown again", got)
	}
}

// An extension's filter is a list key: on, it narrows the list to what Keep
// keeps and says so in the header; off, the list is whole again. A Keep
// that panics keeps everything.
func TestAnExtensionFilterNarrowsTheList(t *testing.T) {
	var asked []string
	m, _ := rowMarksModel(t, ExtensionUI{Owner: "batch", Filters: []ExtensionFilter{{
		Action: "batch_only", Keys: []string{"alt+s"}, Label: "only batch", Badge: "batch",
		Keep: func(sess store.Session) bool {
			asked = append(asked, sess.ID)
			return sess.ID == "p1" || sess.ParentID == "p1"
		},
	}}})
	if problems := strings.Join(m.keyProblems, "\n"); strings.Contains(problems, "batch_only") {
		t.Fatalf("the filter's key did not take: %s", problems)
	}
	m.selectSessionRow(t, "worker")
	if _, cmd := m.handleKey(key("alt+s")); cmd != nil {
		cmd()
	}
	if got := joined(rowIDs(m)); got != "p1" {
		t.Fatalf("rows = %s, want the parent alone (its children fold)", got)
	}
	if len(asked) == 0 {
		t.Fatal("Keep was never asked")
	}
	header := ansi.Strip(strings.Join(m.filterBadgeLines(), "\n"))
	if !strings.Contains(header, "BATCH") || !strings.Contains(header, "show all") {
		t.Fatalf("header = %q, want the filter's badge and the way out", header)
	}
	m.handleKey(key("alt+s"))
	if got := joined(rowIDs(m)); got != "p1,s9" {
		t.Fatalf("rows = %s after the filter was lifted", got)
	}

	m.extFilters[0].Keep = func(store.Session) bool { panic("broken") }
	m.handleKey(key("alt+s"))
	if got := joined(rowIDs(m)); got != "p1,s9" {
		t.Fatalf("rows = %s, want a panicking filter to keep every session", got)
	}
}

// A header is a line of the session's entry above its row, not a row of its
// own: the cursor never lands on it, and it goes where the row goes.
func TestAHeaderIsDrawnAboveItsRow(t *testing.T) {
	m, bridge := rowMarksModel(t)
	rows := len(m.rows)
	bridge.Group("batch", "s9", []Span{{Text: "◈ batch ", Tone: ToneAccent, Bold: true}, {Text: "ship it\x1b[2J · 3 earlier"}})
	bridge.Group("other", "s9", []Span{{Text: "second header"}})
	m.Update(extensionBadgesMsg{})
	if len(m.rows) != rows {
		t.Fatalf("a header added a row: %d rows, want %d", len(m.rows), rows)
	}
	var lines []string
	for _, line := range m.entryLines(m.rows, 0, 80, 30) {
		lines = append(lines, ansi.Strip(line.text))
	}
	at := -1
	for i, line := range lines {
		if strings.Contains(line, "unrelated") {
			at = i
			break
		}
	}
	if at < 2 || !strings.Contains(lines[at-2], "◈ batch ship it[2J · 3 earlier") || !strings.Contains(lines[at-1], "second header") {
		t.Fatalf("want both headers, in build order and cleaned, above the row:\n%s", strings.Join(lines, "\n"))
	}

	bridge.Group("batch", "s9", nil)
	bridge.Group("other", "s9", []Span{})
	m.Update(extensionBadgesMsg{})
	for _, line := range m.entryLines(m.rows, 0, 80, 30) {
		if strings.Contains(ansi.Strip(line.text), "header") {
			t.Fatal("a cleared header is still drawn")
		}
	}
}

// A filter with no state of its own takes it, once, from the first old
// setting that holds one, and stores it under its own key; the old setting
// is left alone, and a filter with state of its own never reads it.
func TestAFilterCarriesAnOldSettingOverOnce(t *testing.T) {
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for key, value := range map[string]string{"items_only": "1", "items_later": "off"} {
		if err := st.SetSetting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	install := func() *Model {
		m := childModel(t)
		m.store = st
		m.InstallExtensions([]ExtensionUI{{Owner: "items", Filters: []ExtensionFilter{{
			Action: "items_view", Keys: []string{"alt+s"}, Label: "only items",
			Keep:        func(store.Session) bool { return true },
			CarriedFrom: []string{"items_missing", "items_only", "items_later"},
		}}}}, NewExtensionBridge([]string{"items"}))
		return m
	}
	if m := install(); !m.extFilters[0].on {
		t.Fatal("the filter did not take the old setting's on")
	}
	if got, _ := st.Setting("extension_filter.items.items_view"); got != "on" {
		t.Fatalf("the filter's own setting = %q, want the carried value stored", got)
	}
	if got, _ := st.Setting("items_only"); got != "1" {
		t.Fatalf("the old setting = %q, want it left as it was", got)
	}
	// Once carried, the old setting is not read again.
	if err := st.SetSetting("items_only", "0"); err != nil {
		t.Fatal(err)
	}
	if m := install(); !m.extFilters[0].on {
		t.Fatal("the old setting was read again after the carry")
	}
	if err := st.SetSetting("extension_filter.items.items_view", "off"); err != nil {
		t.Fatal(err)
	}
	if m := install(); m.extFilters[0].on {
		t.Fatal("the filter's own off lost to the old setting")
	}
}
