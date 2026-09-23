package ui

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestATestDriverLeavesNoTmuxClientBehind is the assertion that keeps the leak
// from coming back. A previous reaping of these strays was undone within days
// because nothing failed when they returned.
//
// The reaper and the shape of this assertion live in internal/tmuxtest, which
// is what lets every other package that drives tmux state the same guard
// rather than copy one. The predicate tests that used to sit here -- the ones
// covering which sockets the reaper will touch -- moved there with it.
func TestATestDriverLeavesNoTmuxClientBehind(t *testing.T) {
	tmuxtest.AssertNoLeak(t, testSocket, func(t *testing.T) {
		m := buildModel(t)
		const id = "leakguard"
		if err := m.tmux.Create(id, t.TempDir(), "cat", nil, 80, 24); err != nil {
			t.Fatalf("create: %v", err)
		}
		t.Cleanup(func() { m.tmux.Kill(id) })
		// PaneState is the point: it is what makes the driver open a pooled
		// control-mode client, which is the process that leaks. Captures no
		// longer open one -- they fork, so tmux cannot retain their replies
		// (tmux/tmux#5553) -- so a capture here would assert nothing.
		if _, err := m.tmux.PaneState(id, "#{pane_id}"); err != nil {
			t.Fatalf("pane state: %v", err)
		}
	})
}
