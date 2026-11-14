package sessioncmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// restingRow is an idle agent with a live pane and no parent, so nothing
// tracks it: a send from the harness's caller proves the wake does not lean
// on a spawner's own restore.
func restingRow(t *testing.T, h *sessionHarness, id string) store.Session {
	t.Helper()
	return childRow(t, h, store.Session{ID: id, Name: "resting-" + id, Tool: "stoppable"}, true)
}

func hibernated(t *testing.T, h *sessionHarness) []string {
	t.Helper()
	ids, err := h.store.Hibernated()
	if err != nil {
		t.Fatalf("Hibernated: %v", err)
	}
	return ids
}

// The promise hibernate makes: mail sent while the agent is stopped is held,
// not dropped, and is still there, first in line, once a message from any
// sender has woken the agent on its conversation.
func TestHibernatedMailIsHeldAndWaitsForTheWokenAgent(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	rest := restingRow(t, h, "rest0001")

	result, err := h.sessions.Hibernate(h.caller.ID, rest.ID, false)
	if err != nil {
		t.Fatalf("Hibernate: %v", err)
	}
	if len(result.Hibernated) != 1 || h.driver.Exists(rest.ID) {
		t.Fatalf("result = %+v, pane alive = %v; want the agent stopped", result, h.driver.Exists(rest.ID))
	}
	if !slices.Contains(hibernated(t, h), rest.ID) {
		t.Fatal("the row was not marked hibernated")
	}

	// A child's report reaches its parent's queue straight through the
	// store, and the board's poll then resolves the queue of every row it
	// finds dead. Both happen to a hibernated row.
	if _, _, err := h.store.Enqueue(store.InboxMessage{SessionID: rest.ID, SenderID: "kid00001", SenderName: "a-child", Body: "report: tests pass"}, store.DefaultInboxLimits); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	dropped, err := h.store.ResolveEndedRecipient(rest.ID, time.Now())
	if err != nil || len(dropped) != 0 {
		t.Fatalf("the poll's resolve dropped %v (err %v) from a hibernated row", dropped, err)
	}

	sent, err := h.sessions.Send(h.caller.ID, rest.ID, "and one more thing", "", false)
	if err != nil {
		t.Fatalf("Send to a hibernated session from a sender that does not track it: %v", err)
	}
	if want := "woke hibernated session " + rest.ID; !strings.Contains(sent.Restored, want) {
		t.Fatalf("Restored = %q, want %q", sent.Restored, want)
	}
	if !h.driver.Exists(rest.ID) {
		t.Fatal("the send did not wake the agent")
	}
	if slices.Contains(hibernated(t, h), rest.ID) {
		t.Fatal("the woken row is still marked hibernated")
	}
	row, err := h.store.Get(rest.ID)
	if err != nil || row.Status == status.Dead {
		t.Fatalf("woken row = %+v err=%v; want it live so the poll types its mail in", row, err)
	}
	head, found, err := h.store.HeadMessage(rest.ID)
	if err != nil || !found || head.Body != "report: tests pass" {
		t.Fatalf("queue head = %+v found=%v err=%v; want the held report first", head, found, err)
	}
	if n, err := h.store.UndeliveredCount(rest.ID); err != nil || n != 2 {
		t.Fatalf("undelivered = %d err=%v; want the held report and the new message", n, err)
	}
}

