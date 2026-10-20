package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
)

// skipFleet is a drain with one session the operator has waited on for a week
// and skipped, and a question that arrived a minute ago, on a clock the test
// moves by hand.
func skipFleet(t *testing.T, skip time.Duration) (*Model, *time.Time) {
	t.Helper()
	m := smartFleet(t,
		smartSess("old", status.Waiting, 7*24*time.Hour),
		smartSess("new", status.Waiting, time.Minute),
	)
	now := smartNow
	m.clock = func() time.Time { return now }
	m.triageSkip = skip
	m.mute(sessionNamed(t, m, "old"))
	return m, &now
}

// The bug: once every old session had been handed over once, a drain left
// open offered only new arrivals until triage was toggled.
func TestASkippedSessionRejoinsTheQueueAfterTheSkipLapses(t *testing.T) {
	m, now := skipFleet(t, triageSkipDefault*time.Minute)
	if got := drainOrder(m); !slices.Equal(got, []string{"new"}) {
		t.Fatalf("drain right after the skip = %v, want only new", got)
	}
	*now = smartNow.Add(15*time.Minute - time.Second)
	m.rebuildRows()
	if got := drainOrder(m); !slices.Equal(got, []string{"new"}) {
		t.Fatalf("drain before the skip lapsed = %v, want only new", got)
	}
	*now = smartNow.Add(15 * time.Minute)
	m.rebuildRows()
	if got := drainOrder(m); !slices.Equal(got, []string{"old", "new"}) {
		t.Fatalf("drain after the skip lapsed = %v, want old ahead of new", got)
	}
	if _, ok := m.muted["old"]; ok {
		t.Fatal("a lapsed skip was not dropped")
	}
}

func TestATriageSkipOfZeroNeverLapses(t *testing.T) {
	m, now := skipFleet(t, 0)
	*now = smartNow.Add(30 * 24 * time.Hour)
	m.rebuildRows()
	if got := drainOrder(m); !slices.Equal(got, []string{"new"}) {
		t.Fatalf("drain a month on with skips set never to lapse = %v, want only new", got)
	}
}

// The mute after an answer lands is the next poll's to lift, not the clock's.
func TestTheSkipTimeoutLeavesAJustAnsweredMuteAlone(t *testing.T) {
	m, now := skipFleet(t, triageSkipDefault*time.Minute)
	m.unmute("old")
	m.statusesAsOf = smartNow
	m.muteUntilSeen(sessionNamed(t, m, "old"), smartNow.Add(time.Second))
	*now = smartNow.Add(time.Hour)
	m.rebuildRows()
	if !m.isMuted(sessionNamed(t, m, "old")) {
		t.Fatal("the skip timeout lifted a mute still waiting on the poll after its answer")
	}
}

// Leaving a finished session acks it to idle under the same LastStatusAt; the
// mark carries over that, and lapses on the same clock as any other skip.
func TestASeenFinishLapsesWithTheSkip(t *testing.T) {
	m, now := skipFleet(t, triageSkipDefault*time.Minute)
	done := sessionNamed(t, m, "old")
	done.Status = status.Finished
	m.mute(done)
	done.Status, done.Acked = status.Idle, true
	if !m.isMuted(done) {
		t.Fatal("a seen finish was handed straight back")
	}
	*now = smartNow.Add(15 * time.Minute)
	if m.isMuted(done) {
		t.Fatal("a seen finish stayed muted past the skip")
	}
}

// A drain that ran out picks a lapsed skip back up, but only for a session
// that needs a person, the same rule it applies to any arrival.
func TestTriagePickupTakesALapsedSkipThatNeedsAPerson(t *testing.T) {
	for _, tc := range []struct {
		state  string
		pickUp bool
	}{{status.Waiting, true}, {status.Idle, false}} {
		t.Run(tc.state, func(t *testing.T) {
			m := buildModel(t)
			now := time.Now()
			m.clock = func() time.Time { return now }
			m.triageSkip = triageSkipDefault * time.Minute
			only := liveHookedSession(t, m, "only")
			writeHookStatus(t, m, only.ID, tc.state)
			m.applyCmd(t, m.refreshCmd())
			m.applyCmd(t, m.refreshCmd())
			requireStatus(t, m, "only", tc.state)

			m.applyCmd(t, m.toggleTriage())
			if m.mode != modeFocus {
				t.Fatalf("triage opened in mode %v, want focus on only", m.mode)
			}
			updated, _ := m.handleFocusKey(ctrlQ())
			m = updated.(*Model)
			if m.mode != modeList || !m.triageResume {
				t.Fatalf("leaving the only session left mode %v resume %v, want an open drain on the list", m.mode, m.triageResume)
			}

			now = now.Add(15*time.Minute - time.Second)
			m.applyCmd(t, m.refreshCmd())
			if m.mode != modeList {
				t.Fatalf("a skip picked back up before it lapsed, mode %v", m.mode)
			}
			now = now.Add(time.Second)
			m.applyCmd(t, m.refreshCmd())
			if got := m.mode == modeFocus; got != tc.pickUp {
				t.Fatalf("after the skip lapsed mode = %v, want picked up %v", m.mode, tc.pickUp)
			}
		})
	}
}

func TestTriageSkipSettingDefaultsToFifteenMinutes(t *testing.T) {
	m := buildModel(t)
	if got := storedTriageSkip(m.store); got != triageSkipDefault {
		t.Fatalf("triage skip with nothing stored = %d, want %d", got, triageSkipDefault)
	}
	for raw, want := range map[string]int{"0": 0, "45": 45, "": 15, "soon": 15, "-5": 15} {
		if err := m.store.SetSetting(triageSkipSetting, raw); err != nil {
			t.Fatal(err)
		}
		if got := storedTriageSkip(m.store); got != want {
			t.Fatalf("stored %q = %d, want %d", raw, got, want)
		}
	}
}

func TestTriageSkipCyclesInSettingsAndSaves(t *testing.T) {
	m := buildModel(t)
	m.openSettings()
	m.settings.field = settingsFieldTriageSkip
	m.cycleSetting(1)
	if m.settings.triageSkip != 30 {
		t.Fatalf("stepping on from 15 = %d, want 30", m.settings.triageSkip)
	}
	m.persistSettings()
	if m.triageSkip != 30*time.Minute || storedTriageSkip(m.store) != 30 {
		t.Fatalf("saved skip = %v stored %d, want 30 minutes", m.triageSkip, storedTriageSkip(m.store))
	}
}
