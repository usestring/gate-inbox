package sessioncmd

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestAHarnessLeavesNoControlClientBehind is the assertion that keeps this
// package's share of the leak from coming back. Both harnesses killed their
// tmux server in cleanup and stopped there, which reads as thorough and is
// not: a pooled control-mode client is a process that outlives the server it
// attached to, so kill-server is exactly the thing that cannot collect it.
func TestAHarnessLeavesNoControlClientBehind(t *testing.T) {
	t.Parallel()
	tmuxtest.AssertNoLeakOnItsOwnSocket(t, func(t *testing.T) string {
		h := newTerminalHarness(t)
		created, err := h.terminals.Create(h.caller.ID, CreateTerminalOptions{})
		if err != nil {
			t.Fatalf("create terminal: %v", err)
		}
		// The read is the point: it is what makes the driver open a
		// pooled control-mode client, which is the process that leaks.
		if _, err := h.terminals.Read(h.caller.ID, created.ID); err != nil {
			t.Fatalf("read terminal: %v", err)
		}
		return h.driver.SocketName()
	})
}
