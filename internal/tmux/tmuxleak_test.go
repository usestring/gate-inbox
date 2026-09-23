package tmux

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestADriverLeavesNoControlClientBehind is the assertion that keeps this
// package's share of the leak from coming back. requireTmux handed out drivers
// with no cleanup at all, so every test that captured a pane left a
// control-mode client running with nothing to collect it: kill-server cannot,
// because the client outlives the server.
func TestADriverLeavesNoControlClientBehind(t *testing.T) {
	tmuxtest.AssertNoLeak(t, testSocket, func(t *testing.T) {
		driver := requireTmux(t)
		const id = "leakguard"
		if err := driver.Create(id, t.TempDir(), "cat", nil, 80, 24); err != nil {
			t.Fatalf("create: %v", err)
		}
		t.Cleanup(func() { driver.Kill(id) })
		// PaneState is the point: it is what makes the driver open a pooled
		// control-mode client, which is the process that leaks. Captures no
		// longer open one -- they fork, to keep tmux from retaining their
		// replies (tmux/tmux#5553) -- so a capture here would leave this
		// guard asserting nothing.
		if _, err := driver.PaneState(id, "#{pane_id}"); err != nil {
			t.Fatalf("pane state: %v", err)
		}
	})
}
