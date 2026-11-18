package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// snooze writes a snooze the way the command does: straight to the store,
// for the next pass to find.
func snooze(t *testing.T, m *Model, sess store.Session, until time.Time) {
	t.Helper()
	if err := m.store.SetSnoozed(sess.ID, until); err != nil {
		t.Fatalf("snooze %s: %v", sess.Name, err)
	}
}

// A snooze lifts in the poll pass once its time has come, and not before.
// The time may have come while no board was running: the end is stored, so
// the first pass back up is the one that lifts it.
func TestASnoozeLiftsOnThePassAfterItsTimeComes(t *testing.T) {
	m := buildModel(t)
	due := liveHookedSession(t, m, "due")
	later := liveHookedSession(t, m, "later")
	forever := liveHookedSession(t, m, "forever")
	snooze(t, m, due, time.Now().Add(-time.Minute))
	snooze(t, m, later, time.Now().Add(time.Hour))
	if err := m.store.SetMuted(forever.ID, true); err != nil {
		t.Fatal(err)
	}

	m.applyCmd(t, m.refreshCmd())

	if got := storedSession(t, m, "due"); got.Muted || !got.MutedUntil.IsZero() {
		t.Fatalf("a snooze past its end is still muted %v until %v", got.Muted, got.MutedUntil)
	}
	if sessionNamed(t, m, "due").Muted {
		t.Fatal("the board still shows the lifted snooze as muted")
	}
	if got := storedSession(t, m, "later"); !got.Muted || got.MutedUntil.IsZero() {
		t.Fatal("a snooze with an hour left was lifted")
	}
	if !storedSession(t, m, "forever").Muted {
		t.Fatal("a mute with no end was lifted")
	}
}

// A snoozed session that stops on a dialog or errors needs the operator in a
// way it did not when they set it aside, so the snooze ends there. A turn
// that merely finishes does not end it, and neither does a dialog that was
// already up when the snooze was set.
func TestASnoozeEndsEarlyOnAQuestionOrAnErrorButNotAFinishedTurn(t *testing.T) {
	m := buildModel(t)
	names := []string{"asks", "fails", "finishes", "asking"}
	for _, name := range names {
		sess := liveHookedSession(t, m, name)
		writeHookStatus(t, m, sess.ID, status.Working)
	}
	writeHookStatus(t, m, sessionNamed(t, m, "asking").ID, status.Waiting)
	m.applyCmd(t, m.refreshCmd())
	for _, name := range names[:3] {
		requireStatus(t, m, name, status.Working)
	}
	requireStatus(t, m, "asking", status.Waiting)
	for _, name := range names {
		snooze(t, m, sessionNamed(t, m, name), time.Now().Add(time.Hour))
	}
	m.applyCmd(t, m.refreshCmd())
	for _, name := range names {
		if !storedSession(t, m, name).Muted {
			t.Fatalf("%s lost its snooze on a pass that moved nothing", name)
		}
	}

	writeHookStatus(t, m, sessionNamed(t, m, "asks").ID, status.Waiting)
	writeHookStatus(t, m, sessionNamed(t, m, "fails").ID, status.Errored)
	writeHookStatus(t, m, sessionNamed(t, m, "finishes").ID, status.Finished)
	m.applyCmd(t, m.refreshCmd())
	requireStatus(t, m, "asks", status.Waiting)
	requireStatus(t, m, "fails", status.Errored)
	requireStatus(t, m, "finishes", status.Finished)

	for name, wantMuted := range map[string]bool{"asks": false, "fails": false, "finishes": true, "asking": true} {
		got := storedSession(t, m, name)
		if got.Muted != wantMuted || got.MutedUntil.IsZero() == wantMuted {
			t.Errorf("%s (%s) = muted %v until %v, want muted %v", name, got.Status, got.Muted, got.MutedUntil, wantMuted)
		}
	}
	if m.triageWalkable(sessionNamed(t, m, "finishes")) {
		t.Error("a snoozed session that finished a turn went back on the queue")
	}
}

// The snooze key walks an hour, four hours, a day, then back to unmuted,
// and the row reads when it comes back.
func TestTheSnoozeKeyWalksItsLengthsAndTheRowSaysWhenItIsBack(t *testing.T) {
	m := buildModel(t)
	liveTriageFleet(t, m, map[string]string{"ask": status.Waiting, "busy": status.Working})
	m.rebuildRows()
	m.selectSessionRow(t, "ask")

	for _, want := range []time.Duration{time.Hour, 4 * time.Hour, 24 * time.Hour} {
		pressed := time.Now()
		updated, _ := m.handleKey(key("alt+z"))
		m = updated.(*Model)
		ask := sessionNamed(t, m, "ask")
		if left := ask.MutedUntil.Sub(pressed); !ask.Muted || left < want || left > want+time.Minute {
			t.Fatalf("snoozed for %v (muted %v), want %v: %s", left, ask.Muted, want, m.errBar.text)
		}
		if stored := storedSession(t, m, "ask"); !stored.Muted || !stored.MutedUntil.Equal(ask.MutedUntil) {
			t.Fatalf("the store holds muted %v until %v, the board %v", stored.Muted, stored.MutedUntil, ask.MutedUntil)
		}
		label := mutedStatusLabel + " " + snoozeLabel(ask.MutedUntil, time.Now())
		if rail := triageRail(m); !strings.Contains(rail, label) {
			t.Fatalf("a snoozed row does not read %q:\n%s", label, rail)
		}
		if m.needsPerson(ask) || m.triageWalkable(ask) {
			t.Fatal("a snoozed session still counts as on the operator's queue")
		}
	}

	updated, _ := m.handleKey(key("alt+z"))
	m = updated.(*Model)
	if ask := storedSession(t, m, "ask"); ask.Muted || !ask.MutedUntil.IsZero() {
		t.Fatalf("the fourth press left muted %v until %v, want it unmuted", ask.Muted, ask.MutedUntil)
	}
}

// The walk is read off how long the row has left, so it carries on from a
// snooze the command or a restart left behind.
func TestNextSnoozeIsReadOffWhatTheRowHasLeft(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		sess store.Session
		want time.Duration
	}{
		{"not muted", store.Session{}, time.Hour},
		{"muted with no end", store.Session{Muted: true}, time.Hour},
		{"an hour just set", store.Session{Muted: true, MutedUntil: now.Add(time.Hour)}, 4 * time.Hour},
		{"twenty minutes left", store.Session{Muted: true, MutedUntil: now.Add(20 * time.Minute)}, 4 * time.Hour},
		{"four hours just set", store.Session{Muted: true, MutedUntil: now.Add(4*time.Hour - time.Second)}, 24 * time.Hour},
		{"ten hours from the command", store.Session{Muted: true, MutedUntil: now.Add(10 * time.Hour)}, 24 * time.Hour},
		{"a day just set", store.Session{Muted: true, MutedUntil: now.Add(24*time.Hour - time.Second)}, 0},
		{"a week from the command", store.Session{Muted: true, MutedUntil: now.Add(7 * 24 * time.Hour)}, 0},
	} {
		if got := nextSnooze(tc.sess, now); got != tc.want {
			t.Errorf("%s: next = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSnoozeLabelIsAsShortAsTheDistanceAllows(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	for until, want := range map[time.Time]string{
		now.Add(4 * time.Hour):       "until 14:00",
		now.Add(24 * time.Hour):      "until Thu 10:00",
		now.Add(10 * 24 * time.Hour): "until Nov 28",
	} {
		if got := snoozeLabel(until, now); got != want {
			t.Errorf("snoozeLabel(%v) = %q, want %q", until, got, want)
		}
	}
}
