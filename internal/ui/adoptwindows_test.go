package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/usestring/gate-inbox/internal/adopt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/sessname"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// The board this file is built against is the operator's real one, which the
// fixture below reproduces rather than approximates. Twenty-three agents run
// as windows of two long-lived tmux sessions, nearly all of them in one
// checkout. The scan took two of them.
//
// A fixture with one pane per session cannot see that. It was the shape the
// adoption tests already had, every one of them passed, and the rule they were
// agreeing with -- one row per tmux session name -- is exactly what dropped
// twenty-one agents. So every test here puts MANY windows in ONE session, and
// puts them all in ONE directory, because those are the two facts that make
// the operator's board different from the fixtures.
//
// Every one of them runs on a private -L socket. Nothing in this file may
// reach tmux's default server: that is where the agents this tool exists to
// show are actually running, and this tool has taken that server down before.

// fixtureAgent is what stands in for an agent CLI. It draws the prompt marker
// identification looks for and then holds the pane open as "cat", which is the
// program the tool below is configured with -- the two signals adopt.Identify
// insists on, from a real process in a real pane.
const fixtureAgent = `printf '\n❯ '; exec cat`

// fixtureTool is the agent adoption is looking for in these panes.
func fixtureTool() []adopt.Tool {
	return []adopt.Tool{{
		Name:    "claude",
		Command: "cat",
		Prompt:  regexp.MustCompile(`(?m)^❯`),
	}}
}

// windowFixture builds a tmux server of the operator's shape: one session,
// windows windows, an agent in each, all of them in dir.
//
// The socket is private and per-test. Teardown is kill-server on that socket
// by name, never a pkill: this box runs the operator's own tmux server and
// several other checkouts' test servers, and every one of them would go.
func windowFixture(t *testing.T, session string, windows int, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := newTestSocket()
	t.Cleanup(func() { killFixtureServer(t, socket) })

	newSession := tmuxOnSocket(socket, "new-session", "-d", "-s", session, "-c", dir, fixtureAgent)
	if out, err := newSession.CombinedOutput(); err != nil {
		t.Fatalf("new-session %s: %v: %s", session, err, out)
	}
	for i := 1; i < windows; i++ {
		newWindow := tmuxOnSocket(socket, "new-window", "-t", session, "-c", dir, fixtureAgent)
		if out, err := newWindow.CombinedOutput(); err != nil {
			t.Fatalf("new-window %d in %s: %v: %s", i, session, err, out)
		}
	}
	waitForPrompts(t, socket, windows)
	return socket
}

// addFixtureSession adds another session to a fixture server, for the gi_
// cases, which are about which session a pane is in rather than how many
// windows it has.
func addFixtureSession(t *testing.T, socket, session, dir string) {
	t.Helper()
	cmd := tmuxOnSocket(socket, "new-session", "-d", "-s", session, "-c", dir, fixtureAgent)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("new-session %s: %v: %s", session, err, out)
	}
}

