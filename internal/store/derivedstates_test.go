package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// openSharedStore opens a store and hands back its path too, so a test or a
// benchmark can open further connections against the same file the way the mcp
// helpers do.
func openSharedStore(t testing.TB) (*Store, string) {
	t.Helper()
	path := filepath.Join(tmuxtest.ScratchDir(t), "derived.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

func seedDerived(t testing.TB, st *Store, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("s%02d", i)
		if err := st.CreateSession(sample(id, "g")); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestApplyDerivedStatesWritesBothColumns(t *testing.T) {
	st := newTestStore(t)
	ids := seedDerived(t, st, 3)
	at := time.Now().Add(-90 * time.Minute).Truncate(time.Second)
	// The acked-only row's status clock must come through untouched.
	untouched, err := st.Get(ids[1])
	if err != nil {
		t.Fatal(err)
	}
	before := untouched.LastStatusAt
	acked := true
	if err := st.ApplyDerivedStates(at, []DerivedState{
		{ID: ids[0], Status: status.Working},
		{ID: ids[1], Acked: &acked},
		{ID: ids[2], Status: status.Waiting, Acked: &acked},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	first, err := st.Get(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != status.Working {
		t.Errorf("status = %q, want %q", first.Status, status.Working)
	}
	if first.Acked {
		t.Error("a state with no Acked moved the acked column")
	}
	if !first.LastStatusAt.Equal(at) {
		t.Errorf("last_status_at = %v, want the pass's own instant %v", first.LastStatusAt, at)
	}

	second, err := st.Get(ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if !second.Acked {
		t.Error("acked not applied")
	}
	if second.Status != "idle" {
		t.Errorf("a state with no Status moved the status column to %q", second.Status)
	}
	if !second.LastStatusAt.Equal(before) {
		t.Errorf("acked-only write restamped last_status_at from %v to %v",
			before, second.LastStatusAt)
	}

	third, err := st.Get(ids[2])
	if err != nil {
		t.Fatal(err)
	}
	if third.Status != status.Waiting || !third.Acked {
		t.Errorf("both columns: status=%q acked=%v", third.Status, third.Acked)
	}
}

// A row deleted between the pass listing it and the flush is skipped. Every
// single-row write this replaces was wrapped in ignoreDeletedSession, so a
// vanished session must not fail the batch or lose the rows beside it.
func TestApplyDerivedStatesSkipsDeletedRows(t *testing.T) {
	st := newTestStore(t)
	ids := seedDerived(t, st, 2)
	if err := st.Delete(ids[0]); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := st.ApplyDerivedStates(time.Now(), []DerivedState{
		{ID: ids[0], Status: status.Working},
		{ID: ids[1], Status: status.Working},
	}); err != nil {
		t.Fatalf("apply over a deleted row: %v", err)
	}
	survivor, err := st.Get(ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if survivor.Status != status.Working {
		t.Errorf("row beside the deleted one = %q, want %q", survivor.Status, status.Working)
	}
}

// The property the fix rests on: the whole pass's row state lands in ONE
// write transaction. A reader on its own connection therefore never sees a
// batch half-applied -- which is only true if the batch takes the write lock
// once rather than once per row.
func TestApplyDerivedStatesIsOneTransaction(t *testing.T) {
	st, path := openSharedStore(t)
	const rows = 12
	ids := seedDerived(t, st, rows)

	reader, err := Open(path)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	defer reader.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	torn := make(chan string, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			sessions, err := reader.ListSessions(true)
			if err != nil {
				continue
			}
			seen := map[string]int{}
			for _, sess := range sessions {
				seen[sess.Status]++
			}
			if len(seen) > 1 {
				select {
				case torn <- fmt.Sprint(seen):
				default:
				}
				return
			}
		}
	}()

	for round := 0; round < 60; round++ {
		next := status.Working
		if round%2 == 0 {
			next = status.Idle
		}
		states := make([]DerivedState, 0, rows)
		for _, id := range ids {
			states = append(states, DerivedState{ID: id, Status: next})
		}
		if err := st.ApplyDerivedStates(time.Now(), states); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	select {
	case mix := <-torn:
		t.Fatalf("a reader saw the board half-written (%s): the pass is not one transaction", mix)
	default:
	}
}
