package store

import (
	"os"
	"path/filepath"
	"testing"
)

func tier(t *testing.T, st *Store, id string) string {
	t.Helper()
	var tier string
	if err := st.db.QueryRow(`SELECT priority_tier FROM sessions WHERE id = ?`, id).Scan(&tier); err != nil {
		t.Fatal(err)
	}
	return tier
}

// untiered is a row as a binary from before priority tiers wrote it, which
// the migrations rewrite to High.
func untiered(t *testing.T, st *Store, id string) {
	t.Helper()
	if err := st.CreateSession(sample(id, "g1")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE sessions SET priority = 1, priority_tier = '' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
}

func TestOpenMigratesAFileOncePerProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	untiered(t, st, "a")
	st.Close()

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if tier(t, again, "a") != "" {
		t.Fatal("a second Open of the same file ran the migrations again")
	}
	again.Close()

	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(path + suffix)
	}
	// A new file at the same path, which can reuse the inode, starts unmigrated.
	fresh, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	untiered(t, fresh, "b")
	fresh.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if tier(t, reopened, "b") != "" {
		t.Fatal("a recreated file migrated twice in one process")
	}
	if _, err := reopened.db.Exec(`ALTER TABLE groups ADD COLUMN scratch TEXT`); err != nil {
		t.Fatal(err)
	}
	changed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer changed.Close()
	if tier(t, changed, "b") != "high" {
		t.Fatal("a schema change since the last Open did not migrate again")
	}
}
