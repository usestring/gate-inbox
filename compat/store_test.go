package compat

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/store"
	_ "modernc.org/sqlite"
)

// TestStoreSchema records the state database a fresh board creates: every
// table, column, index and trigger, and the schema version pragma. A
// migration in core, or a table an extension adds to the core store, shows
// up here as a diff.
func TestStoreSchema(t *testing.T) {
	s := newScratch(t)
	path := filepath.Join(s.home, "state.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	// Opening an existing store runs the migrations again; the schema it
	// leaves must be the same one.
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	db := openRaw(t, path)
	var b strings.Builder
	for _, pragma := range []string{"user_version", "application_id", "journal_mode"} {
		var value string
		if err := db.QueryRow("PRAGMA " + pragma).Scan(&value); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "PRAGMA %s = %s\n", pragma, value)
	}

	type object struct{ kind, name, table, sql string }
	var objects []object
	rows, err := db.Query(`SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.kind, &o.name, &o.table, &o.sql); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, o)
	}
	rows.Close()

	for _, o := range objects {
		fmt.Fprintf(&b, "\n=== %s %s", o.kind, o.name)
		if o.table != o.name {
			fmt.Fprintf(&b, " on %s", o.table)
		}
		b.WriteString("\n")
		if o.kind != "table" {
			fmt.Fprintf(&b, "%s\n", o.sql)
			continue
		}
		columns, err := db.Query(`SELECT cid, name, type, "notnull", COALESCE(dflt_value, 'NULL'), pk FROM pragma_table_info(?) ORDER BY cid`, o.name)
		if err != nil {
			t.Fatal(err)
		}
		for columns.Next() {
			var cid, notNull, pk int
			var name, kind, dflt string
			if err := columns.Scan(&cid, &name, &kind, &notNull, &dflt, &pk); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "  %2d %-28s %-8s notnull=%d default=%s pk=%d\n", cid, name, kind, notNull, dflt, pk)
		}
		columns.Close()
		indexes, err := db.Query(`SELECT name, "unique", origin, partial FROM pragma_index_list(?) ORDER BY name`, o.name)
		if err != nil {
			t.Fatal(err)
		}
		for indexes.Next() {
			var name, origin string
			var unique, partial int
			if err := indexes.Scan(&name, &unique, &origin, &partial); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "  index %s unique=%d origin=%s partial=%d (%s)\n", name, unique, origin, partial, indexColumns(t, db, name))
		}
		indexes.Close()
	}
	golden(t, "store/schema.golden", b.String())
}

func indexColumns(t *testing.T, db *sql.DB, index string) string {
	t.Helper()
	rows, err := db.Query(`SELECT COALESCE(name, '<expr>') FROM pragma_index_info(?) ORDER BY seqno`, index)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// openRaw opens a state database read-only, beside whatever store handle
// the test holds.
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
