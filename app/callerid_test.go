package app

import (
	"os"
	"testing"

	"github.com/usestring/gate-inbox/internal/hooks"
)

// A claude the board adopted has no GATE_INBOX_SESSION_ID, so its shell
// commands find their row through the pane's adoption marker; a launched
// session's environment still wins.
func TestCallerIDFallsBackToTheAdoptionMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(hooks.EnvSessionID, "")
	t.Setenv("TMUX", "/run/tmux-test/default,4242,0")
	t.Setenv("TMUX_PANE", "%7")
	if got := callerID(dir); got != "" {
		t.Fatalf("callerID with no marker = %q, want none", got)
	}
	if err := hooks.NewManager(dir).SyncAdopted([]hooks.AdoptedPane{
		{ID: "a1b2c3d4", ServerPID: 4242, PaneID: "%7", AgentPID: os.Getppid()},
	}); err != nil {
		t.Fatal(err)
	}
	if got := callerID(dir); got != "a1b2c3d4" {
		t.Fatalf("callerID in an adopted pane = %q, want a1b2c3d4", got)
	}
	t.Setenv(hooks.EnvSessionID, "launched")
	if got := callerID(dir); got != "launched" {
		t.Fatalf("callerID with a launch's id = %q, want launched", got)
	}
	t.Setenv(hooks.EnvSessionID, "")
	t.Setenv("TMUX_PANE", "")
	if got := callerID(dir); got != "" {
		t.Fatalf("callerID outside tmux = %q, want none", got)
	}
}
