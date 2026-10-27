package store

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/status"
)

// The board this was measured on held 178 live rows and 499 archived ones,
// each archived row carrying a launch prompt of about 2KB.
const (
	benchLiveRows     = 178
	benchArchivedRows = 499
)

// delegationBenchStore is a board of that shape, or a copy of a real one
// named by GATE_INBOX_BENCH_DB. Never point it at a live state.db: Open
// migrates it.
func delegationBenchStore(b *testing.B) *Store {
	b.Helper()
	if path := os.Getenv("GATE_INBOX_BENCH_DB"); path != "" {
		st, err := Open(path)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { st.Close() })
		return st
	}
	st, err := Open(filepath.Join(b.TempDir(), "delegation.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { st.Close() })
	prompt := strings.Repeat("brief ", 340)
	for i := range benchLiveRows + benchArchivedRows {
		sess := Session{ID: fmt.Sprintf("s%03d", i), Tool: "claude", LaunchPrompt: prompt}
		if i%4 != 0 {
			sess.ParentID = fmt.Sprintf("s%03d", i-i%4)
		}
		if err := st.CreateSession(sess); err != nil {
			b.Fatal(err)
		}
		state := status.Finished
		if i%3 == 0 {
			state = status.Working
		}
		if _, err := st.db.Exec(`UPDATE sessions SET status = ?, archived = ? WHERE id = ?`,
			state, i >= benchLiveRows, sess.ID); err != nil {
			b.Fatal(err)
		}
	}
	return st
}

func BenchmarkDelegationOverlay(b *testing.B) {
	st := delegationBenchStore(b)
	reads := []struct {
		name string
		read func() ([]Session, error)
	}{
		{"all", func() ([]Session, error) { return st.ListSessions(true) }},
		{"live", func() ([]Session, error) { return st.ListSessions(false) }},
		{"slim", st.DelegationSessions},
	}
	overlay := func(read func() ([]Session, error)) map[string]bool {
		sessions, err := read()
		if err != nil {
			b.Fatal(err)
		}
		pending, err := st.PendingChildReports()
		if err != nil {
			b.Fatal(err)
		}
		ids := map[string]bool{}
		for _, sess := range ApplyDelegation(sessions, pending, nil) {
			if sess.Status == status.Delegating {
				ids[sess.ID] = true
			}
		}
		return ids
	}
	want := overlay(reads[0].read)
	for _, r := range reads[1:] {
		if got := overlay(r.read); !maps.Equal(got, want) {
			b.Fatalf("%s overlay %v, want %v", r.name, slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
		}
	}
	for _, r := range reads {
		b.Run(r.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				overlay(r.read)
			}
		})
	}
}
