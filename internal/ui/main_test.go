// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/usestring/gate-inbox/internal/termseq"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// testSocket is an isolated tmux server for this package's tests, so they
// never touch the default socket where the user's shell tmux and live agents
// live. TestMain tears it down before and after the run, so the name must be
// unique per process: a fixed one let concurrent checkouts kill-server each
// other's tests, which reads as an unrelated flake.
var testSocket = newTestSocket()

// TestMain kills any leftover test server so each run starts and ends clean.
// The anchor session then holds the server up for the whole run: tests kill
// their sessions in cleanup, and a server whose last session dies begins an
// exit-empty shutdown that takes the next test's fresh session down with it
// ("server exited unexpectedly", the recurring CI failure in
// TestFocusWatchReportsCursor).
func TestMain(m *testing.M) {
	// A pane runs the launch script under $SHELL, and tmux answers
	// #{pane_current_path} from that process's own working directory. An
	// interactive shell chdirs while it sources its startup files -- an
	// oh-my-zsh zsh passes through ~/.oh-my-zsh -- so any test that reads a
	// pane's directory before the shell settles reads somewhere it never
	// asked for. A login /bin/sh sources nothing and never moves, which is
	// what makes the terminal and editor tests here deterministic.
	os.Setenv("SHELL", "/bin/sh")
	// OLED, the default theme, repaints the terminal's background. Tests that
	// read those sequences point Out at their own buffer; the rest must not
	// turn the terminal running go test black.
	termseq.Out = io.Discard
	// tmuxtest.Guard clears the inherited TMUX/TMUX_PANE, sweeps strays from
	// earlier runs, and takes this run's server and the clients that outlive
	// it down afterwards. Shared with every other package here that drives
	// tmux: this one used to be the only one that swept, and the rest went on
	// leaking.
	os.Exit(tmuxtest.Guard(testSocket, func() int {
		// Without tmux the run still starts: each test skips through its
		// own requireTmux. With tmux, a run that could not plant the
		// anchor would pass or flake on luck, so it stops instead.
		if _, err := exec.LookPath("tmux"); err == nil {
			if out, err := tmuxCmd("new-session", "-d", "-s", "anchor").CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "anchor session: %v: %s\n", err, out)
				os.Exit(1)
			}
		}
		// Parsed here rather than by m.Run so shouldShard can read what the
		// run selected.
		flag.Parse()
		if shouldShard() {
			return runSharded(m)
		}
		return m.Run()
	}))
}

// tmuxCmd builds a raw tmux command aimed at the test socket, matching the
// socket buildModel's driver runs on.
func tmuxCmd(args ...string) *exec.Cmd {
	return tmuxOnSocket(testSocket, args...)
}

// tmuxOnSocket is every raw tmux command this package's tests run. They kill
// the server they run on, so one that resolved to tmux's default would end
// whatever the operator has running there.
func tmuxOnSocket(socket string, args ...string) *exec.Cmd {
	if !tmux.OwnsSocket(socket) {
		panic("test tmux command aimed at tmux's default server: " + socket)
	}
	return exec.Command("tmux", append([]string{"-L", socket}, args...)...)
}

// newTestDriver is how this package's tests get a tmux driver. Every capture
// opens a pooled control-mode client -- a process -- and the driver holds it
// until CloseCaptureClients takes it down, so a driver built without this
// cleanup leaks one client per test that captured anything.
//
// It is a constructor rather than a rule because a rule gets forgotten: the
// leak it replaces was a t.Cleanup that killed the sessions and left the
// clients, repeated at each call site.
func newTestDriver(t testing.TB, socket string) *tmux.Driver {
	t.Helper()
	driver, err := tmux.NewWithSocket(socket)
	if err != nil {
		t.Fatalf("NewWithSocket: %v", err)
	}
	t.Cleanup(driver.CloseCaptureClients)
	return driver
}
