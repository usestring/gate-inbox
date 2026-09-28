package ui

import (
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A drain that runs out of work leaves the queue open: a session reaching
// finished on a later poll joins the same queue instead of waiting for
// another explicit pass.
func TestTriagePicksUpFinishedArrivingAfterDrain(t *testing.T) {
	m := buildModel(t)
	first := liveHookedSession(t, m, "first")
	later := liveHookedSession(t, m, "later")
	writeHookStatus(t, m, first.ID, status.Finished)
	writeHookStatus(t, m, later.ID, status.Working)
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "first", status.Finished)
	requireStatus(t, m, "later", status.Working)
	// One more pass so the rows the drain mutes carry the stamps the store
	// wrote: the pass that reports a transition delivers the new status
	// with the previous stamp, and a mute taken from it lapses on the next
	// poll. See triagedrain_test.go.
	m.applyCmd(t, m.refreshCmd())

	m.applyCmd(t, m.toggleTriage())
	if m.mode != modeFocus || focusedName(t, m) != "first" {
		t.Fatalf("triage opened in mode %v on %q, want focus on first", m.mode, focusedName(t, m))
	}

	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("draining the only waiting session left mode %v, want the list", m.mode)
	}
	if !m.triageResume {
		t.Fatal("the drained queue did not stay open for late arrivals")
	}

	writeHookStatus(t, m, later.ID, status.Finished)
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "later", status.Finished)
	if m.mode != modeFocus || focusedName(t, m) != "later" {
		t.Fatalf("the late finish landed in mode %v on %q, want focus on later", m.mode, focusedName(t, m))
	}
	if m.triageResume {
		t.Fatal("the pickup did not close the open drain")
	}
}

// Leaving the drain outright ends it: a finish arriving after ctrl+\ stays
// on the list rather than pulling the operator back in.
func TestTriagePickupStandsDownAfterLeavingTheDrain(t *testing.T) {
	m := buildModel(t)
	first := liveHookedSession(t, m, "first")
	later := liveHookedSession(t, m, "later")
	writeHookStatus(t, m, first.ID, status.Finished)
	writeHookStatus(t, m, later.ID, status.Working)
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "first", status.Finished)
	requireStatus(t, m, "later", status.Working)
	// Settled for the reason the first pickup test names: a mute taken
	// from a freshly transitioned row lapses on the next poll.
	m.applyCmd(t, m.refreshCmd())

	m.applyCmd(t, m.toggleTriage())
	updated, _ := m.handleFocusKey(ctrlQ())
	m = updated.(*Model)
	if m.mode != modeList || !m.triageResume {
		t.Fatalf("draining left mode %v resume %v, want list with the drain open", m.mode, m.triageResume)
	}

	m.enterFocusOn(t, "first")
	updated, _ = m.handleFocusKey(ctrlBackslash())
	m = updated.(*Model)
	if m.mode != modeList {
		t.Fatalf("leaving the drain left mode %v, want the list", m.mode)
	}
	if m.triageResume {
		t.Fatal("leaving the drain left it open for late arrivals")
	}

	writeHookStatus(t, m, later.ID, status.Finished)
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "later", status.Finished)
	if m.mode != modeList {
		t.Fatalf("a finish after leaving the drain landed in mode %v, want the list", m.mode)
	}
}

// The pickup is for sessions needing a person only: a newly idle session
// waits for the next explicit pass rather than pulling the operator in.
func TestTriagePickupIgnoresIdleSessions(t *testing.T) {
	m := buildModel(t)
	base := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	for _, seed := range []store.Session{
		{ID: "busy", Name: "busy", Tool: "claude", Cwd: "/tmp", Status: status.Working, CreatedAt: base, LastStatusAt: base},
		{ID: "calm", Name: "calm", Tool: "claude", Cwd: "/tmp", Status: status.Idle, CreatedAt: base, LastStatusAt: base},
	} {
		if err := m.store.CreateSession(seed); err != nil {
			t.Fatalf("create session %q: %v", seed.ID, err)
		}
	}
	loadStoredRows(t, m)
	m.triage, m.triageResume = true, true
	m.rebuildRows()

	if cmd := m.triagePickupCmd(); cmd != nil {
		t.Fatal("an idle-only board offered a pickup")
	}
	if m.mode != modeList || !m.triageResume {
		t.Fatalf("the idle check moved mode %v resume %v, want the list with the drain still open", m.mode, m.triageResume)
	}
}
