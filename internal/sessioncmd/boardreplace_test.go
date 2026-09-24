package sessioncmd

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A replacement sits where the old session sat, inherits what was queued
// for it and what it held, and the old one is left dead with its screen.
func TestBoardReplaceTakesTheOldSessionsPlace(t *testing.T) {
	h := newSessionHarness(t)
	watcher := &launchWatcher{}
	useWatcher(t, watcher)
	old := childShowing(t, h, h.caller.ID, "child201", "worker", "lost the thread\n")
	if _, err := h.sessions.BoardSend("ext1", old.ID, "a report for whoever holds the seat", "", false); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := h.store.Reserve([]store.Reservation{{
		ID: "lease201", SessionID: old.ID, Pattern: "internal/*",
		Mode: store.ReservationExclusive, AcquiredAt: now, ExpiresAt: now.Add(time.Hour),
	}}); err != nil {
		t.Fatal(err)
	}

	fresh, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{Prompt: "start over"}, false)
	if err != nil {
		t.Fatalf("BoardReplace: %v", err)
	}
	if fresh.ID == old.ID || fresh.Name != "worker" || fresh.Tool != "echoer" || fresh.ParentID != h.caller.ID || fresh.Group != old.Group {
		t.Fatalf("fresh = %+v, want the old seat under a new id", fresh)
	}
	if h.driver.Exists(old.ID) {
		t.Fatal("the old pane outlived its replacement")
	}
	stored, err := h.store.Get(old.ID)
	if err != nil || stored.Status != status.Dead {
		t.Fatalf("old row = %+v, %v; want it kept, dead", stored, err)
	}
	if snap, _ := h.store.Snapshot(old.ID); !strings.Contains(snap, "lost the thread") {
		t.Fatalf("snapshot = %q, want the old pane's last screen", snap)
	}
	if counts, _ := h.store.QueuedCounts(); counts[fresh.ID] != 1 || counts[old.ID] != 0 {
		t.Fatalf("queued = %v, want the report forwarded", counts)
	}
	if leases, _ := h.store.Reservations(time.Now()); len(leases) != 1 || leases[0].SessionID != fresh.ID {
		t.Fatalf("leases = %+v, want the lease moved", leases)
	}
	if len(watcher.asked) != 1 || watcher.asked[0].By != extension.SpawnByExtension {
		t.Fatalf("asked = %+v, want the spawn policy asked once", watcher.asked)
	}
	if launch := watcher.launches[0]; launch.Reason != extension.LaunchReplace || launch.From != old.ID || launch.Session.ID != fresh.ID {
		t.Fatalf("launch = %+v, want a replacement from the old session", launch)
	}
	waitForSessionOutput(t, h.sessions, h.caller.ID, fresh.ID, "start over")
}

// A refused replacement leaves the old session running and nothing filed.
func TestBoardReplaceRefusalsLeaveTheOldSessionAlone(t *testing.T) {
	h := newSessionHarness(t)
	watcher := &launchWatcher{refuse: "over-budget"}
	useWatcher(t, watcher)
	old := childShowing(t, h, "", "child202", "worker", "working\n")
	if err := h.store.CreateSession(store.Session{ID: "theirs01", Name: "theirs", Tool: "echoer", Cwd: old.Cwd, Role: "other/helper"}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func() error{
		"cannot be filed elsewhere": func() error {
			_, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{ParentID: h.caller.ID}, false)
			return err
		},
		"another extension's role": func() error {
			_, err := h.sessions.BoardReplace("theirs01", "ext1/", BoardLaunchOptions{}, false)
			return err
		},
		"over the spawn budget": func() error {
			_, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{Name: "over-budget"}, false)
			return err
		},
		"is not configured": func() error {
			_, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{Tool: "nonesuch"}, false)
			return err
		},
	}
	for want, replace := range cases {
		if err := replace(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
	if !h.driver.Exists(old.ID) {
		t.Fatal("a refused replacement ended the old pane")
	}
	listed, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("listed %d rows, want only the caller, the old session and the other extension's", len(listed))
	}
}

