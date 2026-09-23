package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/adopt"
)

// The four panes below are the four panes one scan of the operator's live
// board refused as "not confident", and they are why the rule changed. Three
// of them ran claude and were dropped for having no prompt marker on screen.
// The fourth ran zsh and matched on the marker alone, from agent-shaped text
// left in its scrollback -- the case that says why the marker cannot be
// promoted to sufficient in the other's place.
//
// The shell is what keeps this honest. Without it, "the process decides"
// could be loosened to "anything decides" and every other case here would
// still pass.
//
// Every pane runs on a private -L amuitest- socket. Nothing in this file may
// reach tmux's default server: that is where the agents this tool exists to
// show are actually running, and this tool has taken that server down before.

// signalCase is one pane of the fixture: what to run in it, and what
// adoption owes it.
type signalCase struct {
	name string
	// run is the pane's command. The tool being looked for is "cat", so a
	// case that runs cat is a pane whose process IS the agent.
	run string
	// draws is text the pane must have on screen before the scan reads it,
	// so a pane that is merely slow is never mistaken for one with nothing
	// to show.
	draws string
	// settles is what the pane's process tree must name before the scan
	// reads it, for the cases that have no output to wait on.
	settles string
	adopted bool
	why     string
}

func signalCases() []signalCase {
	return []signalCase{{
		name:    "agent-mid-turn",
		run:     "exec cat",
		settles: "cat",
		adopted: true,
		why:     "its process is the agent; an agent mid-turn draws no prompt and is the one worth surfacing",
	}, {
		name:    "agent-at-prompt",
		run:     `printf '\n❯ '; exec cat`,
		draws:   "❯",
		adopted: true,
		why:     "its process is the agent",
	}, {
		name:    "shell-with-marker-in-scrollback",
		run:     `printf '\n❯ read the transcript above\n'; exec sh`,
		draws:   "❯",
		adopted: false,
		why:     "it is a shell: the marker is text somebody left on the screen, and a sentence typed here is a command",
	}, {
		name:    "neither",
		run:     "exec sleep 600",
		settles: "sleep",
		adopted: false,
		why:     "nothing identifies it",
	}}
}

// TestTheProcessDecidesWhichPanesAreAdopted runs all four cases through the
// scan itself rather than through Identify, so what is asserted is rows on a
// board.
func TestTheProcessDecidesWhichPanesAreAdopted(t *testing.T) {
	cases := signalCases()
	socket, dirs := signalFixture(t, cases)

	st := newFixtureStore(t)
	run := newFixtureRun(t, st, socket)
	if _, err := run.take(adopt.Panes(socket), adopt.NewProcTable()); err != nil {
		t.Fatalf("take: %v", err)
	}

	rows, err := st.ListSessions(false)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	// The cwd is what ties a row back to the case that made it: each pane
	// runs in a directory of its own.
	onBoard := map[string]bool{}
	for _, sess := range rows {
		onBoard[sess.Cwd] = true
	}
	for _, c := range cases {
		got := onBoard[dirs[c.name]]
		if got == c.adopted {
			continue
		}
		verb := "was not adopted"
		if got {
			verb = "was adopted"
		}
		t.Errorf("%s %s; it should have been %v because %s. rejections were %s",
			c.name, verb, c.adopted, c.why, rejectionSummary(run.rejected))
	}
	if len(rows) != countAdopted(cases) {
		names := make([]string, 0, len(rows))
		for _, sess := range rows {
			names = append(names, filepath.Base(sess.Cwd))
		}
		sort.Strings(names)
		t.Errorf("board holds %d rows (%s), want %d", len(rows), strings.Join(names, " "), countAdopted(cases))
	}
}

func countAdopted(cases []signalCase) int {
	n := 0
	for _, c := range cases {
		if c.adopted {
			n++
		}
	}
	return n
}

// signalFixture builds one private tmux server with a window per case, each
// in a directory of its own, and returns the socket and those directories.
func signalFixture(t *testing.T, cases []signalCase) (string, map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := newTestSocket()
	t.Cleanup(func() { killFixtureServer(t, socket) })

	dirs := map[string]string{}
	for i, c := range cases {
		// A directory per case, named after it, so a row can be traced to
		// the pane that produced it and so the adopted rows get distinct
		// names rather than being numbered against each other.
		dir := filepath.Join(t.TempDir(), c.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		// Keyed by the path tmux reports back, not the one handed to -c: on
		// macOS t.TempDir() sits under /var, a symlink to /private/var, and
		// #{pane_current_path} answers the resolved form. Keying by the
		// unresolved spelling matches no pane at all, so the fixture waits
		// out its whole deadline and every case then reads as not adopted.
		dirs[c.name] = resolved(t, dir)
		args := []string{"new-window", "-t", "main", "-c", dir, c.run}
		if i == 0 {
			args = []string{"new-session", "-d", "-s", "main", "-c", dir, c.run}
		}
		if out, err := tmuxOnSocket(socket, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", c.name, err, out)
		}
	}
	waitForSignalPanes(t, socket, cases, dirs)
	return socket, dirs
}

// waitForSignalPanes blocks until every pane is in the state its case
// describes. Without it a pane read before its command has run carries
// neither signal, and the refusal that follows is a race rather than the
// rule under test.
func waitForSignalPanes(t *testing.T, socket string, cases []signalCase, dirs map[string]string) {
	t.Helper()
	byDir := map[string]signalCase{}
	for _, c := range cases {
		byDir[dirs[c.name]] = c
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		procs := adopt.NewProcTable()
		ready, pending := 0, []string{}
		for _, candidate := range adopt.Panes(socket) {
			c, ok := byDir[candidate.Cwd]
			if !ok {
				continue
			}
			if c.draws != "" {
				text, err := adopt.Capture(candidate.Socket, candidate.PaneID)
				if err != nil || !strings.Contains(text, c.draws) {
					pending = append(pending, c.name+" (no "+c.draws+")")
					continue
				}
			}
			if c.settles != "" && !treeNames(procs, candidate, c.settles) {
				pending = append(pending, c.name+" (no "+c.settles+" process)")
				continue
			}
			ready++
		}
		if ready == len(cases) {
			return
		}
		if time.Now().After(deadline) {
			sort.Strings(pending)
			t.Fatalf("%d of %d fixture panes never settled: %s", ready, len(cases), strings.Join(pending, ", "))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func treeNames(procs *adopt.ProcTable, candidate adopt.Candidate, program string) bool {
	if candidate.Command == program {
		return true
	}
	for _, line := range procs.Cmdlines(candidate.PID) {
		for _, field := range strings.Fields(line) {
			if filepath.Base(field) == program {
				return true
			}
		}
	}
	return false
}
