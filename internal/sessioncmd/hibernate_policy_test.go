package sessioncmd

import (
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
)

func TestAdaptiveHibernatePreservesWorkAndStopsOnlyOldestExcess(t *testing.T) {
	h := newSessionHarness(t)
	old := restingRow(t, h, "old00001")
	young := restingRow(t, h, "young001")
	fresh := restingRow(t, h, "fresh001")
	busy := restingRow(t, h, "busy0001")
	mail := restingRow(t, h, "mail0001")
	now := time.Now()
	for i, sess := range []store.Session{mail, busy, old, young} {
		state := status.Finished
		if sess.ID == busy.ID {
			state = status.Working
		}
		if err := h.store.ApplyDerivedStates(now.Add(-time.Duration(8-i)*time.Hour),
			[]store.DerivedState{{ID: sess.ID, Status: state}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := h.store.Enqueue(store.InboxMessage{SessionID: mail.ID, SenderID: h.caller.ID, Body: "pending work"}, store.DefaultInboxLimits); err != nil {
		t.Fatal(err)
	}
	memory := sysstat.Snapshot{MemOK: true, MemTotal: 10 << 30, MemPercent: 50}
	for _, dryRun := range []bool{true, false} {
		result, err := h.sessions.HibernateAdaptive("balanced", memory, dryRun)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Errors) != 0 || len(result.Hibernated) != 1 || result.Hibernated[0].ID != old.ID {
			t.Fatalf("dryRun=%v: %+v; want only the oldest eligible row", dryRun, result)
		}
		if h.driver.Exists(old.ID) != dryRun {
			t.Fatalf("dryRun=%v: oldest live=%v", dryRun, h.driver.Exists(old.ID))
		}
		for _, sess := range []store.Session{young, fresh, busy, mail} {
			if !h.driver.Exists(sess.ID) {
				t.Fatalf("stopped protected or within-budget row %s", sess.ID)
			}
		}
	}
	if !slices.Contains(hibernated(t, h), old.ID) {
		t.Fatal("stopped session lost its wake marker")
	}
	result, err := h.sessions.HibernateAdaptive("balanced", memory, false)
	if err != nil || len(result.Hibernated) != 0 {
		t.Fatalf("sweep continued below budget: %+v, %v", result, err)
	}
}

func TestAdaptiveHibernateRequiresEnabledPolicyAndMemoryReading(t *testing.T) {
	h := newSessionHarness(t)
	rest := restingRow(t, h, "rest0001")
	if err := h.store.ApplyDerivedStates(time.Now().Add(-time.Hour), []store.DerivedState{{ID: rest.ID, Status: status.Finished}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sensitivity string
		memory      sysstat.Snapshot
	}{
		{"off", sysstat.Snapshot{MemOK: true, MemTotal: 1 << 30, MemPercent: 99}},
		{"balanced", sysstat.Snapshot{}},
		{"balanced", sysstat.Snapshot{MemOK: true, MemPercent: 99}},
	} {
		result, err := h.sessions.HibernateAdaptive(tc.sensitivity, tc.memory, false)
		if err != nil || len(result.Hibernated) != 0 || !h.driver.Exists(rest.ID) {
			t.Fatalf("disabled or unreadable policy stopped work: %+v, %v", result, err)
		}
	}
}

func TestHibernateBudgetRespondsToMemoryAndSensitivity(t *testing.T) {
	memory := sysstat.Snapshot{MemOK: true, MemTotal: 16 << 30, MemPercent: 50}
	low := hibernateBudget("low", memory, 20)
	balanced := hibernateBudget("", memory, 20)
	high := hibernateBudget("high", memory, 20)
	if !(low > balanced && balanced > high) {
		t.Fatalf("budgets low=%d balanced=%d high=%d", low, balanced, high)
	}
	memory.MemTotal *= 2
	if hibernateBudget("balanced", memory, 20) <= balanced {
		t.Fatal("more RAM did not increase the budget")
	}
	memory.MemPercent = 99
	if hibernateBudget("balanced", memory, 4) >= 4 {
		t.Fatal("memory pressure did not reclaim an idle slot below the normal budget")
	}
}
