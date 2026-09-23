package ui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// This file is the outage of 2026-08-27 written down as a test.
//
// The manager mirrored a focused session with a tmux control client, and for
// an adopted pane that client attached to the session holding the pane --
// which is the operator's own. Every cursor move forked another one into
// their server and the close never landed: 584 opens, zero closes, and at the
// peak 53 attaches in a minute. The server ran out and died, taking every
// live agent session with it.
//
// The walk below is that morning at speed. What it asserts is not "fewer
// clients": it is that the manager holds none at all on a server it does not
// own, and that nothing it did leaves a process behind.

// walkPanes is how many adopted rows the walk crosses. The board that died
// had about thirty sessions; a dozen is enough to make an unbounded leak
// unmistakable while keeping the test's own tmux server small.
const walkPanes = 12

// controlClientSessions names the session behind every control-mode client on
// a tmux server: tmux's own view of what this manager has joined.
//
// The session is the whole point of reading it. One control client on a
// foreign server is correct and expected -- the pooled capture client, which
// attaches to the "gi_poll-anchor" session the manager creates for the
// purpose. A client sitting in any other session is the outage.
func controlClientSessions(t testing.TB, socket string) []string {
	t.Helper()
	// tmuxOnSocket, not a raw exec: it panics on tmux's default server, which
	// is where the operator's live work is and the one place these tests must
	// never point.
	out, err := tmuxOnSocket(socket, "list-clients",
		"-F", "#{client_control_mode} #{client_session}").CombinedOutput()
	if err != nil {
		// No server, or no clients: either way there is nothing attached.
		return nil
	}
	var sessions []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		mode, session, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || mode != "1" {
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions
}

// foreignSessions is every control-client session on a server that this
// manager did not create. It is the invariant the outage cost, in one line.
func foreignSessions(t testing.TB, socket string) []string {
	t.Helper()
	var foreign []string
	for _, session := range controlClientSessions(t, socket) {
		if !strings.HasPrefix(session, "gi_") {
			foreign = append(foreign, session)
		}
	}
	return foreign
}

// tmuxProcessesOn counts live tmux processes aimed at a socket, which is the
// only way to see the clients that outlived their server -- the exact shape
// of the leak, and invisible to list-clients and to kill-server alike.
//
// It counts and never signals. The kill path is reapStrayTestClients, which
// refuses any socket outside this package's own prefix.
func tmuxProcessesOn(socket string) int {
	out, err := exec.Command("ps", "-axo", "pid=,args=").Output()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		argv := strings.Fields(line)
		if len(argv) < 2 || filepath.Base(argv[1]) != "tmux" {
			continue
		}
		for i := 2; i+1 < len(argv); i++ {
			if argv[i] == "-L" {
				if argv[i+1] == socket {
					count++
				}
				break
			}
		}
	}
	return count
}

