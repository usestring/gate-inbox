package launch

import (
	"testing"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestADriverLeavesNoControlClientBehind is the assertion that keeps this
// package's share of the leak from coming back. The one tmux-driving test here
// used to run on a fixed socket, close nothing and kill nothing, so every run
// left both a server and the control client its capture opened.
func TestADriverLeavesNoControlClientBehind(t *testing.T) {
	tmuxtest.AssertNoLeakOnItsOwnSocket(t, func(t *testing.T) string {
		socket := tmuxtest.NewSocket("launch")
		driver, err := tmux.NewWithSocket(socket)
		if err != nil {
			t.Fatalf("driver: %v", err)
		}
		t.Cleanup(func() {
			driver.CloseCaptureClients()
			tmuxtest.KillServer(socket)
			tmuxtest.ReapSocket(socket)
		})
		const id = "leakguard"
		command := mustRevive(t, config.Tool{ResumeByIDCommand: "echo resume {id}"}, "abc", "")
		if err := driver.Create(id, t.TempDir(), command, map[string]string{}, 80, 24); err != nil {
			t.Fatalf("create: %v", err)
		}
		t.Cleanup(func() { driver.Kill(id) })
		// PaneState is the point: it is what makes the driver open a pooled
		// control-mode client, which is the process that leaks. Captures no
		// longer open one -- they fork, so tmux cannot retain their replies
		// (tmux/tmux#5553) -- so a capture here would assert nothing.
		if _, err := driver.PaneState(id, "#{pane_id}"); err != nil {
			t.Fatalf("pane state: %v", err)
		}
		return socket
	})
}
