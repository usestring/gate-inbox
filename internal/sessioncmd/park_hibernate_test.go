package sessioncmd

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// hibernateBoard is a board with one session hibernate would take and one
// of each reason it would not. Each is parked; only the first is hibernated.
type hibernateBoard struct {
	rest, fresh, parent, child, waiting, dialog, mailed, staleParent, staleChild store.Session
}

func newHibernateBoard(t *testing.T, h *sessionHarness) hibernateBoard {
	t.Helper()
	old := time.Now().Add(-4 * time.Hour)
	var b hibernateBoard
	b.rest = childRow(t, h, store.Session{ID: "hib00001", Name: "long-rest", Tool: "stoppable", LastStatusAt: old}, true)
	// Rested less than hibernate_after.
	b.fresh = childRow(t, h, store.Session{ID: "hib00002", Name: "short-rest", Tool: "stoppable"}, true)
	// A parent at rest long enough, with a child still working under it.
	b.parent = childRow(t, h, store.Session{ID: "hib00003", Name: "parent", Tool: "stoppable", Status: status.Finished, LastStatusAt: old}, true)
	b.child = childRow(t, h, store.Session{ID: "hib00004", Name: "working-child", ParentID: b.parent.ID, SpawnedBy: b.parent.ID, Status: status.Working, LastStatusAt: old}, true)
	// Stored as waiting on a question.
	b.waiting = childRow(t, h, store.Session{ID: "hib00005", Name: "asking", Tool: "stoppable", Status: status.Waiting, LastStatusAt: old}, true)
	// Stored as idle long enough, but its screen shows a dialog: no poller
	// moved the row, and park's own read of the screen must win.
	dialog, err := h.sessions.Create(h.caller.ID, CreateSessionOptions{Name: "dialog", Tool: "dialog"})
	if err != nil {
		t.Fatalf("Create dialog: %v", err)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, dialog.ID, "Enter to confirm")
	if err := h.store.ApplyDerivedStates(old, []store.DerivedState{{ID: dialog.ID, Status: status.Idle}}); err != nil {
		t.Fatalf("settle dialog: %v", err)
	}
	if b.dialog, err = h.store.Get(dialog.ID); err != nil {
		t.Fatal(err)
	}
	// At rest long enough, with a message still to be typed in.
	b.mailed = childRow(t, h, store.Session{ID: "hib00006", Name: "mailed", Tool: "stoppable", LastStatusAt: old}, true)
	if _, _, err := h.store.Enqueue(store.InboxMessage{SessionID: b.mailed.ID, SenderID: "x0000001", SenderName: "x", Body: "hi"}, store.DefaultInboxLimits); err != nil {
		t.Fatal(err)
	}
	// A parent whose child's row still reads idle, because no poller moved
	// it, while the child's screen shows it working.
	b.staleParent = childRow(t, h, store.Session{ID: "hib00007", Name: "stale-parent", Tool: "stoppable", LastStatusAt: old}, true)
	b.staleChild = childRow(t, h, store.Session{ID: "hib00008", Name: "stale-child", Tool: "busy", ParentID: b.staleParent.ID, SpawnedBy: b.staleParent.ID, LastStatusAt: old}, false)
	if err := h.driver.Create(b.staleChild.ID, b.staleChild.Cwd, "printf 'Thinking about it\\n'; sleep 60", nil, 80, 24); err != nil {
		t.Fatalf("create stale child pane: %v", err)
	}
	t.Cleanup(func() { _ = h.driver.Kill(b.staleChild.ID) })
	waitForSessionOutput(t, h.sessions, h.caller.ID, b.staleChild.ID, "Thinking about it")
	return b
}

func (b hibernateBoard) parkedIDs() []string {
	return sorted(b.fresh.ID, b.parent.ID, b.child.ID, b.waiting.ID, b.dialog.ID, b.mailed.ID, b.staleParent.ID, b.staleChild.ID)
}

func sessionIDs(sessions []Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		ids = append(ids, sess.ID)
	}
	return ids
}

