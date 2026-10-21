package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// foldModel is rowMarksModel with "batch" heading p1 and s9 with headers that
// fold the session they head.
func foldModel(t *testing.T) *Model {
	t.Helper()
	m, bridge := rowMarksModel(t, ExtensionUI{Owner: "batch", FoldHeaders: true,
		OpenHeader: func(Press) error { return nil }})
	// Folding is kept with the other folds, in the store.
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m.store = st
	bridge.Group("batch", "p1", []Span{{Text: "◈ ship it"}})
	bridge.Group("batch", "s9", []Span{{Text: "◈ tidy up"}})
	m.Update(extensionBadgesMsg{})
	return m
}

func drawnRail(m *Model) string {
	var lines []string
	for _, line := range m.entryLines(m.rows, 0, 100, 40) {
		lines = append(lines, ansi.Strip(line.text))
	}
	return strings.Join(lines, "\n")
}

// A folding header is its session's row until it is unfolded: the session,
// and the children under it, are not drawn, and the header carries the
// session's state so a question it is holding still shows.
func TestAFoldingHeaderStandsInForItsSession(t *testing.T) {
	m := foldModel(t)
	if ids := joined(rowIDs(m)); strings.Contains(ids, "p1") || strings.Contains(ids, "c2") || strings.Contains(ids, "s9") {
		t.Fatalf("session rows = %s, want p1 with its children and s9 folded behind their headers", ids)
	}
	at := headIndex(t, m, "batch", "p1")
	if !m.rows[at].stands {
		t.Fatal("the header over a folded session does not stand in for it")
	}
	m.cursor = at
	if sess, ok := m.selected(); !ok || sess.ID != "p1" {
		t.Fatalf("selected = %q, want the folded session", sess.ID)
	}
	if row, _ := m.selectedRow(); !row.isSession() {
		t.Fatal("a key acting on the folded header's session does not see a session")
	}
	rail := drawnRail(m)
	if !strings.Contains(rail, "▸") || !strings.Contains(rail, "◈ ship it") || !strings.Contains(rail, "working") {
		t.Fatalf("the folded header does not carry its session's state:\n%s", rail)
	}
}

// Step-in unfolds it like a group; step-out on the header, or on the
// session's own row, folds it back.
func TestAFoldingHeaderUnfoldsAndFolds(t *testing.T) {
	m := foldModel(t)
	at := headIndex(t, m, "batch", "s9")
	m.cursor = at
	m.headFoldAction(m.rows[at], keymap.StepIn)
	if !strings.Contains(joined(rowIDs(m)), "s9") {
		t.Fatalf("session rows = %s after unfolding s9's header", joined(rowIDs(m)))
	}
	at = headIndex(t, m, "batch", "s9")
	if m.rows[at].stands {
		t.Fatal("an unfolded header still stands in for its session")
	}
	if next := m.rows[at+1]; !next.isSession() || next.sess.ID != "s9" {
		t.Fatalf("rows = %s, want the session under its unfolded header", strings.Join(rowKeys(m), ","))
	}
	if !strings.Contains(drawnRail(m), "▾") {
		t.Fatalf("the unfolded header does not say it folds:\n%s", drawnRail(m))
	}

	m.cursor = at + 1
	if !m.foldUnderHead(m.rows[m.cursor]) {
		t.Fatal("step-out on the session did not fold it back")
	}
	if strings.Contains(joined(rowIDs(m)), "s9") {
		t.Fatalf("session rows = %s after folding s9 back", joined(rowIDs(m)))
	}
	if row, ok := m.cursorRow(); !ok || !row.stands || row.sess.ID != "s9" {
		t.Fatalf("the cursor left the session it folded: %s", strings.Join(rowKeys(m), ","))
	}
}

// Triage folds as the tree does, and the drain hands over the header
// standing in for a session waiting on the operator; the header's page is not
// what it shows there, the session is.
func TestTriageHandsOverAFoldedHeadersSession(t *testing.T) {
	m := foldModel(t)
	for i := range m.sessions {
		if m.sessions[i].ID == "s9" {
			m.sessions[i].Status = status.Waiting
		}
	}
	m.triage = true
	m.rebuildRows()
	if strings.Contains(joined(rowIDs(m)), "s9") {
		t.Fatalf("triage rows = %s, want s9 folded behind its header", strings.Join(rowKeys(m), ","))
	}
	index, ok := m.nextTriageInput("", nil)
	if !ok || !m.rows[index].stands || m.rows[index].sess.ID != "s9" {
		t.Fatalf("the drain would hand over %v %s, want s9's header", ok, strings.Join(rowKeys(m), ","))
	}
	if _, has := m.headPaneAt(m.rows[index]); has {
		t.Fatal("triage shows the header's page over the session it stands for")
	}
}

// A search shows what it matched, so it unfolds every header.
func TestASearchUnfoldsFoldingHeaders(t *testing.T) {
	m := foldModel(t)
	m.search = "unrelated"
	m.rebuildRows()
	if !strings.Contains(joined(rowIDs(m)), "s9") {
		t.Fatalf("search rows = %s, want the match drawn", strings.Join(rowKeys(m), ","))
	}
}
