package sessioncmd

import (
	"os"
	"testing"
)

// TestMain clears TMUX_PANE and TMUX for this package. This is what stops
// that from being quietly undone.
//
// Both are inherited from whatever real tmux a developer runs the suite in.
// Production code reads them to find the pane the manager is drawing in, so
// with them set a test resolves the operator's own pane on tmux's default
// server -- the server this suite must never touch, and the one whose
// destruction on 2026-08-27 took every running agent with it. It reached a
// release build once already, through exactly this route: a visibility read
// that looked up its own pane and found the developer's.
//
// The comment in TestMain explaining the clearing is not a guarantee; a
// comment cannot fail. This can.
func TestNoInheritedTmuxEnvironment(t *testing.T) {
	for _, name := range []string{"TMUX_PANE", "TMUX"} {
		if value, set := os.LookupEnv(name); set && value != "" {
			t.Fatalf("%s is set to %q for this package's tests: production code reads it to find the manager's own pane, so a test can reach the operator's real tmux server through it. TestMain must clear it.", name, value)
		}
	}
}
