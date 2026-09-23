package store

import (
	"path/filepath"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// A store written before spawned_by existed has to keep answering as it did:
// every row on it was filed under its spawner or under that spawner's root,
// and the root is the answer those rows have always given.
func TestSpawnedByBackfillsFromTheTreeItReplaces(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "state.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.CreateSession(Session{ID: "parent01", Name: "root", Tool: "claude"}); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if err := st.CreateSession(Session{ID: "child001", Name: "kid", Tool: "claude", ParentID: "parent01"}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	// Back to what an older binary wrote: the column present but unset.
	if _, err := st.db.Exec(`UPDATE sessions SET spawned_by = ''`); err != nil {
		t.Fatalf("clear spawned_by: %v", err)
	}
	st.Close()

	st, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	child, err := st.Get("child001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if child.SpawnedBy != "parent01" {
		t.Fatalf("spawned_by = %q, want the parent it was filed under", child.SpawnedBy)
	}
	root, err := st.Get("parent01")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if root.SpawnedBy != "" {
		t.Fatalf("a root came back spawned by %q", root.SpawnedBy)
	}
}

// Placement and ownership are two columns now, and a grandchild is where they
// differ: filed under the root the tree can carry, owned by the session that
// asked for it.
func TestInsertKeepsSpawnerWhileFlatteningPlacement(t *testing.T) {
	st, err := Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	for _, sess := range []Session{
		{ID: "parent01", Name: "root", Tool: "claude"},
		{ID: "child001", Name: "spawner", Tool: "claude", ParentID: "parent01", SpawnedBy: "parent01"},
		{ID: "child002", Name: "grandchild", Tool: "claude", ParentID: "parent01", SpawnedBy: "child001"},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatalf("create %s: %v", sess.ID, err)
		}
	}
	grand, err := st.Get("child002")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if grand.ParentID != "parent01" {
		t.Fatalf("parent = %q, want the one level the board draws", grand.ParentID)
	}
	if got := SpawnerOf(grand); got != "child001" {
		t.Fatalf("SpawnerOf = %q, want the session that spawned it", got)
	}
}