// awaitProcessesOn waits for the tmux process count on a socket to fall to
// want. Close returns once the process is gone, but the kernel holds the
// zombie until Wait collects it.
func awaitProcessesOn(socket string, want int, within time.Duration) int {
	deadline := time.Now().Add(within)
	for {
		got := tmuxProcessesOn(socket)
		if got <= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// adoptedBoard builds a model whose rows are all adopted panes on one foreign
// server, the board the outage happened on.
func adoptedBoard(t testing.TB, panes int) (*Model, string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	m := buildModel(t)
	socket := tmuxtest.NewSocket("uiforeign")
	tmuxOnSocket(socket, "kill-server").Run()
	if out, err := tmuxOnSocket(socket, "new-session", "-d", "-s", "user",
		"-c", "/tmp", "-x", "80", "-y", "24", "cat").CombinedOutput(); err != nil {
		t.Fatalf("foreign new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { tmuxOnSocket(socket, "kill-server").Run() })
	for i := 1; i < panes; i++ {
		if out, err := tmuxOnSocket(socket, "new-window", "-t", "user", "-c", "/tmp", "cat").CombinedOutput(); err != nil {
			t.Fatalf("foreign new-window: %v: %s", err, out)
		}
	}
	out, err := tmuxOnSocket(socket, "list-panes", "-s", "-t", "user", "-F", "#{pane_id}").CombinedOutput()
	if err != nil {
		t.Fatalf("foreign list-panes: %v: %s", err, out)
	}
	ids := strings.Fields(string(out))
	if len(ids) != panes {
		t.Fatalf("foreign server has %d panes, want %d", len(ids), panes)
	}
	for i, pane := range ids {
		id := fmt.Sprintf("walk%02d", i)
		if err := m.store.CreateSession(store.Session{
			ID: id, Name: id, Tool: "claude", Cwd: t.TempDir(),
			Status: status.Idle, CreatedAt: time.Now(), LastStatusAt: time.Now(),
			TmuxSocket: socket, TmuxPaneID: pane,
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if err := m.tmux.Adopt(id, tmux.Target{Socket: socket, Name: pane}); err != nil {
			t.Fatalf("Adopt: %v", err)
		}
	}
	m.applyCmd(t, nil)
	return m, socket
}

// walkCursor drives one arrow key through the real Update path, the way a
// cursor walk actually reaches the read paths below.
func (m *Model) walkCursor(code rune) {
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: code})
	*m = *updated.(*Model)
}

// readSelected runs the manager's real reads against the row the cursor
// landed on: the tick capture, the pane facts beside it, and the keystroke
// echo chase, which is a few hundred captures of its own inside its budget.
//
// These are the paths that reach for a control client, and they are what the
// walk has to drive. The deleted focusWatch opened one per cursor stop; the
// reads that replaced it take the pooled per-server client instead, and a
// walk that only moved the cursor without reading anything would prove
// nothing about either.
func (m *Model) readSelected(t testing.TB) {
	t.Helper()
	sess, ok := m.selected()
	if !ok {
		t.Fatal("the cursor did not land on a session row")
	}
	for _, cmd := range []tea.Cmd{
		m.previewCmd(sess, m.previewGen, true),
		m.paneStateCmd(sess.ID),
		m.focusEchoCmd(sess.ID, m.previewGen, m.preview, m.echoBudget(sess.Tool)),
	} {
		if cmd == nil {
			t.Fatal("a focused read produced no command to run")
		}
		cmd()
	}
}

// The regression test. Before the fix this walk left one control client per
// cursor stop attached to a server the manager does not own; after it, the
// manager attaches nothing there but its own anchor, and no process survives.
func TestCursorWalkOverAdoptedRowsAttachesNothing(t *testing.T) {
	m, socket := adoptedBoard(t, walkPanes)
	baseline := tmuxProcessesOn(socket)

	peakClients, peakProcs := 0, baseline
	var joined []string
	step := func() {
		// Every read the manager makes of the row it settled on, which on
		// the day was about once a second as somebody held the key down.
		m.readSelected(t)
		joined = append(joined, foreignSessions(t, socket)...)
		if got := len(controlClientSessions(t, socket)); got > peakClients {
			peakClients = got
		}
		if got := tmuxProcessesOn(socket); got > peakProcs {
			peakProcs = got
		}
	}
	rows := m.sessionRows()
	if len(rows) != walkPanes {
		t.Fatalf("board has %d session rows, want %d", len(rows), walkPanes)
	}
	// Down the board and back up, twice: the shape that turned a leak into
	// an outage was repetition, not depth.
	for pass := 0; pass < 2; pass++ {
		for i := 0; i < len(rows)-1; i++ {
			m.walkCursor(tea.KeyDown)
			step()
		}
		for i := 0; i < len(rows)-1; i++ {
			m.walkCursor(tea.KeyUp)
			step()
		}
	}

	if len(joined) > 0 {
		t.Fatalf("the manager became a client of %d session(s) it did not create: %q; "+
			"this is the mechanism that killed the operator's tmux server on 2026-08-27", len(joined), joined)
	}
	// The one client that is allowed is the pooled capture client on the
	// manager's own anchor session, and there is one of it per server no
	// matter how many rows the walk crossed or how many reads each made.
	if peakClients > 1 {
		t.Fatalf("the walk held %d control clients on a foreign server; only the pooled capture client belongs there", peakClients)
	}
	// A client per cursor stop would show here even if tmux had already
	// forgotten it: these processes outlive the server they attached to,
	// which is why the leak was invisible to list-clients and kill-server.
	if peakProcs > baseline+1 {
		t.Fatalf("the walk started %d tmux processes on a foreign socket (baseline %d)", peakProcs-baseline, baseline)
	}
	m.tmux.CloseCaptureClients()
	if got := awaitProcessesOn(socket, baseline, 5*time.Second); got > baseline {
		t.Fatalf("%d tmux processes outlived the walk on %s, baseline %d", got, socket, baseline)
	}
	if got := controlClientSessions(t, socket); len(got) != 0 {
		t.Fatalf("control clients still attached to the foreign server: %q", got)
	}
	t.Logf("walk of %d cursor stops over adopted rows: sessions joined that the manager does not own = %d, "+
		"peak control clients on the foreign server = %d (baseline processes %d, peak %d)",
		4*(walkPanes-1), len(joined), peakClients, baseline, peakProcs)
}

// The same walk over the manager's own sessions, where a client attached to
// a session the manager created would once have been allowed -- up to
// tmux.MaxControlClientsPerSocket of them, reused across rows.
//
// It is not allowed any more, and this is where that is asserted. The mirror
// is gone and the reads that replaced it share one pooled client per server,
// so the ceiling here is not the cap: it is one. Holding the walk to the cap
// would let a per-row client come back on the manager's own board, which is
// half of what 2026-08-27 cost and the half that no adopted-pane guard would
// catch.
func TestCursorWalkOverManagedRowsHoldsOnePooledClient(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	m := buildModel(t)
	const rows = 6
	for i := 0; i < rows; i++ {
		name := "managed" + strconv.Itoa(i)
		if err := m.tmux.Create(name, t.TempDir(), "", nil, 80, 24); err != nil {
			t.Fatalf("Create: %v", err)
		}
		id := name
		t.Cleanup(func() { m.tmux.Kill(id) })
		if err := m.store.CreateSession(store.Session{
			ID: id, Name: name, Tool: "claude", Cwd: t.TempDir(),
			Status: status.Idle, CreatedAt: time.Now(), LastStatusAt: time.Now(),
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}
	m.applyCmd(t, nil)

	peak, peakForeign := 0, 0
	step := func() {
		m.readSelected(t)
		if got := m.tmux.ControlClientsOn(testSocket); got > peak {
			peak = got
		}
		// tmux's own account of it, which is what the outage was measured
		// in: the driver's counter cannot see a client it has forgotten.
		if got := len(foreignSessions(t, testSocket)); got > peakForeign {
			peakForeign = got
		}
	}
	for pass := 0; pass < 2; pass++ {
		for i := 0; i < rows-1; i++ {
			m.walkCursor(tea.KeyDown)
			step()
		}
		for i := 0; i < rows-1; i++ {
			m.walkCursor(tea.KeyUp)
			step()
		}
	}
	if peak > 1 {
		t.Fatalf("the manager held %d control clients on its own server; the pooled capture client is the only one there should ever be (the old cap was %d)",
			peak, tmux.MaxControlClientsPerSocket)
	}
	if peakForeign > 0 {
		t.Fatalf("the walk attached to %d session(s) the manager did not create on its own server", peakForeign)
	}
	m.tmux.CloseCaptureClients()
	if got := m.tmux.ControlClientsOn(testSocket); got != 0 {
		t.Fatalf("%d control clients survived shutdown", got)
	}
	if got := controlClientSessions(t, testSocket); len(got) != 0 {
		t.Fatalf("control clients still attached after shutdown: %q", got)
	}
	t.Logf("walk of %d cursor stops over managed rows: peak control clients = %d",
		4*(rows-1), peak)
}
