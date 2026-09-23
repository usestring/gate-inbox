package sessioncmd

import (
	"os"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain pins the shell every test pane is launched under. tmux answers
// #{pane_current_path} from the pane process's own working directory, and an
// interactive shell chdirs while it sources its startup files -- an oh-my-zsh
// zsh passes through ~/.oh-my-zsh -- so a terminal resolved against a pane
// that is still starting inherits a directory nobody asked for. A login
// /bin/sh sources nothing and never moves.
//
// Tests built on a harness call t.Parallel: each harness owns its tmux server,
// store and scratch directory, and run serially this package spent 212s
// waiting on tmux round trips. A test stays serial when it sets the
// environment, swaps the global tracer, asserts on elapsed time, or gives a
// pane a fixed session ID -- the launch script is $TMPDIR/am-launch-<id>.sh,
// so two concurrent tests sharing an ID delete each other's.
func TestMain(m *testing.M) {
	os.Setenv("SHELL", "/bin/sh")
	// Clears the inherited TMUX/TMUX_PANE, which would otherwise point
	// Resize, PrepareAttach and the visibility read at the operator's own
	// pane on tmux's default server: the one server no test here may touch.
	// TestNoInheritedTmuxEnvironment keeps that half honest.
	tmuxtest.ClearInheritedTmuxEnv()
	// Each harness here builds its own socket and tears it down itself, so
	// there is no package server to guard -- but a run that panicked or timed
	// out left control clients behind on sockets whose servers are gone, and
	// kill-server cannot collect those. Sweeping at the start is what heals a
	// box that already accumulated them.
	tmuxtest.ReapStrays()
	os.Exit(m.Run())
}

// serverAlreadyGone reports the two ways kill-server can find nothing left to
// kill. Each harness kills its sessions first, and a tmux server whose last
// session dies starts an exit-empty shutdown of its own; the kill-server that
// follows then loses a race it does not need to win, since a server that has
// already gone is the outcome the cleanup wanted.
func serverAlreadyGone(out string) bool {
	return strings.Contains(out, "no server running") ||
		strings.Contains(out, "server exited unexpectedly")
}
