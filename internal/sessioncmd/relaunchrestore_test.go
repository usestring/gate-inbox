package sessioncmd

import (
	"path/filepath"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
)

// Live 2026-10-12: six archived rows held running agents, relaunched after
// the archive ended them, where no sweep would ever look again.
func TestRestoreRelaunchedBringsAnArchivedRowBack(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sess := store.Session{ID: "a1b2c3d4", Name: "manager", Tool: "claude", Cwd: "/", Status: "idle"}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.SetArchived(sess.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	sess.Archived = true
	if err := restoreRelaunched(st, sess); err != nil {
		t.Fatalf("restoreRelaunched: %v", err)
	}
	got, err := st.Get(sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Archived {
		t.Fatal("a relaunched row is still archived")
	}
}
