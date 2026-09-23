package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/store"
)

func TestMigrationTreeConnectsPeersAcrossTheirChildren(t *testing.T) {
	m := Model{rows: []treeRow{
		{isGroup: true, group: "api"},
		{depth: 1, sess: store.Session{ID: "source", MigrationID: "source"}},
		{depth: 2, sess: store.Session{ID: "terminal", ParentID: "source"}},
		{depth: 1, sess: store.Session{ID: "second", MigrationID: "source"}},
		{depth: 1, sess: store.Session{ID: "third", MigrationID: "source"}},
		{depth: 1, sess: store.Session{ID: "other-source", MigrationID: "other-source"}},
		{depth: 1, sess: store.Session{ID: "other-target", MigrationID: "other-source"}},
		{depth: 1, sess: store.Session{ID: "unlinked"}},
	}}
	connectMigrationRows(m.rows)
	wantGuides := []string{"", "├─ ╭─ ", "│  │  ╰─ ", "│  ├─ ", "│  ╰─ ", "├─ ╭─ ", "│  ╰─ ", "╰─ "}
	wantTrails := []string{"", "│  │  ", "│  │     ", "│  │  ", "│     ", "│  │  ", "│     ", "   "}
	for i := range m.rows {
		if got := ansi.Strip(m.treeGuidesAt(i)); got != wantGuides[i] {
			t.Errorf("row %d guide = %q, want %q", i, got, wantGuides[i])
		}
		if got := ansi.Strip(m.treeGuideTrail(i)); got != wantTrails[i] {
			t.Errorf("row %d metadata guide = %q, want %q", i, got, wantTrails[i])
		}
	}
	if m.rows[2].sess.ParentID != "source" || m.rows[3].sess.ParentID != "" {
		t.Fatal("visual grouping changed session ownership")
	}
}

func TestMigrationTreeClosesAtRootAndDoesNotBridgeFilteredGaps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		links []string
		want  []string
	}{
		{"root pair", []string{"a", "a"}, []string{"╭─ ", "╰─ "}},
		{"lone match", []string{"a"}, []string{""}},
		{"ranked apart", []string{"a", "", "a"}, []string{"", "", ""}},
		{"adjacent pairs", []string{"a", "a", "b", "b"}, []string{"╭─ ", "╰─ ", "╭─ ", "╰─ "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{}
			for _, link := range tc.links {
				m.rows = append(m.rows, treeRow{sess: store.Session{MigrationID: link}})
			}
			connectMigrationRows(m.rows)
			if len(m.rows) != len(tc.links) {
				t.Fatal("grouping added a keyboard stop")
			}
			for i, want := range tc.want {
				if got := ansi.Strip(m.treeGuidesAt(i)); got != want {
					t.Errorf("row %d guide = %q, want %q", i, got, want)
				}
			}
		})
	}
}
