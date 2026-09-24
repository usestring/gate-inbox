package store

import (
	"errors"
	"testing"
	"time"
)

func replaceFixture(t *testing.T) *Store {
	t.Helper()
	st := newTestStore(t)
	for _, sess := range []Session{
		{ID: "run1", Name: "run", Tool: "t", Cwd: "/", Group: "work"},
		{ID: "target01", Name: "worker", Tool: "t", Cwd: "/", ParentID: "run1", Role: "ext1/worker"},
		{ID: "after01", Name: "sibling", Tool: "t", Cwd: "/", ParentID: "run1"},
	} {
		if err := st.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if _, _, err := st.Enqueue(message("the report", now), DefaultInboxLimits); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Reserve([]Reservation{{
		ID: "lease001", SessionID: "target01", Pattern: "internal/*",
		Mode: ReservationExclusive, AcquiredAt: now, ExpiresAt: now.Add(time.Hour),
	}}); err != nil {
		t.Fatal(err)
	}
	return st
}

// The replacement takes the old row's seat, inbox and leases, and the old
// row is left dead with its last screen.
func TestReplaceSessionTakesTheOldSeat(t *testing.T) {
	st := replaceFixture(t)
	moved, err := st.ReplaceSession(Session{ID: "fresh01", Name: "worker", Tool: "t", Cwd: "/", Status: "starting", Role: "ext1/worker"},
		"target01", "last screen", func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if moved.Forwarded != 1 || moved.Reservations != 1 {
		t.Fatalf("moved = %+v, want the one message and the one lease", moved)
	}
	fresh, err := st.Get("fresh01")
	if err != nil || fresh.ParentID != "run1" || fresh.Group != "work" || fresh.Role != "ext1/worker" {
		t.Fatalf("fresh = %+v, %v; want it under the old parent, in its group", fresh, err)
	}
	old, err := st.Get("target01")
	if err != nil || old.Status != "dead" {
		t.Fatalf("old = %+v, %v; want it kept, dead", old, err)
	}
	if snap, err := st.Snapshot("target01"); err != nil || snap != "last screen" {
		t.Fatalf("snapshot = %q, %v", snap, err)
	}
	if counts, err := st.QueuedCounts(); err != nil || counts["fresh01"] != 1 || counts["target01"] != 0 {
		t.Fatalf("queued = %v, %v; want the message moved", counts, err)
	}
	leases, err := st.Reservations(time.Now())
	if err != nil || len(leases) != 1 || leases[0].SessionID != "fresh01" {
		t.Fatalf("leases = %+v, %v; want the lease moved", leases, err)
	}
	listed, err := st.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, sess := range listed {
		if sess.ParentID == "run1" {
			order = append(order, sess.ID)
		}
	}
	if len(order) != 3 || order[0] != "target01" || order[1] != "fresh01" || order[2] != "after01" {
		t.Fatalf("children in order %v, want the replacement straight after the old row", order)
	}
}

// A launch that fails leaves nothing moved and the old row as it was.
func TestReplaceSessionRollsBackAFailedLaunch(t *testing.T) {
	st := replaceFixture(t)
	boom := errors.New("pane would not start")
	if _, err := st.ReplaceSession(Session{ID: "fresh01", Name: "worker", Tool: "t", Cwd: "/"}, "target01", "last screen",
		func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("ReplaceSession = %v, want the launch's error", err)
	}
	if _, err := st.Get("fresh01"); err == nil {
		t.Fatal("the failed replacement's row was kept")
	}
	old, err := st.Get("target01")
	if err != nil || old.Status == "dead" {
		t.Fatalf("old = %+v, %v; want it untouched", old, err)
	}
	if counts, _ := st.QueuedCounts(); counts["target01"] != 1 {
		t.Fatalf("queued = %v, want the message still the old row's", counts)
	}
	if leases, _ := st.Reservations(time.Now()); len(leases) != 1 || leases[0].SessionID != "target01" {
		t.Fatalf("leases = %+v, want the lease still the old row's", leases)
	}
	if _, err := st.ReplaceSession(Session{ID: "fresh02"}, "missing1", "", nil); err == nil {
		t.Fatal("replacing a missing row succeeded")
	}
}

// A held replacement is a leaf under the old row until it commits, and then
// takes the seat, inbox and leases exactly as an unheld one would have.
func TestCommitReplacementTakesTheOldSeat(t *testing.T) {
	st := replaceFixture(t)
	if err := st.CreateSessionLeaf(Session{ID: "fresh01", Name: "worker", Tool: "t", Cwd: "/", ParentID: "target01", SpawnedBy: "run1", Role: "ext1/worker"}); err != nil {
		t.Fatal(err)
	}
	if counts, _ := st.QueuedCounts(); counts["target01"] != 1 {
		t.Fatalf("queued = %v, want the message still the old row's during the hold", counts)
	}
	retired := false
	moved, err := st.CommitReplacement("fresh01", "target01", "last screen", nil, func() error { retired = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !retired || moved.Forwarded != 1 || moved.Reservations != 1 {
		t.Fatalf("retired %v, moved %+v; want the old pane ended and both moved", retired, moved)
	}
	fresh, err := st.Get("fresh01")
	if err != nil || fresh.ParentID != "run1" || fresh.Group != "work" || fresh.SpawnedBy != "run1" {
		t.Fatalf("fresh = %+v, %v; want it in the old seat", fresh, err)
	}
	if old, _ := st.Get("target01"); old.Status != "dead" {
		t.Fatalf("old = %+v, want it dead", old)
	}
	if snap, _ := st.Snapshot("target01"); snap != "last screen" {
		t.Fatalf("snapshot = %q", snap)
	}
	listed, err := st.ListSessions(false)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, sess := range listed {
		if sess.ParentID == "run1" {
			order = append(order, sess.ID)
		}
	}
	if len(order) != 3 || order[0] != "target01" || order[1] != "fresh01" || order[2] != "after01" {
		t.Fatalf("children in order %v, want the replacement straight after the old row", order)
	}
	if _, err := st.CommitReplacement("fresh01", "target01", "", nil, nil); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("a second commit = %v, want ErrNotHeld", err)
	}
}

// A commit whose retire fails, or whose new row has left the hold, changes
// nothing.
func TestCommitReplacementRollsBack(t *testing.T) {
	st := replaceFixture(t)
	if err := st.CreateSessionLeaf(Session{ID: "fresh01", Name: "worker", Tool: "t", Cwd: "/", ParentID: "target01"}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("old pane would not end")
	if _, err := st.CommitReplacement("fresh01", "target01", "", nil, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("CommitReplacement = %v, want the retire's error", err)
	}
	if fresh, _ := st.Get("fresh01"); fresh.ParentID != "target01" {
		t.Fatalf("fresh = %+v, want it still held", fresh)
	}
	if old, _ := st.Get("target01"); old.Status == "dead" {
		t.Fatal("a failed commit retired the old row")
	}
	if counts, _ := st.QueuedCounts(); counts["target01"] != 1 {
		t.Fatalf("queued = %v, want the message still the old row's", counts)
	}
	if _, err := st.CommitReplacement("after01", "target01", "", nil, nil); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("committing a row never held = %v, want ErrNotHeld", err)
	}
}

// A commit whose new row is dead, or whose pane the caller reports gone, is
// refused inside the transaction and retires nothing.
func TestCommitReplacementRefusesAReplacementNotRunning(t *testing.T) {
	st := replaceFixture(t)
	if err := st.CreateSessionLeaf(Session{ID: "fresh01", Name: "worker", Tool: "t", Cwd: "/", ParentID: "target01", Status: "starting"}); err != nil {
		t.Fatal(err)
	}
	retired := false
	retire := func() error { retired = true; return nil }
	if _, err := st.CommitReplacement("fresh01", "target01", "", func() bool { return false }, retire); !errors.Is(err, ErrReplacementNotRunning) {
		t.Fatalf("commit with the pane gone = %v, want ErrReplacementNotRunning", err)
	}
	if err := st.UpdateStatus("fresh01", "dead"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CommitReplacement("fresh01", "target01", "", func() bool { return true }, retire); !errors.Is(err, ErrReplacementNotRunning) {
		t.Fatalf("commit of a dead row = %v, want ErrReplacementNotRunning", err)
	}
	if retired {
		t.Fatal("a refused commit ended the old pane")
	}
	if old, _ := st.Get("target01"); old.Status == "dead" {
		t.Fatal("a refused commit retired the old row")
	}
	if fresh, _ := st.Get("fresh01"); fresh.ParentID != "target01" {
		t.Fatalf("fresh = %+v, want it still held", fresh)
	}
	if counts, _ := st.QueuedCounts(); counts["target01"] != 1 {
		t.Fatalf("queued = %v, want the message still the old row's", counts)
	}
}

// A hold's record lasts until the commit takes the seat or the fresh row is
// deleted, and ClearHold drops one whose launch never filed a row.
func TestHoldRecordLastsUntilTheHoldSettles(t *testing.T) {
	st := replaceFixture(t)
	for _, id := range []string{"fresh01", "fresh02", "fresh03"} {
		if err := st.RecordHold(HeldReplacement{FreshID: id, OldID: "target01", Owner: "ext1"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"fresh01", "fresh02"} {
		if err := st.CreateSessionLeaf(Session{ID: id, Name: "worker", Tool: "t", Cwd: "/", ParentID: "target01", Status: "starting"}); err != nil {
			t.Fatal(err)
		}
	}
	holds, err := st.HeldReplacements()
	if err != nil || len(holds) != 3 || holds[0].FreshID != "fresh01" || holds[0].OldID != "target01" || holds[0].Owner != "ext1" || holds[0].Since.IsZero() {
		t.Fatalf("holds = %+v, %v; want the three recorded", holds, err)
	}
	if _, err := st.CommitReplacement("fresh01", "target01", "", func() bool { return true }, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete("fresh02"); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearHold("fresh03"); err != nil {
		t.Fatal(err)
	}
	if holds, err := st.HeldReplacements(); err != nil || len(holds) != 0 {
		t.Fatalf("holds = %+v, %v; want none once each is settled", holds, err)
	}
}
