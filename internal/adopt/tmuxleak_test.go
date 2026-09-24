package adopt

import (
	"os"
	"testing"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestMain runs the package under tmuxtest.Run: the TMUX/TMUX_PANE this
// process inherited are cleared and TMUX_TMPDIR is private, so a scan cannot
// reach the operator's own server, and control clients earlier runs left on
// dead servers are swept, which is what kill-server cannot collect.
func TestMain(m *testing.M) {
	os.Exit(tmuxtest.Run(m.Run))
}

// TestAPaneServerRunsOnASocketTheReaperOwns is this package's share of the
// same guard, aimed at what actually went wrong here. These fixtures drive
// tmux with one-shot commands rather than a control client, and a server whose
// last pane dies shuts itself down, so little leaks in the happy path -- but
// the socket was named "adopt-test-<pid>-<test>", outside every prefix the
// sweep matches, so anything a panicking or timing-out run did leave behind
// was invisible to it and could only be collected by a human.
func TestAPaneServerRunsOnASocketTheReaperOwns(t *testing.T) {
	socket := paneServer(t, t.TempDir(), "cat")
	if !tmuxtest.Owns(socket) {
		t.Fatalf("the fixture server runs on %q, which the reaper does not own: nothing this package leaves behind would ever be swept", socket)
	}
	if panes := Panes(socket); len(panes) == 0 {
		t.Fatalf("the fixture server on %s has no panes, so this proves nothing about a socket in use", socket)
	}
}
