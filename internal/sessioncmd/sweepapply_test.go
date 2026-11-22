package sessioncmd

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sweep"
)

// staleRow is a row that has sat in state for a week, with a pane when it
// is not dead.
func staleRow(t *testing.T, h *sessionHarness, id, state string, extra store.Session) store.Session {
	t.Helper()
	extra.ID, extra.Name, extra.Tool = id, "stale-"+id, "stoppable"
	if extra.AgentSessionID == "" {
		extra.AgentSessionID = "conv-" + id
	}
	sess := childRow(t, h, extra, state != status.Dead)
	if err := h.store.ApplyDerivedStates(time.Now().Add(-7*24*time.Hour), []store.DerivedState{{ID: id, Status: state}}); err != nil {
		t.Fatal(err)
	}
	return sess
}

// boardPolling stamps the heartbeat a running board writes, without which
// an apply takes every status for out of date.
func boardPolling(t *testing.T, h *sessionHarness) {
	t.Helper()
	if err := h.store.SetSetting(store.PollerHeartbeatKey, strconv.FormatInt(time.Now().UnixNano(), 10)); err != nil {
		t.Fatal(err)
	}
}

// sweepBoard reads the board the way the sweep command does.
func sweepBoard(t *testing.T, h *sessionHarness) []sweep.Proposal {
	t.Helper()
	sessions, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := h.store.WaitingMessages()
	if err != nil {
		t.Fatal(err)
	}
	return sweep.Sweep(sweep.Input{Sessions: sessions, Messages: messages, Hibernated: hibernated(t, h), Now: time.Now()}, sweep.DefaultThresholds).Proposals
}

func outcomes(applied sweep.Applied) map[string]sweep.Outcome {
	out := map[string]sweep.Outcome{}
	for _, o := range applied.Outcomes {
		out[o.SessionID] = o
	}
	return out
}

