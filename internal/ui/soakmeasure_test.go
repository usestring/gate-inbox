package ui

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
)

// TestSoakSessionChurn is the measurement behind the leak audit, not a gate.
//
// It creates and kills real tmux sessions in rounds and reports goroutines,
// open file descriptors and live heap after each one, so the numbers in the
// audit are readings rather than assertions. It is skipped unless
// GATE_INBOX_SOAK is set: it costs a real tmux server, a couple of minutes,
// and its output is a table for a human rather than a pass or a fail.
func TestSoakSessionChurn(t *testing.T) {
	if os.Getenv("GATE_INBOX_SOAK") == "" {
		t.Skip("set GATE_INBOX_SOAK=1 to run the churn soak")
	}
	m := buildModel(t)
	dir := t.TempDir()

	report := func(tag string) {
		runtime.GC()
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", os.Getpid()))
		open := -1
		if err == nil {
			open = len(fds)
		}
		t.Logf("%-12s goroutines=%3d fds=%3d heapalloc=%d", tag, runtime.NumGoroutine(), open, stats.HeapAlloc)
	}

	report("baseline")
	const rounds = 12
	for round := 0; round < rounds; round++ {
		var ids []string
		for i := 0; i < 4; i++ {
			name := fmt.Sprintf("soak-%d-%d", round, i)
			createSession(t, m, name, dir, "")
			for _, sess := range m.sessions {
				if sess.Name == name {
					ids = append(ids, sess.ID)
				}
			}
		}
		// Drive the passes that write the per-session state: a poll, and the
		// work refresh that feeds the tracker.
		m.applyCmd(t, m.refreshCmd())
		if cmd := m.refreshWork(); cmd != nil {
			cmd()
		}
		for _, id := range ids {
			m.tmux.Kill(id)
		}
		m.applyCmd(t, m.refreshCmd())
		if cmd := m.refreshWork(); cmd != nil {
			cmd()
		}
		time.Sleep(100 * time.Millisecond)
		report(fmt.Sprintf("round=%d", round))
	}
	// What the tracker retains across this churn is asserted directly in
	// internal/worktracker; here the readings above are the evidence.
}
