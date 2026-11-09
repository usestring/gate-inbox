// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// Agents reserving the same path at the same moment are the collision the
// lease exists to report. Each holder opens its own handle on the shared
// database, the way separate processes do, so a conflict read that ran
// before someone else's write would leave both of them believing the path
// was theirs alone.
func TestConcurrentReservationsLeaveOnlyTheFirstHolderUnaware(t *testing.T) {
	const holders, rounds = 8, 20
	path := filepath.Join(tmuxtest.ScratchDir(t), "test.db")
	stores := make([]*Store, holders)
	for index := range stores {
		st, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { st.Close() })
		stores[index] = st
	}

	for round := range rounds {
		pattern := fmt.Sprintf("internal/store/round%d.go", round)
		now := time.Now()
		clashCounts := make([]int, holders)
		failures := make([]error, holders)
		start := make(chan struct{})
		var ready, running sync.WaitGroup
		for index := range stores {
			ready.Add(1)
			running.Add(1)
			go func(index int) {
				defer running.Done()
				ready.Done()
				<-start
				clashes, err := stores[index].Reserve([]Reservation{{
					ID:         fmt.Sprintf("r%d-%d", round, index),
					SessionID:  fmt.Sprintf("s%d", index),
					Pattern:    pattern,
					Mode:       ReservationExclusive,
					AcquiredAt: now,
					ExpiresAt:  now.Add(time.Hour),
				}})
				clashCounts[index], failures[index] = len(clashes), err
			}(index)
		}
		ready.Wait()
		close(start)
		running.Wait()

		unaware := 0
		for index, err := range failures {
			if err != nil {
				t.Fatalf("holder %d: %v", index, err)
			}
			if clashCounts[index] == 0 {
				unaware++
			}
		}
		if unaware != 1 {
			t.Fatalf("round %d: %d of %d holders were never told about the clash; only the one that got there first may miss it",
				round, unaware, holders)
		}
	}
}

// One foreign lease overlapping two of the caller's patterns is one
// holder, not two, so it comes back once.
func TestReserveReportsOneOverlappingLeaseOnce(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	if _, err := st.Reserve([]Reservation{{
		ID: "held0001", SessionID: "rival", Pattern: "internal/cli",
		Mode: ReservationExclusive, AcquiredAt: now, ExpiresAt: now.Add(time.Hour),
	}}); err != nil {
		t.Fatalf("seed the rival lease: %v", err)
	}
	conflicts, err := st.Reserve([]Reservation{
		{ID: "mine0001", SessionID: "me", Pattern: "internal/*",
			Mode: ReservationExclusive, AcquiredAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "mine0002", SessionID: "me", Pattern: "internal/cli",
			Mode: ReservationExclusive, AcquiredAt: now, ExpiresAt: now.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].ID != "held0001" {
		t.Fatalf("conflicts = %+v, want the rival lease once", conflicts)
	}
}

func TestLegacyReservationUsesTheHoldersDirectory(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	if err := st.CreateSession(Session{ID: "holder", Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// The old insert did not record the resolved path or glob.
	for _, lease := range []struct{ id, holder, pattern string }{
		{"known", "holder", "dir/*.md"},
		{"unknown", "orphan", "*.md"},
	} {
		if _, err := st.db.Exec(`
INSERT INTO file_reservations (id, session_id, pattern, acquired_at, expires_at)
VALUES (?, ?, ?, ?, ?)`, lease.id, lease.holder, lease.pattern, encodeTime(now), encodeTime(now.Add(time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	conflicts, err := st.Reserve([]Reservation{{
		ID: "new", SessionID: "rival", Pattern: filepath.Join(dir, "dir", "file.md"),
		Mode: ReservationExclusive, AcquiredAt: now, ExpiresAt: now.Add(time.Hour),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].ID != "known" || conflicts[0].Pattern != "dir/*.md" {
		t.Fatalf("conflicts = %+v, want only the lease with a known directory", conflicts)
	}
	if n, err := st.Release("orphan", "*.md"); err != nil || n != 1 {
		t.Fatalf("release the unscoped lease by its original spelling = %d, %v", n, err)
	}
}

func TestReleaseUsesTheListedSpellingAfterTheSessionMoves(t *testing.T) {
	st := newTestStore(t)
	oldDir, newDir := t.TempDir(), t.TempDir()
	const session = "holder"
	if err := st.CreateSession(Session{ID: session, Cwd: oldDir}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	relative := filepath.Join("dir", "file.md")
	if _, err := st.Reserve([]Reservation{{
		ID: "old", SessionID: session, Pattern: relative, Mode: ReservationExclusive,
		AcquiredAt: now, ExpiresAt: now.Add(time.Hour),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.PromoteAdopted(session, newDir, ""); err != nil {
		t.Fatal(err)
	}
	// The same relative name now points at another live lease in the new
	// checkout. Releasing the listed spelling should leave that lease alone.
	if _, err := st.Reserve([]Reservation{{
		ID: "new", SessionID: session, Pattern: filepath.Join(newDir, relative),
		Mode: ReservationExclusive, AcquiredAt: now, ExpiresAt: now.Add(time.Hour),
	}}); err != nil {
		t.Fatal(err)
	}
	if n, err := st.Release(session, relative); err != nil || n != 1 {
		t.Fatalf("release by listed spelling = %d, %v; want one lease released", n, err)
	}
	leases, err := st.Reservations(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].ID != "new" {
		t.Fatalf("remaining leases = %+v, want only the lease in the new directory", leases)
	}
}

func TestRenewalCoalescesLegacyPathAliases(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	const session = "holder"
	if err := st.CreateSession(Session{ID: session, Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	relative := filepath.Join("dir", "file.md")
	absolute := filepath.Join(dir, relative)
	for _, lease := range []struct {
		id, holder, pattern string
		acquiredAt          time.Time
	}{
		{"z-first", session, relative, now.Add(-2 * time.Minute)},
		{"a-alias", session, absolute, now.Add(-time.Minute)},
		{"other", "rival", absolute, now.Add(-time.Minute)},
	} {
		if _, err := st.db.Exec(`
INSERT INTO file_reservations (id, session_id, pattern, acquired_at, expires_at)
VALUES (?, ?, ?, ?, ?)`, lease.id, lease.holder, lease.pattern, encodeTime(lease.acquiredAt), encodeTime(now.Add(time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	expires := now.Add(2 * time.Hour)
	if _, err := st.Reserve([]Reservation{{
		ID: "renew", SessionID: session, Pattern: absolute, Mode: ReservationShared,
		Note: "renewed", AcquiredAt: now, ExpiresAt: expires,
	}}); err != nil {
		t.Fatal(err)
	}
	leases, err := st.Reservations(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 2 || leases[0].ID != "z-first" || leases[1].ID != "other" {
		t.Fatalf("leases = %+v, want the original lease and the other holder's lease", leases)
	}
	if got := leases[0]; got.Pattern != relative || got.Mode != ReservationShared || got.Note != "renewed" || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("renewed lease = %+v, want original spelling and renewed mode, note and expiry", got)
	}
}