func claim(t *testing.T, h *sessionHarness, taskID, owner string) {
	t.Helper()
	if err := h.store.CreateTask(store.Task{ID: taskID, Title: "work " + taskID, State: store.TaskPending, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if ok, err := h.store.ClaimTask(taskID, owner, time.Now()); err != nil || !ok {
		t.Fatalf("claim %s for %s: ok=%v err=%v", taskID, owner, ok, err)
	}
}

func claims(t *testing.T, h *sessionHarness, owner string) int {
	t.Helper()
	page, err := h.store.QueryTasks(store.TaskQuery{Owner: owner, States: []string{store.TaskInProgress}})
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Tasks)
}

// The default apply is the safe subset: a resting row hibernate would take
// is hibernated, keeping its claim; an ended row is archived; and every row
// that would lose a question, mail, a child or a claim is left as it was,
// with the reason.
func TestSweepApplyHibernatesOrArchivesAndHoldsWhatWouldLoseSomething(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	rested := staleRow(t, h, "aaa00001", status.Finished, store.Session{})
	claim(t, h, "task-rested", rested.ID)
	ended := staleRow(t, h, "aaa00002", status.Dead, store.Session{})
	asking := staleRow(t, h, "aaa00003", status.Waiting, store.Session{})
	errored := staleRow(t, h, "aaa00004", status.Errored, store.Session{})
	mailed := staleRow(t, h, "aaa00005", status.Idle, store.Session{})
	if _, _, err := h.store.Enqueue(store.InboxMessage{SessionID: mailed.ID, SenderID: "x0000001", SenderName: "x", Body: "hi"}, store.DefaultInboxLimits); err != nil {
		t.Fatal(err)
	}
	parent := staleRow(t, h, "aaa00006", status.Dead, store.Session{})
	childRow(t, h, store.Session{ID: "aaa00007", Name: "busy-child", ParentID: parent.ID, Status: status.Working}, true)
	claimed := staleRow(t, h, "aaa00008", status.Dead, store.Session{})
	claim(t, h, "task-claimed", claimed.ID)

	proposals := sweepBoard(t, h)
	plan, err := h.sessions.SweepApply("", proposals, SweepApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !plan.DryRun || !h.driver.Exists(rested.ID) || len(hibernated(t, h)) != 0 {
		t.Fatalf("the dry run changed the board: %+v", plan)
	}
	if row, err := h.store.Get(ended.ID); err != nil || row.Archived {
		t.Fatalf("the dry run archived a row: %+v err=%v", row, err)
	}

	// With no board polling the statuses may be days old: the plan above
	// was printed, but nothing is carried out on the strength of them.
	if _, err := h.sessions.SweepApply("", proposals, SweepApplyOptions{}); err == nil || !strings.Contains(err.Error(), "the board is not running") {
		t.Fatalf("apply with no board polling: err = %v", err)
	}
	if !h.driver.Exists(rested.ID) || len(hibernated(t, h)) != 0 {
		t.Fatal("the refused apply changed the board")
	}
	boardPolling(t, h)

	applied, err := h.sessions.SweepApply("", proposals, SweepApplyOptions{})
	if err != nil {
		t.Fatalf("SweepApply: %v", err)
	}
	got := outcomes(applied)
	for id, want := range map[string]struct{ outcome, detail string }{
		rested.ID:  {sweep.Hibernated, "next message wakes it"},
		ended.ID:   {sweep.Archived, ""},
		asking.ID:  {sweep.Held, "--kill-waiting"},
		errored.ID: {sweep.Held, "--revive"},
		mailed.ID:  {sweep.Held, "1 message(s) are waiting"},
		parent.ID:  {sweep.Held, "busy-child (id aaa00007) under it is working"},
		claimed.ID: {sweep.Held, "holds 1 task claim(s)"},
	} {
		if got[id].Outcome != want.outcome || !strings.Contains(got[id].Detail, want.detail) {
			t.Errorf("%s = %q (%s), want %q with %q", id, got[id].Outcome, got[id].Detail, want.outcome, want.detail)
		}
		if plan := outcomes(plan)[id]; plan.Outcome != want.outcome {
			t.Errorf("the dry run planned %q for %s, and the apply did %q", plan.Outcome, id, want.outcome)
		}
	}
	if applied.Counts[sweep.Hibernated] != 1 || applied.Counts[sweep.Archived] != 1 || applied.Counts[sweep.Held] != 5 {
		t.Errorf("counts = %v", applied.Counts)
	}

	if h.driver.Exists(rested.ID) || !slices.Contains(hibernated(t, h), rested.ID) {
		t.Error("the rested row was not hibernated")
	}
	if row, err := h.store.Get(rested.ID); err != nil || row.Archived || claims(t, h, rested.ID) != 1 {
		t.Errorf("hibernated row = %+v err=%v claims=%d; want it on the list with its claim", row, err, claims(t, h, rested.ID))
	}
	if row, err := h.store.Get(ended.ID); err != nil || !row.Archived {
		t.Errorf("ended row = %+v err=%v; want it archived", row, err)
	}
	for _, id := range []string{asking.ID, errored.ID, mailed.ID, "aaa00007"} {
		if !h.driver.Exists(id) {
			t.Errorf("held row %s was stopped", id)
		}
	}
	for _, id := range []string{parent.ID, claimed.ID, mailed.ID} {
		if row, err := h.store.Get(id); err != nil || row.Archived {
			t.Errorf("held row %s = %+v err=%v; want it left on the list", id, row, err)
		}
	}
	if claims(t, h, claimed.ID) != 1 {
		t.Error("a held row's task claim was released")
	}

	// A hibernated row reads dead in the store. The next sweep must not
	// take it for an ended one and archive what the last one kept.
	for _, p := range sweepBoard(t, h) {
		if p.SessionID == rested.ID {
			t.Fatalf("the next sweep proposed %q for the row this one hibernated", p.Action)
		}
	}
}

// Each thing the default holds has a flag that asks for it.
func TestSweepApplyFlagsCarryOutWhatTheDefaultHolds(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	asking := staleRow(t, h, "bbb00001", status.Waiting, store.Session{})
	errored := staleRow(t, h, "bbb00002", status.Errored, store.Session{Tool: "echoer"})
	if err := h.driver.Kill(errored.ID); err != nil {
		t.Fatal(err)
	}
	claimed := staleRow(t, h, "bbb00003", status.Dead, store.Session{})
	claim(t, h, "task-forced", claimed.ID)
	boardPolling(t, h)

	applied, err := h.sessions.SweepApply("", sweepBoard(t, h), SweepApplyOptions{Revive: true, KillWaiting: true, Force: true})
	if err != nil {
		t.Fatalf("SweepApply: %v", err)
	}
	got := outcomes(applied)
	if got[asking.ID].Outcome != sweep.Killed || h.driver.Exists(asking.ID) {
		t.Errorf("waiting row = %+v, alive=%v; want it killed", got[asking.ID], h.driver.Exists(asking.ID))
	}
	if got[errored.ID].Outcome != sweep.Revived || !h.driver.Exists(errored.ID) {
		t.Errorf("errored row = %+v, alive=%v; want it revived", got[errored.ID], h.driver.Exists(errored.ID))
	}
	if o := got[claimed.ID]; o.Outcome != sweep.Archived || o.ReleasedTasks != 1 || claims(t, h, claimed.ID) != 0 {
		t.Errorf("claimed row = %+v; want it archived with its claim released", o)
	}
}

// A sweep run from inside a session leaves that session and the ones above
// it alone, and a row that moved after the report was read is somebody
// using it.
func TestSweepApplySparesItsCallerAndRowsThatMoved(t *testing.T) {
	t.Parallel()
	h := newSessionHarness(t)
	if err := h.store.ApplyDerivedStates(time.Now().Add(-7*24*time.Hour), []store.DerivedState{{ID: h.caller.ID, Status: status.Finished}}); err != nil {
		t.Fatal(err)
	}
	moved := staleRow(t, h, "ccc00001", status.Finished, store.Session{})
	boardPolling(t, h)
	proposals := sweepBoard(t, h)
	if err := h.store.UpdateStatus(moved.ID, status.Working); err != nil {
		t.Fatal(err)
	}

	applied, err := h.sessions.SweepApply(h.caller.ID, proposals, SweepApplyOptions{Force: true})
	if err != nil {
		t.Fatalf("SweepApply: %v", err)
	}
	got := outcomes(applied)
	if o := got[h.caller.ID]; o.Outcome != sweep.Held || !strings.Contains(o.Detail, "running this sweep") {
		t.Errorf("caller = %+v, want it held", o)
	}
	if o := got[moved.ID]; o.Outcome != sweep.Held || !strings.Contains(o.Detail, "changed after the sweep read it") {
		t.Errorf("moved row = %+v, want it held", o)
	}
	if !h.driver.Exists(h.caller.ID) || !h.driver.Exists(moved.ID) || len(hibernated(t, h)) != 0 {
		t.Error("a held row was stopped")
	}
}