// Mail that reaches a hibernated row without a send -- a child's question to
// its parent -- wakes it on the board's next pass.
func TestWakeHibernatedRelaunchesARowWithMailWaiting(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	rest := restingRow(t, h, "rest0002")
	quiet := restingRow(t, h, "rest0003")
	for _, id := range []string{rest.ID, quiet.ID} {
		if _, err := h.sessions.Hibernate(h.caller.ID, id, false); err != nil {
			t.Fatalf("Hibernate %s: %v", id, err)
		}
	}
	if _, _, err := h.store.Enqueue(store.InboxMessage{SessionID: rest.ID, SenderID: "kid00002", SenderName: "a-child", Body: "which branch?"}, store.DefaultInboxLimits); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	woken, err := h.sessions.WakeHibernated()
	if err != nil {
		t.Fatalf("WakeHibernated: %v", err)
	}
	if !slices.Equal(woken, []string{rest.ID}) {
		t.Fatalf("woken = %v, want only the row with mail", woken)
	}
	if !h.driver.Exists(rest.ID) || h.driver.Exists(quiet.ID) {
		t.Fatalf("panes: mail row alive=%v, quiet row alive=%v", h.driver.Exists(rest.ID), h.driver.Exists(quiet.ID))
	}
	if got := hibernated(t, h); !slices.Equal(got, []string{quiet.ID}) {
		t.Fatalf("hibernated = %v, want the quiet row alone", got)
	}
}

// Each thing a stop would lose holds the session running.
func TestHibernateRefusesASessionWithSomethingInFlight(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)

	working := restingRow(t, h, "busy0001")
	if err := h.store.UpdateStatus(working.ID, status.Working); err != nil {
		t.Fatal(err)
	}
	waiting := restingRow(t, h, "dlg00001")
	if err := h.store.UpdateStatus(waiting.ID, status.Waiting); err != nil {
		t.Fatal(err)
	}
	mailed := restingRow(t, h, "mail0001")
	if _, _, err := h.store.Enqueue(store.InboxMessage{SessionID: mailed.ID, SenderID: "x0000001", SenderName: "x", Body: "hi"}, store.DefaultInboxLimits); err != nil {
		t.Fatal(err)
	}
	parent := restingRow(t, h, "dad00001")
	childRow(t, h, store.Session{ID: "kid00003", Name: "busy-child", ParentID: parent.ID, Status: status.Working}, false)

	for _, tc := range []struct {
		id, want string
	}{
		{working.ID, "working, not idle or finished"},
		{waiting.ID, "waiting, not idle or finished"},
		{mailed.ID, "1 message(s) are waiting"},
		{parent.ID, "child busy-child (id kid00003) is working"},
		{h.caller.ID, "cannot hibernate itself"},
	} {
		_, err := h.sessions.Hibernate(h.caller.ID, tc.id, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Hibernate(%s) error = %v, want %q", tc.id, err, tc.want)
		}
		if tc.id != h.caller.ID && !h.driver.Exists(tc.id) {
			t.Errorf("refused hibernate of %s still stopped it", tc.id)
		}
	}
	if got := hibernated(t, h); len(got) != 0 {
		t.Fatalf("hibernated = %v after refusals", got)
	}
}

