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
	// tmuxtest.Run keeps the inherited TMUX/TMUX_PANE -- which would
	// otherwise point Resize, PrepareAttach and the visibility read at the
	// operator's own pane -- out of the run, gives it a private TMUX_TMPDIR,
	// sweeps control clients earlier runs left on dead servers, and tears
	// down whatever this run started. TestNoInheritedTmuxEnvironment keeps
	// the environment half honest.
	os.Exit(tmuxtest.Run(m.Run))
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
