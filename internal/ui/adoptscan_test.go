package ui

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// TestAdoptableRecoversOrphansAndLeavesRowsAlone is the pair of answers the
// scan has to get right now that it shares a server with the sessions it
// scans: a session a row still accounts for is already ours, and one no row
// accounts for is an agent still running with nothing pointing at it.
func TestAdoptableRecoversOrphansAndLeavesRowsAlone(t *testing.T) {
	managed := adopt.Candidate{Socket: tmux.DefaultSocket, PaneID: "%1", Session: tmux.SessionName("abc")}
	onBoard := map[string]bool{tmux.SessionName("abc"): true}
	if adoptableOK(managed, map[string]bool{}, onBoard) {
		t.Errorf("adopted %s a second time; a row already points at it", managed.Session)
	}
	if !adoptableOK(managed, map[string]bool{}, map[string]bool{}) {
		t.Errorf("left %s orphaned; no row points at it and nothing else will find it", managed.Session)
	}
	foreign := adopt.Candidate{Socket: tmux.DefaultSocket, PaneID: "%2", Session: "work"}
	if !adoptableOK(foreign, map[string]bool{}, onBoard) {
		t.Error("refused a pane the manager did not start")
	}
	if adoptableOK(foreign, map[string]bool{adoptKey(foreign.Socket, foreign.PaneID): true}, onBoard) {
		t.Error("adopted a pane already on the board")
	}
}

// TestOrphanedSessionIsRecoveredFromARealServer runs the same decision over
// real panes and a real store, so it holds against the names tmux reports and
// the rows the store keeps rather than the ones a test made up. The orphan is
// the case the whole change exists for: a store that moved or was lost leaves
// its agents running, and the scan is what brings them back.
func TestOrphanedSessionIsRecoveredFromARealServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	driver := newTestDriver(t, testSocket)
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	const tracked, orphan = "trackedsess", "orphansess"
	for _, id := range []string{tracked, orphan} {
		if err := driver.Create(id, t.TempDir(), "cat", nil, 80, 24); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
		t.Cleanup(func() { driver.Kill(id) })
	}
	// Only one of them is in the store: the other is what a store that moved
	// out from under a running agent leaves behind.
	row := store.Session{ID: tracked, Name: tracked, Tool: "claude", Cwd: t.TempDir(), CreatedAt: time.Now(), LastStatusAt: time.Now()}
	if err := st.CreateSession(row); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rows, err := st.ListSessions(true)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	onBoard := onBoardSessions(rows)

	seen := map[string]bool{}
	for _, candidate := range adopt.Panes(testSocket) {
		switch candidate.Session {
		case tmux.SessionName(tracked):
			seen[tracked] = true
			if adoptableOK(candidate, map[string]bool{}, onBoard) {
				t.Errorf("adopted %s again; the store still has its row", candidate.Session)
			}
		case tmux.SessionName(orphan):
			seen[orphan] = true
			if !adoptableOK(candidate, map[string]bool{}, onBoard) {
				t.Errorf("left %s orphaned; no row points at it", candidate.Session)
			}
		}
	}
	if !seen[tracked] || !seen[orphan] {
		t.Fatalf("scan of %s did not turn up both sessions: %v", testSocket, seen)
	}
}

