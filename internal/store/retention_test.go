package store

import (
	"github.com/usestring/gate-inbox/internal/tmuxtest"
	"path/filepath"
	"testing"
	"time"
)

// openRetentionStore is a store with one archivable session already in it.
func openRetentionStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(tmuxtest.ScratchDir(t), "retention.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.CreateSession(Session{ID: "one", Name: "filed", Tool: "claude", Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create: %v", err)
	}
	return s
}

// The window runs from the moment the row was filed, and only a row that is
// still archived when it runs out is expired.
func TestExpiredArchivesSelectsOnlyExpiredArchives(t *testing.T) {
	s := openRetentionStore(t)

	if expired, err := s.ExpiredArchives(time.Now()); err != nil || len(expired) != 0 {
		t.Fatalf("a live row is never expired, got %d rows (%v)", len(expired), err)
	}

	if err := s.SetArchived("one", true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	sess, err := s.Get("one")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sess.ArchivedAt.IsZero() {
		t.Fatal("archiving should start the clock")
	}
	if expired, err := s.ExpiredArchives(sess.ArchivedAt); err != nil || len(expired) != 0 {
		t.Fatalf("a row inside its window is not expired, got %d rows (%v)", len(expired), err)
	}
	expired, err := s.ExpiredArchives(sess.ArchivedAt.Add(time.Second))
	if err != nil || len(expired) != 1 || expired[0].ID != "one" {
		t.Fatalf("a row past its window should be expired, got %+v (%v)", expired, err)
	}
}

// A restore takes the row off the clock entirely, so filing it again buys a
// whole fresh window rather than the remainder of the first.
func TestRestoreClearsTheRetentionClock(t *testing.T) {
	s := openRetentionStore(t)
	if err := s.SetArchived("one", true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	filed, err := s.Get("one")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := s.SetArchived("one", false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	back, err := s.Get("one")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !back.ArchivedAt.IsZero() {
		t.Fatalf("restore should clear the clock, got %v", back.ArchivedAt)
	}
	if expired, err := s.ExpiredArchives(filed.ArchivedAt.Add(time.Hour)); err != nil || len(expired) != 0 {
		t.Fatalf("a restored row is not on the clock, got %d rows (%v)", len(expired), err)
	}
}

// A row archived before the column existed carries no stamp, and an
// unmeasured stay waits for a person rather than being swept on the first
// poll after the upgrade.
func TestUnstampedArchiveIsNeverExpired(t *testing.T) {
	s := openRetentionStore(t)
	if _, err := s.db.Exec(`UPDATE sessions SET archived = 1, archived_at = 0 WHERE id = ?`, "one"); err != nil {
		t.Fatalf("seed legacy archive: %v", err)
	}
	if expired, err := s.ExpiredArchives(time.Now().Add(10000 * time.Hour)); err != nil || len(expired) != 0 {
		t.Fatalf("an unstamped archive should never expire, got %d rows (%v)", len(expired), err)
	}
}
