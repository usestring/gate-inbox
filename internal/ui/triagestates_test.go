package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Working is the bottom of the queue in both orders: every row the drain can
// hand over -- a failed turn, a background or delegating finish -- reads
// above it on the rail.
func TestWorkingSortsBelowEveryWalkableRow(t *testing.T) {
	for _, smart := range []bool{false, true} {
		m := buildModel(t)
		m.smartTriage = smart
		sessions := []store.Session{
			{ID: "busy", Status: status.Working, LastStatusAt: m.now().Add(-3 * time.Hour)},
			{ID: "broke", Status: status.Errored, LastStatusAt: m.now().Add(-time.Minute)},
			{ID: "shell", Status: status.Background, LastStatusAt: m.now().Add(-time.Minute)},
			{ID: "fanout", Status: status.Delegating, LastStatusAt: m.now().Add(-time.Minute)},
			{ID: "done", Status: status.Finished, LastStatusAt: m.now().Add(-time.Minute)},
		}
		m.sortTriage(sessions)
		var ids []string
		for _, s := range sessions {
			ids = append(ids, s.ID)
		}
		if ids[len(ids)-1] != "busy" {
			t.Fatalf("smart=%v: order %v, want working last", smart, ids)
		}
		if slices.Index(ids, "shell") > slices.Index(ids, "broke") || slices.Index(ids, "fanout") > slices.Index(ids, "broke") {
			t.Fatalf("smart=%v: order %v, want ended turns above the failed one", smart, ids)
		}
	}
}

// A drain left open picks up a turn that ends with a shell still running,
// just as it picks up a plain finish.
func TestTriagePicksUpABackgroundFinish(t *testing.T) {
	m := buildModel(t)
	first := liveHookedSession(t, m, "first")
	later := liveHookedSession(t, m, "later")
	writeHookStatus(t, m, first.ID, status.Finished)
	writeHookStatus(t, m, later.ID, status.Working)
	m.applyCmd(t, m.refreshCmd())
	m.applyCmd(t, m.refreshCmd())
	m.applyCmd(t, m.toggleTriage())
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode != modeList || !m.triageResume {
		t.Fatalf("draining left mode %v resume %v, want the list with the drain open", m.mode, m.triageResume)
	}

	if err := m.store.UpdateStatus(later.ID, status.Background); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	m.rebuildRows()
	if cmd := m.triagePickupCmd(); cmd == nil {
		t.Fatal("the open drain did not pick up a background finish")
	}
}

// Seeing a background finish acknowledges it without pretending its shell
// stopped, and the ack holds while the shell runs.
func TestAcknowledgingABackgroundFinishKeepsItsStatus(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "bg000001", Name: "bg", Tool: "claude", Cwd: "/tmp", Status: status.Background}
	if err := m.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := m.store.AcknowledgeFinished(sess.ID); err != nil {
		t.Fatal(err)
	}
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != status.Background || !got.Acked {
		t.Fatalf("after the ack: status %q acked %v, want background and acked", got.Status, got.Acked)
	}
	if m.triageNews(got) {
		t.Fatal("a seen background finish is still news to the open drain")
	}
	if st := m.triageScoredState(got); st != triageFinishedSeen {
		t.Fatalf("seen background scores %q, want a seen finish", st)
	}
}

// A working parent cannot be entered, so a child's finish must not hoist it
// over the rows that can; a child's question still does.
func TestAWorkingParentIsLiftedOnlyByAQuestion(t *testing.T) {
	for _, tc := range []struct {
		child string
		want  string
	}{
		{status.Finished, "e1"},
		{status.Waiting, "p1"},
	} {
		m := buildModel(t)
		m.sessions = []store.Session{
			childSess("p1", "worker", "", "", status.Working, time.Hour),
			childSess("c1", "probe", "", "p1", tc.child, time.Minute),
			childSess("e1", "broken", "", "", status.Errored, 3*time.Hour),
		}
		m.triage = true
		m.rebuildRows()
		if ids := rowIDs(m); len(ids) == 0 || ids[0] != tc.want {
			t.Fatalf("child %s: rows %v, want %s first", tc.child, ids, tc.want)
		}
	}
}

// Skipping a background finish in a drain marks it seen, the way skipping a
// plain finish does, so the next drain does not hand it back as news.
func TestSkippingABackgroundFinishAcknowledgesIt(t *testing.T) {
	m := buildModel(t)
	sess := store.Session{ID: "bg000002", Name: "bg", Tool: "claude", Cwd: "/tmp", Status: status.Background}
	if err := m.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)
	m.triage = true
	m.rebuildRows()
	m.selectSessionRow(t, "bg")
	m.skipSelected()
	got, err := m.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != status.Background || !got.Acked {
		t.Fatalf("after the skip: status %q acked %v, want background and acked", got.Status, got.Acked)
	}
}
