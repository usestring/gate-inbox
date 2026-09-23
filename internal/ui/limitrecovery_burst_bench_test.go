package ui

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

func limitBurstModel(t testing.TB, size int) (*Model, []store.Session) {
	t.Helper()
	m := buildModel(t)
	configureLimitCodex(t, m)
	// Session ids name the pane's launch script under the shared temp dir, so
	// a fixed id lets a concurrent run of this fixture overwrite or delete the
	// script before this pane has read it.
	prefix := fmt.Sprintf("burst-%08x", rand.Uint32())
	command := `banner="You've hit your usage limit. Try again at Jan 1st, 2020 9:00 PM."; printf '%s\n› ' "$banner"; cat | while IFS= read -r line; do printf '\033[2J\033[H%s\n%s\n› ' "$line" "$banner"; done`
	ids := make([]string, size)
	for i := range size {
		id := fmt.Sprintf("%s-%d", prefix, i)
		ids[i] = id
		if err := m.store.CreateSession(store.Session{ID: id, Name: id, Tool: "codex", Cwd: "/tmp", Status: status.Errored}); err != nil {
			t.Fatal(err)
		}
		if err := m.tmux.Create(id, "/tmp", command, nil, 160, 24); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { m.tmux.Kill(id) })
	}
	// A pass skips a pane that has not drawn its banner yet, so a shell that
	// starts late sits out the first pass and can still be blank on the
	// second, leaving the drain one short however the cap behaves.
	for _, id := range ids {
		awaitLimitBanner(t, m, id)
	}
	sessions, err := m.store.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	return m, sessions
}

func awaitLimitBanner(t testing.TB, m *Model, id string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		pane, err := m.tmux.CapturePane(id)
		clean := ansi.Strip(pane)
		if err == nil && strings.Contains(clean, "You've hit your usage limit") && strings.Contains(clean, "›") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never drew its usage-limit banner, last capture %q (err %v)", id, clean, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func BenchmarkLimitRecoveryBurst(b *testing.B) {
	for _, size := range []int{10, 100} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			m, sessions := limitBurstModel(b, size)
			var maxPass time.Duration
			polls := 0
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				states, err := m.store.LimitRecoveries()
				if err != nil {
					b.Fatal(err)
				}
				for _, sess := range sessions {
					if old, ok := states[sess.ID]; ok {
						if _, err := m.store.CompareLimitRecovery(sess, &old, nil); err != nil {
							b.Fatal(err)
						}
					}
				}
				attempted := 0
				for pass := 0; attempted < size && pass <= size/limitRecoveriesPerPass; pass++ {
					b.StartTimer()
					started := time.Now()
					if failed, ok := m.poller.refreshOnce().(errMsg); ok {
						b.Fatal(failed.err)
					}
					elapsed := time.Since(started)
					b.StopTimer()
					if elapsed > maxPass {
						maxPass = elapsed
					}
					polls++
					states, err = m.store.LimitRecoveries()
					if err != nil {
						b.Fatal(err)
					}
					nextAttempted := 0
					for _, state := range states {
						if !state.AttemptedAt.IsZero() {
							nextAttempted++
						}
					}
					if nextAttempted-attempted > limitRecoveriesPerPass {
						b.Fatal("recovery exceeded the per-pass budget")
					}
					attempted = nextAttempted
				}
				if attempted != size {
					b.Fatalf("resumed %d/%d sessions", attempted, size)
				}
				for _, sess := range sessions {
					pane, err := m.tmux.CapturePane(sess.ID)
					if err != nil {
						b.Fatal(err)
					}
					if !strings.Contains(ansi.Strip(pane), "reported usage-limit reset time") {
						b.Fatalf("continuation missing from %s", sess.ID)
					}
				}
				b.StartTimer()
			}
			b.ReportMetric(float64(maxPass)/float64(time.Millisecond), "max-pass-ms")
			b.ReportMetric(float64(polls)/float64(b.N), "polls/op")
		})
	}
}

func TestLimitRecoveryBurstDrainsWithinThePerPassBudget(t *testing.T) {
	needsQuietBox(t)
	m, sessions := limitBurstModel(t, limitRecoveriesPerPass+1)
	for _, want := range []int{limitRecoveriesPerPass, len(sessions)} {
		if failed, ok := m.poller.refreshOnce().(errMsg); ok {
			t.Fatal(failed.err)
		}
		states, err := m.store.LimitRecoveries()
		if err != nil {
			t.Fatal(err)
		}
		attempted := 0
		for _, state := range states {
			if !state.AttemptedAt.IsZero() {
				attempted++
			}
		}
		if attempted != want {
			t.Fatalf("attempted %d continuations, want %d", attempted, want)
		}
	}
}