// waitForPrompts blocks until every pane has drawn its marker. A pane read
// before its shell has run printf carries no prompt signal, and identification
// would report "not confident" -- a real refusal for a fixture that is simply
// early, and the sort of race that reads as an unrelated flake.
func waitForPrompts(t *testing.T, socket string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ready := 0
		for _, candidate := range adopt.Panes(socket) {
			text, err := adopt.Capture(candidate.Socket, candidate.PaneID)
			if err == nil && strings.Contains(text, "❯") {
				ready++
			}
		}
		if ready >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d fixture panes drew a prompt", ready, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// killFixtureServer ends one fixture server by name and proves it is gone,
// rather than assuming kill-server collected everything. A client that
// outlives its server is what kill-server cannot reach, and this package has
// leaked thousands of them.
func killFixtureServer(t *testing.T, socket string) {
	t.Helper()
	tmuxOnSocket(socket, "kill-server").Run()
	reapSocket(socket)
}

// newFixtureRun is an adoptRun over a fresh store, the way a manager with an
// empty board starts. Rebuilding one from the rows is how a restart is
// simulated below.
func newFixtureRun(t *testing.T, st *store.Store, socket string) *adoptRun {
	t.Helper()
	run := &adoptRun{
		tools:    fixtureTool(),
		known:    map[string]bool{},
		names:    map[string]bool{},
		stor:     st,
		driver:   newTestDriver(t, socket),
		rejected: map[string]int{},
	}
	rows, err := st.ListSessions(true)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	for _, sess := range rows {
		if key := adoptKey(sess.TmuxSocket, sess.TmuxPaneID); key != "" {
			run.known[key] = true
		}
		run.names[sess.Name] = true
	}
	run.onBoard = onBoardSessions(rows)
	return run
}

func newFixtureStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(tmuxtest.ScratchDir(t), "state.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestEveryWindowOfOneForeignSessionGetsItsOwnRow is the bug, at the size it
// was reported at.
//
// Eleven agents run as windows of a session called "main" -- one operator's
// habit, not a split of one agent -- and each of them is a separate
// conversation that has to be reachable from the board. The rule this
// replaces adopted the first and rejected the other ten with "session already
// has a row", which on the real board left two of twenty-three agents
// showing.
func TestEveryWindowOfOneForeignSessionGetsItsOwnRow(t *testing.T) {
	const windows = 11
	dir := t.TempDir()
	socket := windowFixture(t, "main", windows, dir)
	st := newFixtureStore(t)
	run := newFixtureRun(t, st, socket)

	taken, err := run.take(adopt.Panes(socket), adopt.NewProcTable())
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if taken != windows {
		t.Fatalf("adopted %d of %d agents in one tmux session; rejections were %s",
			taken, windows, rejectionSummary(run.rejected))
	}

	rows, err := st.ListSessions(false)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != windows {
		t.Fatalf("board holds %d rows, want one per agent (%d)", len(rows), windows)
	}
	panes := map[string]bool{}
	for _, sess := range rows {
		if sess.TmuxPaneID == "" {
			t.Errorf("row %q holds no pane id, so nothing can reach its agent", sess.Name)
		}
		if panes[sess.TmuxPaneID] {
			t.Errorf("pane %s has more than one row", sess.TmuxPaneID)
		}
		panes[sess.TmuxPaneID] = true
	}
	if len(panes) != windows {
		t.Errorf("rows address %d distinct panes, want %d", len(panes), windows)
	}
}

// TestRepeatedScansNeverGiveAPaneASecondRow is the invariant the session-wide
// refusal was carrying before this change, now carried by the pane key alone.
//
// Both halves matter. A second scan on the same run must take nothing, and so
// must a run rebuilt from the rows, which is what the manager does when it
// restarts: the pane key survives that because it is rebuilt from the stored
// socket and pane id, and the in-loop session claim never did.
func TestRepeatedScansNeverGiveAPaneASecondRow(t *testing.T) {
	const windows = 6
	dir := t.TempDir()
	socket := windowFixture(t, "main", windows, dir)
	st := newFixtureStore(t)

	run := newFixtureRun(t, st, socket)
	if taken, err := run.take(adopt.Panes(socket), adopt.NewProcTable()); err != nil || taken != windows {
		t.Fatalf("first scan took %d (err %v), want %d", taken, err, windows)
	}
	if taken, err := run.take(adopt.Panes(socket), adopt.NewProcTable()); err != nil || taken != 0 {
		t.Fatalf("second scan took %d more rows (err %v), want none", taken, err)
	}
	// A fresh run over the same panes, which is a restart of the manager.
	restarted := newFixtureRun(t, st, socket)
	if taken, err := restarted.take(adopt.Panes(socket), adopt.NewProcTable()); err != nil || taken != 0 {
		t.Fatalf("a restarted scan took %d more rows (err %v), want none", taken, err)
	}

	rows, err := st.ListSessions(true)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != windows {
		t.Fatalf("board holds %d rows after three scans of %d panes", len(rows), windows)
	}
	panes := map[string]bool{}
	for _, sess := range rows {
		if panes[sess.TmuxPaneID] {
			t.Errorf("pane %s picked up a second row across scans", sess.TmuxPaneID)
		}
		panes[sess.TmuxPaneID] = true
	}
}

// TestManagedSessionsKeepTheirSessionWideRule is the invariant this change had
// to preserve while loosening the foreign case, asserted both ways on a real
// server.
//
// An gi_ session a row accounts for is refused: it is one of the manager's own
// agents, and a second row for it would not answer to revive, rename or kill.
// An gi_ session no row accounts for is adopted: it is an orphan, an agent
// still running with nothing pointing at it, and the scan is the only thing
// that ever finds it again.
func TestManagedSessionsKeepTheirSessionWideRule(t *testing.T) {
	dir := t.TempDir()
	socket := windowFixture(t, "foreign", 2, dir)
	const tracked, orphan = "trackedrow", "orphanrow"
	addFixtureSession(t, socket, tmux.SessionName(tracked), dir)
	addFixtureSession(t, socket, tmux.SessionName(orphan), dir)
	waitForPrompts(t, socket, 4)

	st := newFixtureStore(t)
	row := store.Session{
		ID: tracked, Name: tracked, Tool: "claude", Cwd: dir,
		CreatedAt: time.Now(), LastStatusAt: time.Now(),
	}
	if err := st.CreateSession(row); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	rows, err := st.ListSessions(true)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	onBoard := onBoardSessions(rows)

	seen := map[string]bool{}
	for _, candidate := range adopt.Panes(socket) {
		switch candidate.Session {
		case tmux.SessionName(tracked):
			seen[tracked] = true
			if adoptableOK(candidate, map[string]bool{}, onBoard) {
				t.Errorf("adopted %s again; the store still has its row", candidate.Session)
			}
		case tmux.SessionName(orphan):
			seen[orphan] = true
			if !adoptableOK(candidate, map[string]bool{}, onBoard) {
				t.Errorf("left %s orphaned; no row points at it and nothing else will find it", candidate.Session)
			}
		}
	}
	if !seen[tracked] || !seen[orphan] {
		t.Fatalf("the scan did not turn up both gi_ sessions: %v", seen)
	}
}

// TestASecondPaneOfAManagedSessionIsStillRefused is the split-window case the
// gi_ rule exists for, which the pane key alone would not catch: the manager
// launched one agent, somebody split its window, and the second pane is the
// same agent rather than a new one.
func TestASecondPaneOfAManagedSessionIsStillRefused(t *testing.T) {
	managed := tmux.SessionName("abc")
	second := adopt.Candidate{Socket: "sock", PaneID: "%2", Session: managed}
	onBoard := map[string]bool{managed: true}
	if adoptableOK(second, map[string]bool{adoptKey("sock", "%1"): true}, onBoard) {
		t.Errorf("adopted a second pane of %s; that is one agent with two rows", managed)
	}
	// The same shape in a foreign session is the opposite answer: two windows
	// of "main" are two agents.
	foreignFirst := adopt.Candidate{Socket: "sock", PaneID: "%3", Session: "main"}
	foreignSecond := adopt.Candidate{Socket: "sock", PaneID: "%4", Session: "main"}
	known := map[string]bool{adoptKey("sock", foreignFirst.PaneID): true}
	if !adoptableOK(foreignSecond, known, map[string]bool{"main": true}) {
		t.Error("refused a second window of a foreign session; those are separate agents")
	}
	if adoptableOK(foreignFirst, known, map[string]bool{}) {
		t.Error("adopted a foreign pane that already has a row")
	}
}

// TestManyPanesInOneDirectoryGetDistinctMeaningfulNames is the other half of
// making the board usable. Adopting twenty-one panes is no good if the rail
// reads sample-repo, sample-repo-2 ... sample-repo-21: the operator cannot tell which row is the
// agent they want, which is the state the per-row smart rename exists to
// repair by hand, one keypress at a time.
//
// So adoption reaches for the same signal that rename does -- the pane's own
// agent process, matched against the sidecar Claude Code writes naming that
// pid and its conversation -- and names each row after the conversation
// actually running in it. The fixture is the hard case on purpose: every pane
// is in ONE directory, so nothing about the cwd can separate them.
func TestManyPanesInOneDirectoryGetDistinctMeaningfulNames(t *testing.T) {
	const windows = 4
	dir := t.TempDir()
	socket := windowFixture(t, "main", windows, dir)
	candidates := adopt.Panes(socket)
	if len(candidates) != windows {
		t.Fatalf("fixture has %d panes, want %d", len(candidates), windows)
	}

	titles := []string{
		"Fix the adoption scan",
		"Rotate the pgbackrest cipher",
		"Chase the flaky mitm test",
		"Price the spot node pool",
	}
	home := t.TempDir()
	accepted := make([]adoptCandidate, 0, windows)
	for i, candidate := range candidates {
		id := fmt.Sprintf("pane-%d", i)
		writeClaudeConversation(t, home, candidate.PID, dir, titles[i])
		accepted = append(accepted, adoptCandidate{
			id: id, candidate: candidate, tool: "claude", pane: "❯ ",
		})
	}

	named := adoptNames(accepted, adopt.NewProcTable(), map[string]bool{}, convo.New(home, ""), sessname.NewDrift())
	if len(named) != windows {
		t.Fatalf("named %d of %d panes sharing one directory: %v", len(named), windows, named)
	}
	seen := map[string]string{}
	for _, entry := range accepted {
		name := named[entry.id]
		if name == "" {
			t.Errorf("%s got no name", entry.id)
			continue
		}
		if strings.HasPrefix(name, filepath.Base(dir)) {
			t.Errorf("%s was named %q, which is the directory every pane shares", entry.id, name)
		}
		if other, dup := seen[name]; dup {
			t.Errorf("%s and %s were both named %q", other, entry.id, name)
		}
		seen[name] = entry.id
	}
	// Meaningful, not merely distinct: each name has to come from its own
	// conversation's title rather than from the position it happened to hold.
	for i, entry := range accepted {
		want := strings.Fields(strings.ToLower(titles[i]))
		name := named[entry.id]
		var hit bool
		for _, word := range want {
			if len(word) > 3 && strings.Contains(name, word) {
				hit = true
			}
		}
		if !hit {
			t.Errorf("%s was named %q, which carries nothing from its conversation title %q",
				entry.id, name, titles[i])
		}
	}
}

// TestAdoptedRowsAreNamedFromTheirConversations is the same claim through the
// scan itself, so the naming pass is reached by the code that writes the rows
// rather than only by a test that calls it.
func TestAdoptedRowsAreNamedFromTheirConversations(t *testing.T) {
	const windows = 3
	dir := t.TempDir()
	socket := windowFixture(t, "main", windows, dir)
	candidates := adopt.Panes(socket)

	titles := []string{"Fix the adoption scan", "Rotate the cipher pass", "Chase the flaky test"}
	home := t.TempDir()
	for i, candidate := range candidates {
		writeClaudeConversation(t, home, candidate.PID, dir, titles[i])
	}

	st := newFixtureStore(t)
	run := newFixtureRun(t, st, socket)
	run.index, run.drift = convo.New(home, ""), sessname.NewDrift()
	if taken, err := run.take(candidates, adopt.NewProcTable()); err != nil || taken != windows {
		t.Fatalf("take = %d (err %v), want %d", taken, err, windows)
	}

	rows, err := st.ListSessions(false)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	names := map[string]bool{}
	for _, sess := range rows {
		if names[sess.Name] {
			t.Errorf("two rows are both called %q", sess.Name)
		}
		names[sess.Name] = true
		if strings.HasPrefix(sess.Name, filepath.Base(dir)) {
			t.Errorf("row %s kept the shared directory name %q instead of its conversation's",
				sess.TmuxPaneID, sess.Name)
		}
		// A row named from a conversation is recorded as title-named, so the
		// periodic pass keeps it current instead of treating it as a
		// placeholder to overwrite from scratch.
		if sess.NameSource != store.SourceTitle {
			t.Errorf("row %q was recorded as %q, want %q", sess.Name, sess.NameSource, store.SourceTitle)
		}
	}
}

// writeClaudeConversation plants the two files Claude Code leaves behind for a
// running conversation: the sidecar naming the process, and the transcript
// carrying the title. pid is a real process in the pane's own tree, which is
// what makes the attribution an identity rather than a resemblance.
func writeClaudeConversation(t *testing.T, home string, pid int32, cwd, title string) {
	t.Helper()
	proc, err := process.NewProcess(pid)
	if err != nil {
		t.Fatalf("process %d: %v", pid, err)
	}
	created, err := proc.CreateTime()
	if err != nil {
		t.Fatalf("create time for %d: %v", pid, err)
	}
	id := uuid.NewString()

	sessions := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	sidecar := map[string]any{
		"pid": pid, "sessionId": id, "cwd": cwd, "startedAt": created,
	}
	raw, err := json.Marshal(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, fmt.Sprintf("%d.json", pid)), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	project := filepath.Join(home, "projects", claudeProjectDir(cwd))
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := strings.Join([]string{
		fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"message":{"role":"user","content":"go"}}`, id, cwd),
		fmt.Sprintf(`{"type":"ai-title","aiTitle":%q}`, title),
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
}