// TestAdoptSocketsKeepsTheDefaultServer covers the scan losing its whole
// reason to exist: the default server is where a session somebody started by
// hand lives, and it is now the manager's own server too.
func TestAdoptSocketsKeepsTheDefaultServer(t *testing.T) {
	driver, err := tmux.NewWithSocket(tmux.DefaultSocket)
	if err != nil {
		t.Fatalf("NewWithSocket: %v", err)
	}
	m := &Model{tmux: driver, cfg: config.Config{AdoptSockets: []string{tmux.DefaultSocket, "elsewhere", ""}}}
	sockets := m.adoptSockets()
	var defaults int
	for _, socket := range sockets {
		if socket == tmux.DefaultSocket {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("scanned the default server %d times, want exactly once: %q", defaults, sockets)
	}
	if len(sockets) != 2 || sockets[1] != "elsewhere" {
		t.Errorf("adoptSockets() = %q, want the default server and elsewhere", sockets)
	}
}

// TestTheFirstAdoptScanDoesNotWaitOutTheInterval pins the one thing that made a
// fresh board look broken. Startup used to schedule its first scan through the
// same 45s timer as every later one, so the rail sat empty for the whole
// interval -- measured at 45.6s on a real board -- before a single agent
// appeared. The scan itself takes a fraction of a second.
//
// Hermetic on purpose: it asks what startup hands back, not what a scan of this
// machine happens to find, so it cannot pass or fail on which panes are open.
func TestTheFirstAdoptScanDoesNotWaitOutTheInterval(t *testing.T) {
	m := buildModel(t)

	// The commands are built here, the way Update builds them, and only run
	// off the loop below.
	start, scan, tick := m.adoptStart(), m.adoptScan(), m.adoptTick()
	if scan == nil {
		t.Fatal("adoptScan() is nil; this model cannot answer the question")
	}

	started := make(chan tea.Msg, 1)
	go func() { started <- start() }()

	var msg tea.Msg
	select {
	case msg = <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("startup produced nothing after 2s: the first scan is waiting out the %v interval", adoptScanInterval)
	}

	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("startup handed back %T, want the scan and the timer together", msg)
	}
	scanPtr, tickPtr := reflect.ValueOf(scan).Pointer(), reflect.ValueOf(tick).Pointer()
	var scans, ticks int
	for _, cmd := range batch {
		switch reflect.ValueOf(cmd).Pointer() {
		case scanPtr:
			scans++
		case tickPtr:
			ticks++
		}
	}
	if scans != 1 {
		t.Errorf("startup batched %d scans, want exactly one to run now", scans)
	}
	if ticks != 1 {
		t.Errorf("startup batched %d timers, want exactly one to cover every later scan", ticks)
	}
}

// adoptableOK is adoptable's verdict alone, for the existing cases that are
// about which panes the board may take rather than about which refusal fired.
// The manager's own pane is not in play in any of them, so self is empty.
func adoptableOK(candidate adopt.Candidate, known, onBoard map[string]bool) bool {
	ok, _ := adoptable(candidate, "", known, onBoard)
	return ok
}

// TestTheScanNeverAdoptsTheManagersOwnPane is the inception loop, on a real
// server.
//
// The manager is normally started from inside a pane an agent already holds,
// so `claude` sits in its own process ancestry and identification matches it
// like any other agent pane. Adopting it puts a row on the board that opens
// the manager inside the manager, which nests without limit.
//
// The fixture is the shape that produces it: several identical agent panes on
// one private server, one of which is declared to be the manager's own. The
// assertion is that exactly that pane is passed over and every sibling is
// still taken -- a scan that refused them all would "pass" a self-only check
// while leaving the board empty.
func TestTheScanNeverAdoptsTheManagersOwnPane(t *testing.T) {
	const windows = 4
	dir := t.TempDir()
	socket := windowFixture(t, "main", windows, dir)
	st := newFixtureStore(t)

	candidates := adopt.Panes(socket)
	if len(candidates) != windows {
		t.Fatalf("fixture server holds %d panes, want %d", len(candidates), windows)
	}
	// The manager is drawing in one of the panes the scan is about to look
	// at, which is exactly the production case. TestMain cleared TMUX_PANE,
	// so this is set explicitly rather than inherited -- inheriting it would
	// point the read at the operator's own pane on tmux's default server.
	self := candidates[1]

	run := newFixtureRun(t, st, socket)
	run.self = adoptKey(self.Socket, self.PaneID)

	taken, err := run.take(candidates, adopt.NewProcTable())
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if taken != windows-1 {
		t.Fatalf("adopted %d panes, want every one but the manager's own (%d); rejections were %s",
			taken, windows-1, rejectionSummary(run.rejected))
	}
	if run.rejected["the manager's own pane"] != 1 {
		t.Errorf("rejections were %s, want exactly one for the manager's own pane",
			rejectionSummary(run.rejected))
	}

	rows, err := st.ListSessions(true)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	panes := map[string]bool{}
	for _, sess := range rows {
		panes[adoptKey(sess.TmuxSocket, sess.TmuxPaneID)] = true
	}
	if panes[run.self] {
		t.Errorf("the board holds a row for the manager's own pane %s: opening it opens the manager inside the manager",
			self.PaneID)
	}
	for _, candidate := range candidates {
		if candidate.PaneID == self.PaneID {
			continue
		}
		if !panes[adoptKey(candidate.Socket, candidate.PaneID)] {
			t.Errorf("sibling pane %s got no row; the self rule took its neighbours with it", candidate.PaneID)
		}
	}
}