// A plan of a replacement asks what the replacement would ask and files,
// starts, moves and takes nothing, and refuses what the replacement would.
func TestBoardPlanReplaceChangesNothing(t *testing.T) {
	h := newSessionHarness(t)
	watcher := &launchWatcher{env: map[string]string{"PLANNED": "yes"}}
	useWatcher(t, watcher)
	old := childShowing(t, h, h.caller.ID, "child203", "worker", "working\n")
	if _, err := h.sessions.BoardSend("ext1", old.ID, "still queued", "", false); err != nil {
		t.Fatal(err)
	}
	if err := h.store.CreateSession(store.Session{ID: "theirs02", Name: "theirs", Tool: "echoer", Cwd: old.Cwd, Role: "other/helper"}); err != nil {
		t.Fatal(err)
	}
	before, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := h.sessions.BoardPlanReplace(old.ID, "ext1/", BoardLaunchOptions{Prompt: "start over", Args: []string{"--one word"}})
	if err != nil {
		t.Fatalf("BoardPlanReplace: %v", err)
	}
	if plan.SessionID == "" || plan.SessionID == old.ID || plan.Env["GATE_INBOX_SESSION_ID"] != plan.SessionID || plan.Env["PLANNED"] != "yes" {
		t.Fatalf("plan = %+v, want a new id carried in its environment with the contributor's", plan)
	}
	if !strings.Contains(plan.Command, "start over") || !strings.Contains(plan.Command, "'--one word'") {
		t.Fatalf("plan command = %q, want the prompt and the quoted argument", plan.Command)
	}
	if launch := watcher.launches[len(watcher.launches)-1]; launch.Reason != extension.LaunchReplace || launch.From != old.ID {
		t.Fatalf("launch = %+v, want the contributor told of a replacement", launch)
	}
	after, err := h.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || h.driver.Exists(plan.SessionID) || !h.driver.Exists(old.ID) {
		t.Fatalf("rows %d -> %d, planned pane %v, old pane %v: want nothing started or ended",
			len(before), len(after), h.driver.Exists(plan.SessionID), h.driver.Exists(old.ID))
	}
	if counts, _ := h.store.QueuedCounts(); counts[old.ID] != 1 {
		t.Fatalf("queued = %v, want the old session's message left where it was", counts)
	}
	if borrower, _ := h.store.Setting("account_borrower:" + plan.SessionID); borrower != "" {
		t.Fatalf("a plan recorded a borrower %q", borrower)
	}
	if len(watcher.spawned) != 0 {
		t.Fatalf("spawned = %+v, want no spawn reported for a plan", watcher.spawned)
	}
	if _, err := h.sessions.BoardPlanReplace("theirs02", "ext1/", BoardLaunchOptions{}); err == nil || !strings.Contains(err.Error(), "another extension's role") {
		t.Fatalf("plan of another extension's session: %v", err)
	}
}

