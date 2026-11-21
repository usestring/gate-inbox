package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/tracetest"
)

// The board froze three times in one day, each time for longer than the
// watchdog's 20 seconds, and each dump showed the event loop parked on
// somebody else's work:
//
//   - an adopt tick reading a setting (store.Setting) while a poll pass held
//     the store's one connection: 27 seconds;
//   - the child sweep's landing ending a finished child (killPanes) behind
//     the poller's lock while that same pass held it for its 43-second tmux
//     capture.
//
// A slow store or a slow tmux server may delay what these messages bring
// back. It may not hold the loop. So each test here holds both -- the
// store's connection and the poller's lock, the way a slow pass does -- and
// fails if Update does not come back.

// loopDeadline is how long Update may take on a message whose work is meant
// to be in a command. Generous, because CI machines are loaded; anything that
// waits on a held lock waits for ever, so the margin costs nothing.
const loopDeadline = 10 * time.Second

// updateWhileBlocked runs one message through Update with the store's
// connection and the poller's lock both held, and fails if Update waited on
// either or forked tmux. It returns the command Update handed back, for the
// test to run once they are released.
func updateWhileBlocked(t *testing.T, m *Model, msg tea.Msg) tea.Cmd {
	t.Helper()
	release, err := m.store.Hold(context.Background())
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	m.poller.runMu.Lock()
	unblock := func() {
		release()
		m.poller.runMu.Unlock()
	}
	read := tracetest.Capture(t)

	done := make(chan tea.Cmd, 1)
	go func() {
		_, cmd := m.Update(msg)
		done <- cmd
	}()
	var cmd tea.Cmd
	select {
	case cmd = <-done:
	case <-time.After(loopDeadline):
		// Let it finish before the test returns, so it does not run on into
		// the next test's model.
		unblock()
		<-done
		t.Fatalf("Update(%T) did not return in %v with the store and the poller's lock held: it waits on one of them on the event loop", msg, loopDeadline)
	}
	unblock()
	for _, span := range read() {
		if strings.HasPrefix(span.Name, "tmux.") {
			t.Errorf("Update(%T) forked tmux on the event loop (%s)", msg, span.Name)
		}
	}
	return cmd
}

// The 11:22 stack: adoptScan -> newAdoptRun -> loadPaneDecisions ->
// store.Setting, on the loop, behind a poll pass.
func TestAnAdoptTickDoesNotWaitOnTheStore(t *testing.T) {
	m := buildModel(t)
	m.adoptBusy = false
	if m.newAdoptRun() == nil {
		t.Fatal("this model has nothing to adopt with, so the tick takes a branch that never scans")
	}
	if cmd := updateWhileBlocked(t, m, adoptTickMsg{}); cmd == nil {
		t.Fatal("the tick armed no scan")
	}
	if !m.adoptBusy {
		t.Fatal("the tick did not send a scan out")
	}
}

// The 11:51 stack: applyChildSweep -> retireFinishedChildren ->
// archiveSessions -> killPanes -> reflowSessions, on the loop, waiting for
// the poller's lock.
//
// The landing may only decide. The kill, the capture kept for the archive
// and the store writes are the command's, and the row leaves the board when
// that command lands.
func TestAChildSweepLandingDoesNotWaitOnTheStoreOrThePoller(t *testing.T) {
	m, parent, kid := finishedFanOut(t)
	relayRest(t, m, parent, kid, true)
	answer := armChildSweep(t, m)()
	msg, ok := answer.(childSweptMsg)
	if !ok || len(msg.retire) != 1 {
		t.Fatalf("the sweep answered %#v, want one finished child to retire", answer)
	}

	teardown := updateWhileBlocked(t, m, msg)
	if teardown == nil {
		t.Fatal("the landing sent no teardown for the finished child")
	}
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived || !m.tmux.Exists(kid.ID) {
		t.Fatalf("the child was ended on the loop, before the teardown ran: %+v, %v", row, err)
	}
	if cmd := m.sweepFinishedChildren(); cmd != nil {
		t.Fatal("a second sweep went out while the first one's teardown was still running")
	}

	retired, ok := teardown().(childRetiredMsg)
	if !ok {
		t.Fatal("the teardown did not answer with childRetiredMsg")
	}
	m.Update(retired)
	row, err := m.store.Get(kid.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !row.Archived || m.tmux.Exists(kid.ID) {
		t.Fatalf("the teardown did not end and file the child: archived=%t, pane up=%t", row.Archived, m.tmux.Exists(kid.ID))
	}
	if onBoard, _ := m.sessionByID(kid.ID); !onBoard.Archived {
		t.Fatal("the child is still on the board after its teardown landed")
	}
	if cmd := m.sweepFinishedChildren(); cmd == nil {
		t.Fatal("the next sweep never came back after the teardown landed")
	}
}

// The teardown now runs a moment after the landing chose the child. A child
// sent a follow-up in that moment is working again, and must not be ended.
func TestAChildBackAtWorkBeforeItsTeardownRunsIsLeftAlone(t *testing.T) {
	m, parent, kid := finishedFanOut(t)
	relayRest(t, m, parent, kid, true)
	msg, ok := armChildSweep(t, m)().(childSweptMsg)
	if !ok || len(msg.retire) != 1 {
		t.Fatalf("the sweep answered %#v, want one finished child to retire", msg)
	}
	_, teardown := m.Update(msg)
	if teardown == nil {
		t.Fatal("the landing sent no teardown for the finished child")
	}
	if err := m.store.UpdateStatus(kid.ID, status.Working); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	retired, ok := teardown().(childRetiredMsg)
	if !ok {
		t.Fatal("the teardown did not answer with childRetiredMsg")
	}
	m.Update(retired)
	if row, err := m.store.Get(kid.ID); err != nil || row.Archived || !m.tmux.Exists(kid.ID) {
		t.Fatalf("the teardown ended a child that had gone back to work: %+v, %v", row, err)
	}
}

// Every refresh asked the store for the queue heads on the loop, to resume
// held snippets, whether or not any were held. A refresh is every poll pass,
// so this was the most frequent store read the loop made, and it queued on
// the same connection the slow pass was holding.
func TestARoutineRefreshDoesNotWaitOnTheStore(t *testing.T) {
	m := buildModel(t)
	createSession(t, m, "watched", t.TempDir(), "")
	loadStoredRows(t, m)
	m.width, m.height = 120, 40
	// The first refresh restores what earlier runs left held, once, and
	// re-registers adopted panes; the one under test is a routine pass.
	m.Update(refreshMsg{sessions: m.sessions, listedAt: time.Now()})
	if len(m.heldSnippets) != 0 {
		t.Fatal("this board has held snippets, so the refresh takes the branch that must read the queue")
	}
	updateWhileBlocked(t, m, refreshMsg{sessions: m.sessions, listedAt: time.Now()})
}
