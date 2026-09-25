package store

import (
	"path/filepath"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestOpenKeepsTablesItDoesNotCreate adds a table and rows the current schema
// does not know to a live database, the way an older build might have left
// one, and proves that reopening it and deleting a session leave them intact.
func TestOpenKeepsTablesItDoesNotCreate(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "state.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.CreateSession(sample("a", "g1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE older_rows (session_id TEXT PRIMARY KEY REFERENCES sessions(id), note TEXT NOT NULL)`,
		`INSERT INTO older_rows (session_id, note) VALUES ('a', 'kept'), ('b', 'also kept')`,
		`INSERT INTO settings (key, value) VALUES ('older/a', 'kept')`,
	} {
		if _, err := st.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })
	if err := reopened.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var rows int
	if err := reopened.db.QueryRow(`SELECT count(*) FROM older_rows WHERE note IN ('kept', 'also kept')`).Scan(&rows); err != nil {
		t.Fatalf("read older_rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("older_rows holds %d of its 2 rows after reopen and delete", rows)
	}
	var value string
	if err := reopened.db.QueryRow(`SELECT value FROM settings WHERE key = 'older/a'`).Scan(&value); err != nil {
		t.Fatalf("read settings row: %v", err)
	}
	if value != "kept" {
		t.Fatalf("settings row = %q, want kept", value)
	}
}