// The sweep takes only sessions rested past the window, and a dry run
// stops nothing.
func TestHibernateIdleTakesOnlySessionsRestedPastTheWindow(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	old := restingRow(t, h, "old00001")
	fresh := restingRow(t, h, "new00001")
	// A status move is stamped with the pass's time, which is how a row
	// comes to have rested a given while.
	if err := h.store.ApplyDerivedStates(time.Now().Add(-4*time.Hour), []store.DerivedState{{ID: old.ID, Status: status.Finished}}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.ApplyDerivedStates(time.Now(), []store.DerivedState{{ID: fresh.ID, Status: status.Finished}}); err != nil {
		t.Fatal(err)
	}

	plan, err := h.sessions.HibernateIdle(3*time.Hour, 0, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !h.driver.Exists(old.ID) || len(hibernated(t, h)) != 0 {
		t.Fatal("the dry run stopped something")
	}
	var planned []string
	for _, sess := range plan.Hibernated {
		planned = append(planned, sess.ID)
	}
	if !slices.Contains(planned, old.ID) || slices.Contains(planned, fresh.ID) {
		t.Fatalf("plan = %v, want the rested row and not the fresh one", planned)
	}

	if _, err := h.sessions.HibernateIdle(3*time.Hour, 0, false); err != nil {
		t.Fatalf("HibernateIdle: %v", err)
	}
	if h.driver.Exists(old.ID) || !h.driver.Exists(fresh.ID) {
		t.Fatalf("old alive=%v fresh alive=%v", h.driver.Exists(old.ID), h.driver.Exists(fresh.ID))
	}
	if row, err := h.store.Get(old.ID); err != nil || row.Archived {
		t.Fatalf("hibernated row = %+v err=%v; it must stay on the active list", row, err)
	}
}

// The minimum-sessions floor: at or below it the sweep stops nothing, and
// above it only enough to come back down to it, longest rested first.
func TestHibernateIdleKeepsTheMinimumLiveSessions(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	oldest := restingRow(t, h, "oldest01")
	older := restingRow(t, h, "older001")
	if err := h.store.ApplyDerivedStates(time.Now().Add(-6*time.Hour), []store.DerivedState{{ID: oldest.ID, Status: status.Finished}}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.ApplyDerivedStates(time.Now().Add(-4*time.Hour), []store.DerivedState{{ID: older.ID, Status: status.Finished}}); err != nil {
		t.Fatal(err)
	}
	rows, err := h.store.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	live := 0
	for _, row := range rows {
		if !row.Archived && row.Status != status.Dead {
			live++
		}
	}

	held, err := h.sessions.HibernateIdle(3*time.Hour, live, true)
	if err != nil {
		t.Fatalf("at the floor: %v", err)
	}
	if len(held.Hibernated) != 0 {
		t.Fatalf("at the floor the sweep planned %v, want nothing", held.Hibernated)
	}

	result, err := h.sessions.HibernateIdle(3*time.Hour, live-1, false)
	if err != nil {
		t.Fatalf("one above the floor: %v", err)
	}
	if len(result.Hibernated) != 1 || result.Hibernated[0].ID != oldest.ID {
		t.Fatalf("hibernated = %v, want only the longest rested %s", result.Hibernated, oldest.ID)
	}
	if h.driver.Exists(oldest.ID) || !h.driver.Exists(older.ID) {
		t.Fatalf("oldest alive=%v older alive=%v", h.driver.Exists(oldest.ID), h.driver.Exists(older.ID))
	}
}

// A row that can no longer come back -- its worktree was removed while it
// slept -- must not hold its mail forever: it leaves the set and its queue is
// resolved as an ended session's, so the sender hears what went.
func TestWakeHibernatedGivesUpOnARowThatCannotComeBack(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	dir := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := childRow(t, h, store.Session{ID: "gone0001", Name: "resting-gone", Tool: "stoppable", Cwd: dir}, true)
	if _, err := h.sessions.Hibernate(h.caller.ID, gone.ID, false); err != nil {
		t.Fatalf("Hibernate: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.store.Enqueue(store.InboxMessage{SessionID: gone.ID, SenderID: h.caller.ID, SenderName: h.caller.Name, Body: "still there?"}, store.DefaultInboxLimits); err != nil {
		t.Fatal(err)
	}

	woken, err := h.sessions.WakeHibernated()
	if err == nil || len(woken) != 0 {
		t.Fatalf("woken = %v err = %v; want a failure naming the row", woken, err)
	}
	if slices.Contains(hibernated(t, h), gone.ID) {
		t.Fatal("a row that cannot be woken is still hibernated and would be retried every pass")
	}
	if n, err := h.store.UndeliveredCount(gone.ID); err != nil || n != 0 {
		t.Fatalf("undelivered = %d err=%v; want the queue resolved", n, err)
	}
}

// A hibernated session is resting, not over, and the tools say so: a caller
// told it was dead would revive it by hand or give up on the work it holds.
// A row that really died stays dead beside it.
func TestListReportsAHibernatedSessionAsHibernatingNotDead(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	rest := restingRow(t, h, "rest0010")
	gone := childRow(t, h, store.Session{ID: "gone0010", Name: "gone", Status: status.Dead}, false)
	if _, err := h.sessions.Hibernate(h.caller.ID, rest.ID, false); err != nil {
		t.Fatalf("Hibernate: %v", err)
	}

	list, err := h.sessions.List(h.caller.ID, ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]string{}
	for _, sess := range list.Sessions {
		got[sess.ID] = sess.Status
	}
	if got[rest.ID] != status.Hibernating || got[gone.ID] != status.Dead {
		t.Fatalf("statuses = %v; want %s hibernating and %s dead", got, rest.ID, gone.ID)
	}

	only, err := h.sessions.List(h.caller.ID, ListOptions{Status: []string{status.Hibernating}})
	if err != nil {
		t.Fatalf("List hibernating: %v", err)
	}
	if len(only.Sessions) != 1 || only.Sessions[0].ID != rest.ID {
		t.Fatalf("hibernating filter = %+v, want only %s", only.Sessions, rest.ID)
	}
	dead, err := h.sessions.List(h.caller.ID, ListOptions{Status: []string{status.Dead}})
	if err != nil {
		t.Fatalf("List dead: %v", err)
	}
	for _, sess := range dead.Sessions {
		if sess.ID == rest.ID {
			t.Fatal("the dead filter listed a hibernated session")
		}
	}
}

// send_children is a message to every child, and a hibernated child is
// resting until one comes: it wakes on its conversation and the message is
// queued once, to go in when it is at its prompt, instead of being skipped
// as dead.
func TestSendChildrenWakesAHibernatedChildAndQueuesTheMessageOnce(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	kid := childRow(t, h, store.Session{ID: "kid00010", Name: "sleepy-kid", Tool: "stoppable", ParentID: h.caller.ID}, true)
	if _, err := h.sessions.Hibernate(h.caller.ID, kid.ID, false); err != nil {
		t.Fatalf("Hibernate: %v", err)
	}

	sent, err := h.sessions.SendChildren(h.caller.ID, "the branch moved")
	if err != nil {
		t.Fatalf("SendChildren: %v", err)
	}
	if sent.Queued != 1 || sent.Skipped != 0 {
		t.Fatalf("sent = %+v, want the hibernated child sent to", sent)
	}
	if !h.driver.Exists(kid.ID) {
		t.Fatal("send_children did not wake the hibernated child")
	}
	if slices.Contains(hibernated(t, h), kid.ID) {
		t.Fatal("the woken child is still marked hibernated")
	}
	if n, err := h.store.UndeliveredCount(kid.ID); err != nil || n != 1 {
		t.Fatalf("queued for the child = %d (err %v), want the message once", n, err)
	}
}

// A hibernated session stopped only once it was at rest, so a wait on it
// has its answer: it reached a resting state rather than died.
func TestObserveWaitCountsAHibernatedSessionAsRestingNotDied(t *testing.T) {
	t.Parallel()
	current := []store.Session{{ID: "rest0011", Status: status.Hibernating}, {ID: "gone0011", Status: status.Dead}}
	wanted := map[string]bool{}
	for _, state := range restingStates {
		wanted[state] = true
	}
	seen, outcome, settled, err := observeWait(current, map[string]bool{}, wanted, func(store.Session) (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("observeWait: %v", err)
	}
	if outcome != WaitReached || settled != 2 {
		t.Fatalf("outcome = %s, settled = %d; want reached with both settled", outcome, settled)
	}
	if seen[0].state != status.Hibernating || seen[0].outcome != WaitReached {
		t.Fatalf("hibernated observation = %+v, want hibernating and reached", seen[0])
	}

	delete(wanted, status.Hibernating)
	delete(wanted, status.Dead)
	seen, _, _, err = observeWait(current, map[string]bool{}, wanted, func(store.Session) (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("observeWait: %v", err)
	}
	if seen[0].outcome == WaitDied || seen[1].outcome != WaitDied {
		t.Fatalf("outcomes = %s, %s; a hibernated session never reads as a death, a dead one does", seen[0].outcome, seen[1].outcome)
	}
}
