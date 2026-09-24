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