// With hibernate_after set, park hibernates what hibernate would take and
// parks the rest, and unpark brings back only the parked ones.
func TestParkHibernatesWhatHibernateWouldTakeAndParksTheRest(t *testing.T) {
	t.Parallel()
	h := newSessionHarnessWithConfig(t, "hibernate_after = \"1h\"\n"+sessionConfig)
	b := newHibernateBoard(t, h)

	plan, err := h.sessions.Park(h.caller.ID, true)
	if err != nil {
		t.Fatalf("Park dry run: %v", err)
	}
	if got := sessionIDs(plan.Hibernated); !reflect.DeepEqual(got, []string{b.rest.ID}) {
		t.Fatalf("dry run hibernated %v, want %v", got, []string{b.rest.ID})
	}
	if got := parkedIDs(plan); !reflect.DeepEqual(got, b.parkedIDs()) {
		t.Fatalf("dry run parked %v, want %v", got, b.parkedIDs())
	}
	if plan.Owed != 8 {
		t.Fatalf("dry run owed %d, want 8", plan.Owed)
	}
	if line := FormatParkResult(plan); !strings.Contains(line, "would hibernate 1 session(s) idle at least 1h0m0s instead of parking them") {
		t.Fatalf("dry run says %q", line)
	}
	if !h.driver.Exists(b.rest.ID) || len(hibernated(t, h)) != 0 {
		t.Fatal("the dry run stopped or recorded something")
	}
	if owed, _ := h.store.Parked(); len(owed) != 0 {
		t.Fatalf("the dry run recorded a parked set %v", owed)
	}

	parked, err := h.sessions.Park(h.caller.ID, false)
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	if len(parked.Errors) != 0 {
		t.Fatalf("Park errors: %v", parked.Errors)
	}
	if got := sessionIDs(parked.Hibernated); !reflect.DeepEqual(got, []string{b.rest.ID}) {
		t.Fatalf("hibernated %v, want %v", got, []string{b.rest.ID})
	}
	if got := parkedIDs(parked); !reflect.DeepEqual(got, b.parkedIDs()) {
		t.Fatalf("parked %v, want %v", got, b.parkedIDs())
	}
	if slices.Contains(parked.Dormant, b.rest.ID) || slices.Contains(parked.Working, b.rest.ID) {
		t.Fatalf("the hibernated row is in a park set: dormant %v working %v", parked.Dormant, parked.Working)
	}
	if parked.Owed != 8 {
		t.Fatalf("owed %d, want the 8 parked", parked.Owed)
	}
	if line := FormatParkResult(parked); !strings.HasPrefix(line, "parked 8 session(s)") ||
		!strings.Contains(line, "hibernated 1 session(s) idle at least 1h0m0s instead of parking them: they stay on the board and wake on their next message") {
		t.Fatalf("park says %q", line)
	}
	if h.driver.Exists(b.rest.ID) {
		t.Fatal("the hibernated row is still running")
	}
	if got := hibernated(t, h); !reflect.DeepEqual(got, []string{b.rest.ID}) {
		t.Fatalf("store hibernated = %v", got)
	}
	if owed, _ := h.store.Parked(); slices.Contains(owed, b.rest.ID) {
		t.Fatalf("parked set %v holds the hibernated row", owed)
	}
	row, err := h.store.Get(b.rest.ID)
	if err != nil || row.Status != status.Dead || row.Archived {
		t.Fatalf("hibernated row = %+v err=%v; want dead and on the active list", row, err)
	}
	ends, err := h.store.SessionEnds()
	if err != nil {
		t.Fatal(err)
	}
	if got := ends[b.rest.ID].Reason; got != store.EndHibernated {
		t.Fatalf("end reason = %q, want %q", got, store.EndHibernated)
	}
	if got := ends[b.fresh.ID].Reason; got != store.EndParked {
		t.Fatalf("parked row end reason = %q, want %q", got, store.EndParked)
	}

	unparked, err := h.sessions.Unpark(h.caller.ID)
	if err != nil {
		t.Fatalf("Unpark: %v", err)
	}
	if got := sorted(sessionIDs(unparked.Revived)...); !reflect.DeepEqual(got, b.parkedIDs()) {
		t.Fatalf("revived %v, want %v", got, b.parkedIDs())
	}
	if h.driver.Exists(b.rest.ID) {
		t.Fatal("unpark revived the hibernated row")
	}
	if got := hibernated(t, h); !reflect.DeepEqual(got, []string{b.rest.ID}) {
		t.Fatalf("store hibernated after unpark = %v", got)
	}
}

// Hibernation is opt-in: without hibernate_after, park parks everything.
func TestParkHibernatesNothingWithoutHibernateAfter(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	b := newHibernateBoard(t, h)
	parked, err := h.sessions.Park(h.caller.ID, false)
	if err != nil {
		t.Fatalf("Park: %v", err)
	}
	if len(parked.Hibernated) != 0 || len(hibernated(t, h)) != 0 {
		t.Fatalf("hibernated %v with hibernation off", sessionIDs(parked.Hibernated))
	}
	if got, want := parkedIDs(parked), sorted(append(b.parkedIDs(), b.rest.ID)...); !reflect.DeepEqual(got, want) {
		t.Fatalf("parked %v, want %v", got, want)
	}
	if strings.Contains(FormatParkResult(parked), "hibernat") {
		t.Fatalf("park says %q", FormatParkResult(parked))
	}
}