// TestASiblingManagersPaneIsStillAdoptable pins the other half of the
// decision. Only the pane this process is drawing in is refused: a second
// gate-inbox the operator is running is a real pane that opens once and shows
// the sibling, not a copy of the caller.
func TestASiblingManagersPaneIsStillAdoptable(t *testing.T) {
	self := adopt.Candidate{Socket: tmux.DefaultSocket, PaneID: "%1", Session: "main"}
	sibling := adopt.Candidate{Socket: tmux.DefaultSocket, PaneID: "%2", Session: "main"}
	selfKey := adoptKey(self.Socket, self.PaneID)

	if ok, why := adoptable(self, selfKey, map[string]bool{}, map[string]bool{}); ok {
		t.Error("adopted the manager's own pane")
	} else if why != "the manager's own pane" {
		t.Errorf("refused the manager's own pane as %q, want the self rule to be the one that fired", why)
	}
	if ok, _ := adoptable(sibling, selfKey, map[string]bool{}, map[string]bool{}); !ok {
		t.Error("refused a sibling manager's pane; only this process's own pane nests")
	}
	// A manager not running under tmux has no own pane, and an empty self key
	// must not match the pane whose id happens to be empty either.
	if ok, _ := adoptable(self, "", map[string]bool{}, map[string]bool{}); !ok {
		t.Error("an empty self key refused a pane; a manager outside tmux excludes nothing")
	}
}

// TestAStaleSelfRowIsDroppedAtStartup covers the board the fix inherits: a run
// from before the scan learned to skip itself already wrote a row for the
// manager's own pane, and adoption only ever adds, so nothing else would ever
// take it off.
func TestAStaleSelfRowIsDroppedAtStartup(t *testing.T) {
	m := buildModel(t)
	m.ownSocket, m.ownPane = "someserver", "%7"

	stale := store.Session{
		ID: newID(), Name: "the-manager-itself", Tool: "claude", Cwd: t.TempDir(),
		TmuxSocket: "someserver", TmuxPaneID: "%7",
		CreatedAt: time.Now(), LastStatusAt: time.Now(),
	}
	keeper := store.Session{
		ID: newID(), Name: "a-real-agent", Tool: "claude", Cwd: t.TempDir(),
		TmuxSocket: "someserver", TmuxPaneID: "%8",
		CreatedAt: time.Now(), LastStatusAt: time.Now(),
	}
	for _, sess := range []store.Session{stale, keeper} {
		if err := m.store.CreateSession(sess); err != nil {
			t.Fatalf("CreateSession %s: %v", sess.Name, err)
		}
	}
	if err := m.store.SetArchived(stale.ID, true); err != nil {
		// Archived on purpose: an archived self-row is still one the operator
		// can unarchive and open, so the prune has to reach it.
		t.Fatalf("SetArchived: %v", err)
	}

	m.seedFromStore()

	rows, err := m.store.ListSessions(true)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var names []string
	for _, sess := range rows {
		names = append(names, sess.Name)
		if sess.ID == stale.ID {
			t.Errorf("the row for the manager's own pane survived startup: %+v", sess)
		}
	}
	var kept bool
	for _, sess := range rows {
		kept = kept || sess.ID == keeper.ID
	}
	if !kept {
		t.Errorf("the prune took a real agent's row with it; board is %q", names)
	}
}

func TestManagedHostingSessionIsHiddenWithoutDeletingIt(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "hosting-agent", t.TempDir(), "")
	self := m.sessions[0]
	createSession(t, m, "other-agent", t.TempDir(), "")
	out, err := exec.Command("tmux", "-L", m.tmux.SocketName(), "display-message", "-p", "-t", tmux.SessionName(self.ID), "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "/tmp/tmux-1000/"+m.tmux.SocketName()+",1234,0")
	t.Setenv("TMUX_PANE", strings.TrimSpace(string(out)))
	m.ownPane, m.ownSocket = tmux.OwnPane()
	m.seedFromStore()
	if len(m.sessions) != 1 || m.sessions[0].ID == self.ID {
		t.Fatalf("startup includes the hosting agent: %+v", m.sessions)
	}
	result, ok := m.poller.refreshOnce().(refreshMsg)
	if !ok {
		t.Fatal("poll failed")
	}
	if len(result.sessions) != 1 || result.sessions[0].ID == self.ID {
		t.Fatalf("poll includes the hosting agent: %+v", result.sessions)
	}
	if _, err := m.store.Get(self.ID); err != nil {
		t.Fatalf("hosting agent was deleted: %v", err)
	}
	for _, env := range []string{"", "/tmp/tmux-1000/other-server,1234,0"} {
		t.Setenv("TMUX", env)
		rows, err := m.poller.listSessions(false)
		if err != nil || len(rows) != 2 {
			t.Fatalf("outside the hosting server, got %d rows, err %v", len(rows), err)
		}
	}
}
