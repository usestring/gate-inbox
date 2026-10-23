package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// unknownAgentTool is a claude whose process does not carry the configured
// command's name -- the fixture panes run cat -- and which draws nothing a
// prompt marker would match. Identification alone never takes such a pane.
func unknownAgentTool() []adopt.Tool {
	return []adopt.Tool{{Name: "claude", Command: "claude"}}
}

// fixtureServerPID is the pid of the fixture server, the second field of
// the $TMUX a hook in one of its panes sees.
func fixtureServerPID(t *testing.T, socket string) int {
	t.Helper()
	out, err := tmuxOnSocket(socket, "display-message", "-p", "#{pid}").Output()
	if err != nil {
		t.Fatalf("display-message: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("server pid %q: %v", out, err)
	}
	return pid
}

// fixtureArrival is the announcement the SessionStart hook would write from
// pane, parsed the way the board reads it, so the socket name is the one
// $TMUX's path really yields.
func fixtureArrival(t *testing.T, socket string, pane adopt.Candidate, agentPID int) hooks.Arrival {
	t.Helper()
	raw := tmuxtest.SocketPath(socket) + "," + strconv.Itoa(fixtureServerPID(t, socket)) + ",0\n" +
		pane.PaneID + "\n" + strconv.Itoa(agentPID) + "\n"
	arrival, ok := hooks.ParseArrival(raw)
	if !ok {
		t.Fatalf("the announcement %q does not parse", raw)
	}
	return arrival
}

func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// An agent that announced itself is taken even where identification has
// nothing to go on, and only while it is still the pane's own process.
func TestAnArrivalTakesAPaneIdentificationCannot(t *testing.T) {
	dir := t.TempDir()
	socket := windowFixture(t, "main", 1, dir)
	st := newFixtureStore(t)
	panes := adopt.Panes(socket)
	if len(panes) != 1 {
		t.Fatalf("fixture holds %d panes, want 1", len(panes))
	}
	pane := panes[0]

	run := newFixtureRun(t, st, socket)
	run.tools = unknownAgentTool()
	if taken, err := run.take(panes, adopt.NewProcTable()); err != nil || taken != 0 {
		t.Fatalf("the full scan took %d (%v) of a pane it cannot identify; rejections %s", taken, err, rejectionSummary(run.rejected))
	}

	for name, arrival := range map[string]hooks.Arrival{
		"an agent that has exited": fixtureArrival(t, socket, pane, exitedPID(t)),
	} {
		run := newFixtureRun(t, st, socket)
		run.tools = unknownAgentTool()
		candidates, claims := locateArrivals([]hooks.Arrival{arrival}, "claude")
		run.arrivals = claims
		if taken, _ := run.take(candidates, adopt.NewProcTable()); taken != 0 {
			t.Fatalf("%s: took the pane on an announcement its process tree does not back", name)
		}
	}
	stale := fixtureArrival(t, socket, pane, int(pane.PID))
	stale.ServerPID++
	if candidates, _ := locateArrivals([]hooks.Arrival{stale}, "claude"); len(candidates) != 0 {
		t.Fatalf("a pane announced on another server's life was located: %+v", candidates)
	}

	run = newFixtureRun(t, st, socket)
	run.tools = unknownAgentTool()
	candidates, claims := locateArrivals([]hooks.Arrival{fixtureArrival(t, socket, pane, int(pane.PID))}, "claude")
	if len(candidates) != 1 || candidates[0].PaneID != pane.PaneID || candidates[0].Socket != socket {
		t.Fatalf("located %+v, want the announced pane %s on %s", candidates, pane.PaneID, socket)
	}
	run.arrivals = claims
	taken, err := run.take(candidates, adopt.NewProcTable())
	if err != nil || taken != 1 {
		t.Fatalf("took %d (%v) of an announced pane; rejections %s", taken, err, rejectionSummary(run.rejected))
	}
	rows, err := st.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Tool != "claude" || rows[0].TmuxPaneID != pane.PaneID {
		t.Fatalf("board holds %+v, want one claude row on %s", rows, pane.PaneID)
	}
}

// The board picks an announcement up on its next poll, scans only that
// pane, and runs one scan at a time: an announcement that lands while
// another scan is out waits for it rather than racing it for the pane.
func TestTheBoardScansAnAnnouncedPaneOnItsNextPoll(t *testing.T) {
	m := buildModel(t)
	m.cfg.Tools = map[string]config.Tool{
		"claude": {Command: "claude", StatusSource: hooks.StatusSourceClaude, DefaultStatus: status.Idle},
	}
	if m.arrivalStart() == nil {
		t.Fatal("the board did not start watching for arrivals")
	}
	dir := t.TempDir()
	socket := windowFixture(t, "main", 2, dir)
	panes := adopt.Panes(socket)
	announced := panes[1]
	arrival := fixtureArrival(t, socket, announced, int(announced.PID))
	content := tmuxtest.SocketPath(socket) + "," + strconv.Itoa(arrival.ServerPID) + ",0\n" +
		announced.PaneID + "\n" + strconv.Itoa(arrival.AgentPID) + "\n"
	file := filepath.Join(m.hooks.ArrivalsDir(), strconv.Itoa(arrival.ServerPID)+announced.PaneID)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m.adoptBusy = true
	if cmd := m.scanArrivals(time.Now()); cmd != nil {
		t.Fatal("scanned an arrival while another scan was out")
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("the announcement was left to be read twice")
	}
	if len(m.arrivals.queued) != 1 {
		t.Fatalf("%d announcements held, want the one that arrived", len(m.arrivals.queued))
	}

	m.adoptBusy = false
	cmd := m.scanArrivals(time.Now())
	if cmd == nil || !m.adoptBusy {
		t.Fatal("the held announcement was not scanned once the board was free")
	}
	msg, ok := cmd().(adoptedMsg)
	if !ok || msg.err != nil || msg.taken != 1 {
		t.Fatalf("the arrival scan answered %+v", msg)
	}
	updated, _ := m.Update(msg)
	m = updated.(*Model)
	if m.adoptBusy {
		t.Fatal("the board still thinks a scan is out")
	}
	rows, err := m.store.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].TmuxPaneID != announced.PaneID {
		t.Fatalf("board holds %+v, want only the announced pane %s, not its neighbour", rows, announced.PaneID)
	}

	// The same pane announcing again -- a /clear, a nested claude -- before
	// the board has read its new row back is not taken twice.
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if cmd := m.scanArrivals(time.Now()); cmd != nil {
		if msg := cmd().(adoptedMsg); msg.taken != 0 {
			t.Fatalf("took an announced pane a second time: %+v", msg)
		}
	}
}

// With outside panes set to be ignored, an announcement is read and
// dropped without a scan.
func TestArrivalsAreDroppedWhenOutsidePanesAreIgnored(t *testing.T) {
	m := buildModel(t)
	if err := m.store.SetSetting(outsidePanesSetting, paneIgnore); err != nil {
		t.Fatal(err)
	}
	if m.arrivalStart() == nil {
		t.Fatal("the board did not start watching for arrivals")
	}
	file := filepath.Join(m.hooks.ArrivalsDir(), "1%1")
	if err := os.WriteFile(file, []byte("/tmp/x/default,1,0\n%1\n2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cmd := m.scanArrivals(time.Now()); cmd != nil || m.adoptBusy {
		t.Fatal("scanned an announcement with outside panes ignored")
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("the announcement was left behind")
	}
}
