package ui

import (
	"database/sql"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	_ "modernc.org/sqlite"
)

// heldWrite is how long the stand-in holds the database's write lock. Longer
// than a keypress should ever take and shorter than the store's 5s
// busy_timeout, so a handler that writes on the loop blocks for the whole of
// it rather than failing fast and looking quick.
const heldWrite = 2 * time.Second

// keyLatencyBudget is what a keypress and the frame after it may cost while
// another writer holds the database. Generous against heldWrite on purpose:
// the point is the difference between milliseconds and the whole write, not a
// millisecond count a loaded box would flake on.
const keyLatencyBudget = heldWrite / 4

// holdTheWriteLock takes SQLite's write lock from outside the store, the way
// the `gate-inbox mcp` processes that share this database do, and holds it.
//
// A second connection rather than the store's own is what makes this a test of
// the handler instead of a test of the pool: the store is free the whole time,
// and only a write actually issued on the event loop can block.
func holdTheWriteLock(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)&_txlock=immediate")
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES('latency_probe','1')
		ON CONFLICT(key) DO UPDATE SET value = '1'`); err != nil {
		tx.Rollback()
		db.Close()
		t.Fatalf("take the write lock: %v", err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(heldWrite)
		tx.Rollback()
		db.Close()
		close(released)
	}()
	t.Cleanup(func() { <-released })
}

// Pressing a key must never wait on the database. The store keeps one
// connection and a poll pass occupies it for seconds at a time, so a handler
// that writes on the event loop hands the operator a key that does nothing --
// which is exactly what pressing "i" did.
func TestTriageKeyDoesNotWaitForTheDatabase(t *testing.T) {
	m, dbPath := buildModelWithStorePath(t)
	createSession(t, m, "one", t.TempDir(), "")
	holdTheWriteLock(t, dbPath)

	started := time.Now()
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'i'})
	*m = *updated.(*Model)
	painted := m.frame()
	latency := time.Since(started)
	t.Logf("triage keypress + frame: %v (another writer holds the database for %v)",
		latency.Round(time.Microsecond), heldWrite)

	if latency > keyLatencyBudget {
		t.Fatalf("the triage keypress and its frame took %v while another writer held the database for %v: the write is still on the event loop",
			latency, heldWrite)
	}
	if painted == "" {
		t.Fatal("no frame was painted")
	}
	if !m.triage {
		t.Fatal("the keypress did not turn triage on, so this proves nothing about its cost")
	}
	if cmd == nil {
		t.Fatal("no command was returned, so the write was not deferred anywhere")
	}
}

// The deferred write still has to happen, and still has to report a failure.
// A key that feels instant because its work was dropped is not a fix.
func TestTheDeferredTriageWriteStillLands(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "one", t.TempDir(), "")

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'i'})
	*m = *updated.(*Model)
	if msg := m.persistTriage()(); msg != nil {
		if err, isErr := msg.(errMsg); isErr {
			t.Fatalf("the deferred write failed: %v", err.err)
		}
	}
	if !storedTriage(m.store) {
		t.Fatal("triage was turned on but the setting was never written")
	}
}