// A held replacement runs as a leaf under the old session, which keeps its
// pane and inbox; the commit then makes the same swap an unheld replace makes.
func TestBoardReplaceHeldThenCommitted(t *testing.T) {
	h := newSessionHarness(t)
	useWatcher(t, &launchWatcher{})
	old := childShowing(t, h, h.caller.ID, "child203", "worker", "lost the thread\n")
	if _, err := h.sessions.BoardSend("ext1", old.ID, "a report", "", false); err != nil {
		t.Fatal(err)
	}

	fresh, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{Prompt: "start over"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ParentID != old.ID || fresh.Name != "worker" {
		t.Fatalf("fresh = %+v, want it held under %s", fresh, old.ID)
	}
	if !h.driver.Exists(old.ID) || !h.driver.Exists(fresh.ID) {
		t.Fatal("both panes should run during the hold")
	}
	if counts, _ := h.store.QueuedCounts(); counts[old.ID] != 1 {
		t.Fatalf("queued = %v, want the report still the old session's", counts)
	}

	committed, err := h.sessions.BoardCommitReplace(old.ID, fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if committed.ID != fresh.ID || committed.ParentID != h.caller.ID || committed.Group != old.Group {
		t.Fatalf("committed = %+v, want the old seat", committed)
	}
	if h.driver.Exists(old.ID) || !h.driver.Exists(fresh.ID) {
		t.Fatal("the commit should end the old pane and keep the fresh one")
	}
	if snap, _ := h.store.Snapshot(old.ID); !strings.Contains(snap, "lost the thread") {
		t.Fatalf("snapshot = %q, want the old pane's last screen", snap)
	}
	if retired, err := h.store.Get(old.ID); err != nil || retired.ReplacedBy != fresh.ID {
		t.Fatalf("old = %+v, %v; want it marked replaced by %s", retired, err, fresh.ID)
	}
	// Every Session the commands build carries it: a board read by id, and
	// both list orders, the parent's with terminals beside the agents.
	if got, err := h.sessions.BoardGet(old.ID); err != nil || got.ReplacedBy != fresh.ID {
		t.Fatalf("BoardGet = %+v, %v; want ReplacedBy %s", got, err, fresh.ID)
	}
	for _, opts := range []ListOptions{{}, {Parent: SelfParent, IncludeTerminals: true}} {
		listed, err := h.sessions.List(h.caller.ID, opts)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, sess := range listed.Sessions {
			if sess.ID == old.ID {
				found = sess.ReplacedBy == fresh.ID
			}
		}
		if !found {
			t.Fatalf("List(%+v) = %+v, want %s listed as replaced by %s", opts, listed.Sessions, old.ID, fresh.ID)
		}
	}
	if counts, _ := h.store.QueuedCounts(); counts[fresh.ID] != 1 || counts[old.ID] != 0 {
		t.Fatalf("queued = %v, want the report forwarded at the commit", counts)
	}
	if err := h.sessions.BoardAbortReplace(old.ID, fresh.ID); !errors.Is(err, store.ErrNotHeld) {
		t.Fatalf("abort after the commit = %v, want ErrNotHeld", err)
	}
	if !h.driver.Exists(fresh.ID) {
		t.Fatal("an abort after the commit ended the session in the seat")
	}
}

// An aborted hold leaves nothing but the old session, as it was.
func TestBoardReplaceHeldThenAborted(t *testing.T) {
	h := newSessionHarness(t)
	useWatcher(t, &launchWatcher{})
	old := childShowing(t, h, h.caller.ID, "child204", "worker", "working\n")
	if _, err := h.sessions.BoardSend("ext1", old.ID, "a report", "", false); err != nil {
		t.Fatal(err)
	}
	fresh, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.sessions.BoardAbortReplace(old.ID, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if h.driver.Exists(fresh.ID) {
		t.Fatal("the aborted replacement's pane still runs")
	}
	if _, err := h.store.Get(fresh.ID); err == nil {
		t.Fatal("the aborted replacement's row was kept")
	}
	if !h.driver.Exists(old.ID) {
		t.Fatal("the abort ended the old pane")
	}
	stored, err := h.store.Get(old.ID)
	if err != nil || stored.Status == status.Dead || stored.ParentID != h.caller.ID || stored.ReplacedBy != "" {
		t.Fatalf("old = %+v, %v; want it untouched", stored, err)
	}
	if counts, _ := h.store.QueuedCounts(); counts[old.ID] != 1 {
		t.Fatalf("queued = %v, want the report still the old session's", counts)
	}
	if _, err := h.sessions.BoardCommitReplace(old.ID, fresh.ID); err == nil {
		t.Fatal("a commit after the abort succeeded")
	}
}

// A commit the store took is reported as one even when the fresh row
// cannot be read back afterwards: an error there would read as a refused
// commit while the fresh session already holds the seat.
func TestBoardCommitReplaceSucceedsWhenTheReadBackFails(t *testing.T) {
	h := newSessionHarness(t)
	useWatcher(t, &launchWatcher{})
	old := childShowing(t, h, h.caller.ID, "child205", "worker", "working\n")
	fresh, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	// A row the commit moves but cannot decode, so only the read after it fails.
	db, err := sql.Open("sqlite", filepath.Join(h.sessions.configDir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE sessions SET pending_inputs = 'not json' WHERE id = ?`, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Get(fresh.ID); err == nil {
		t.Fatal("the fresh row still reads back; the test would not reach the failing read")
	}

	committed, err := h.sessions.BoardCommitReplace(old.ID, fresh.ID)
	if err != nil {
		t.Fatalf("commit = %v, want success once the swap is committed", err)
	}
	if committed.ID != fresh.ID || committed.ParentID != h.caller.ID || committed.Group != old.Group {
		t.Fatalf("committed = %+v, want the old seat", committed)
	}
	if h.driver.Exists(old.ID) || !h.driver.Exists(fresh.ID) {
		t.Fatal("the commit should end the old pane and keep the fresh one")
	}
	if retired, err := h.store.Get(old.ID); err != nil || retired.Status != status.Dead || retired.ReplacedBy != fresh.ID {
		t.Fatalf("old = %+v, %v; want it dead and marked replaced by %s", retired, err, fresh.ID)
	}
	if err := h.sessions.BoardAbortReplace(old.ID, fresh.ID); err == nil {
		t.Fatal("an abort after the commit succeeded")
	}
}

// A commit after the fresh session was killed during the hold is refused,
// and the old session keeps running in its seat.
func TestBoardCommitReplaceRefusesAKilledReplacement(t *testing.T) {
	h := newSessionHarness(t)
	useWatcher(t, &launchWatcher{})
	old := childShowing(t, h, h.caller.ID, "child301", "worker", "working\n")
	fresh, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.BoardKill(fresh.ID); err != nil {
		t.Fatalf("BoardKill fresh: %v", err)
	}
	if _, err := h.sessions.BoardCommitReplace(old.ID, fresh.ID); !errors.Is(err, extension.ErrReplacementNotRunning) {
		t.Fatalf("commit of a killed replacement = %v, want ErrReplacementNotRunning", err)
	}
	stored, err := h.store.Get(old.ID)
	if !h.driver.Exists(old.ID) || err != nil || stored.Status == status.Dead || stored.ParentID != h.caller.ID {
		t.Fatalf("old = %+v, %v, pane %v; want it running in its seat", stored, err, h.driver.Exists(old.ID))
	}
	// The hold is still the extension's to abort.
	if err := h.sessions.BoardAbortReplace(old.ID, fresh.ID); err != nil {
		t.Fatalf("abort after the refused commit: %v", err)
	}
}

// A commit after the fresh session's pane ended on its own -- the agent
// crashed, and no pass has marked the row dead yet -- is refused too.
func TestBoardCommitReplaceRefusesAReplacementWhosePaneDied(t *testing.T) {
	h := newSessionHarness(t)
	useWatcher(t, &launchWatcher{})
	old := childShowing(t, h, h.caller.ID, "child302", "worker", "working\n")
	fresh, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = h.driver.Kill(fresh.ID)
	if _, err := h.sessions.BoardCommitReplace(old.ID, fresh.ID); !errors.Is(err, extension.ErrReplacementNotRunning) {
		t.Fatalf("commit with the fresh pane gone = %v, want ErrReplacementNotRunning", err)
	}
	if !h.driver.Exists(old.ID) {
		t.Fatal("the old worker's pane ended for a replacement whose pane is gone")
	}
	if stored, _ := h.store.Get(old.ID); stored.Status == status.Dead {
		t.Fatal("the old worker was retired for a replacement whose pane is gone")
	}
}

// Holds a killed board never settled are aborted at the next start: the
// fresh pane is ended and its row deleted, the old session is untouched,
// and a record whose fresh session never got a row is dropped too.
func TestAbortOrphanedHoldsEndsTheFreshSessionsOnly(t *testing.T) {
	h := newSessionHarness(t)
	useWatcher(t, &launchWatcher{})
	old := childShowing(t, h, h.caller.ID, "child303", "worker", "working\n")
	fresh, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.RecordHold(store.HeldReplacement{FreshID: "norow001", OldID: old.ID, Owner: "ext1"}); err != nil {
		t.Fatal(err)
	}
	holds, err := h.store.HeldReplacements()
	if err != nil || len(holds) != 2 || holds[0].FreshID != fresh.ID || holds[0].Owner != "ext1" {
		t.Fatalf("holds = %+v, %v; want the held replacement recorded with its extension", holds, err)
	}

	aborted, err := h.sessions.AbortOrphanedHolds()
	if err != nil || aborted != 2 {
		t.Fatalf("AbortOrphanedHolds = %d, %v; want both aborted", aborted, err)
	}
	if h.driver.Exists(fresh.ID) {
		t.Fatal("the orphaned replacement's pane still runs")
	}
	if _, err := h.store.Get(fresh.ID); err == nil {
		t.Fatal("the orphaned replacement's row was kept")
	}
	stored, err := h.store.Get(old.ID)
	if !h.driver.Exists(old.ID) || err != nil || stored.Status == status.Dead || stored.ParentID != h.caller.ID {
		t.Fatalf("old = %+v, %v; want it untouched", stored, err)
	}
	if holds, err := h.store.HeldReplacements(); err != nil || len(holds) != 0 {
		t.Fatalf("holds = %+v, %v; want none left", holds, err)
	}
}

// Committing or aborting a hold clears its record, so a later start
// aborts nothing.
func TestSettledHoldsLeaveNoRecord(t *testing.T) {
	h := newSessionHarness(t)
	useWatcher(t, &launchWatcher{})
	old := childShowing(t, h, h.caller.ID, "child304", "worker", "working\n")
	trial, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.sessions.BoardAbortReplace(old.ID, trial.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := h.sessions.BoardReplace(old.ID, "ext1/", BoardLaunchOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.sessions.BoardCommitReplace(old.ID, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if aborted, err := h.sessions.AbortOrphanedHolds(); err != nil || aborted != 0 {
		t.Fatalf("AbortOrphanedHolds = %d, %v; want nothing left to abort", aborted, err)
	}
	if !h.driver.Exists(fresh.ID) {
		t.Fatal("the committed replacement's pane was ended")
	}
}
