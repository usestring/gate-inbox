package store

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

// contendedRows is what one burst moves: a stall upstream leaves a good part
// of the board looking different on the pass after it.
const contendedRows = 20

// contendingWriters stands in for the `gate-inbox mcp` helpers -- one per
// session, each opening this database, writing, and closing.
const contendingWriters = 40

// peerPeriod is roughly how often one helper writes. Left at zero the peers
// hammer hard enough to exhaust the 5s busy timeout outright -- which is the
// board's own worst passes, but too coarse to measure a fix against.
const peerPeriod = 50 * time.Millisecond

// busyPeers runs writers that each commit a short multi-statement transaction
// on a duty cycle, which is the shape every mcp tool write has.
func busyPeers(b *testing.B, path string, n int) func() {
	b.Helper()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			peer, err := Open(path)
			if err != nil {
				return
			}
			defer peer.Close()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tx, err := peer.db.Begin()
				if err != nil {
					continue
				}
				for k := 0; k < 8; k++ {
					tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
						ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
						fmt.Sprintf("peer%d-%d", w, k), time.Now().String())
				}
				tx.Commit()
				time.Sleep(peerPeriod)
			}
		}(w)
	}
	time.Sleep(500 * time.Millisecond)
	return func() {
		close(stop)
		wg.Wait()
	}
}

func benchSetup(b *testing.B) (*Store, string, []string) {
	b.Helper()
	st, path := openSharedStore(b)
	return st, path, seedDerived(b, st, contendedRows)
}

// BenchmarkDerivedStatesBatched is one pass's worth of row state as the poller
// writes it now: one transaction, one acquisition of the contended write lock.
func BenchmarkDerivedStatesBatched(b *testing.B) {
	st, path, ids := benchSetup(b)
	release := busyPeers(b, path, contendingWriters)
	defer release()
	states := make([]DerivedState, 0, len(ids))
	for _, id := range ids {
		states = append(states, DerivedState{ID: id, Status: status.Working})
	}
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		next := status.Working
		if i%2 == 0 {
			next = status.Idle
		}
		for j := range states {
			states[j].Status = next
		}
		if err := st.ApplyDerivedStates(time.Now(), states); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDerivedStatesPerRow is the same pass written a statement at a time,
// which is what it used to be. The gap between the two is the fix.
func BenchmarkDerivedStatesPerRow(b *testing.B) {
	st, path, ids := benchSetup(b)
	release := busyPeers(b, path, contendingWriters)
	defer release()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		next := status.Working
		if i%2 == 0 {
			next = status.Idle
		}
		for _, id := range ids {
			if err := st.UpdateStatus(id, next); err != nil {
				b.Fatal(err)
			}
		}
	}
}
