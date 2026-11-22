package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/tmuxtest"
)

// A mute is a stored flag: it survives a reread, comes back through both
// Get and ListSessions, and clears on a second write.
func TestSetMutedRoundTrips(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("a", "g1")); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := st.SetMuted("a", true); err != nil {
		t.Fatalf("set muted: %v", err)
	}
	got, err := st.Get("a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Muted {
		t.Fatal("Get did not read back the mute")
	}
	listed, err := st.ListSessions(false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || !listed[0].Muted {
		t.Fatalf("ListSessions = %+v, want one muted row", listed)
	}

	if err := st.SetMuted("a", false); err != nil {
		t.Fatalf("clear muted: %v", err)
	}
	got, err = st.Get("a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Muted {
		t.Fatal("unmute did not clear the flag")
	}
}

func TestSetMutedRefusesAGoneSession(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetMuted("ghost", true); err == nil {
		t.Fatal("muting a row that does not exist succeeded")
	}
}

// A snooze is a mute with an end, stored beside the flag: both come back
// through Get and ListSessions, and a plain mute or an unmute written over
// it leaves no end behind to lift the row later.
func TestSetSnoozedRoundTripsAndAPlainMuteReplacesIt(t *testing.T) {
	st := newTestStore(t)
	enableSnooze(t, st)
	if err := st.CreateSession(sample("a", "g1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	until := time.Now().Add(4 * time.Hour)
	if err := st.SetSnoozed("a", until); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	got, err := st.Get("a")
	if err != nil || !got.Muted || !got.MutedUntil.Equal(until) {
		t.Fatalf("Get = muted %v until %v (err %v), want muted until %v", got.Muted, got.MutedUntil, err, until)
	}
	listed, err := st.ListSessions(false)
	if err != nil || len(listed) != 1 || !listed[0].Muted || !listed[0].MutedUntil.Equal(until) {
		t.Fatalf("ListSessions = %+v (err %v), want one row muted until %v", listed, err, until)
	}

	if err := st.SetMuted("a", true); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if got, err = st.Get("a"); err != nil || !got.Muted || !got.MutedUntil.IsZero() {
		t.Fatalf("a plain mute over a snooze = muted %v until %v (err %v), want muted with no end", got.Muted, got.MutedUntil, err)
	}
	if err := st.SetSnoozed("a", until); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	if err := st.SetMuted("a", false); err != nil {
		t.Fatalf("unmute: %v", err)
	}
	if got, err = st.Get("a"); err != nil || got.Muted || !got.MutedUntil.IsZero() {
		t.Fatalf("an unmute over a snooze = muted %v until %v (err %v), want neither", got.Muted, got.MutedUntil, err)
	}
	if err := st.SetSnoozed("a", time.Time{}); err == nil {
		t.Fatal("a snooze with no end was stored")
	}
	if err := st.SetSnoozed("ghost", until); err == nil {
		t.Fatal("snoozing a row that does not exist succeeded")
	}
}

// The board is what lifts a snooze, so the end has to be there for the next
// board to read: it survives the database being closed and opened again.
func TestASnoozeSurvivesARestart(t *testing.T) {
	path := filepath.Join(tmuxtest.ScratchDir(t), "restart.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	enableSnooze(t, st)
	if err := st.CreateSession(sample("a", "g1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	until := time.Now().Add(time.Hour)
	if err := st.SetSnoozed("a", until); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	got, err := st.Get("a")
	if err != nil || !got.Muted || !got.MutedUntil.Equal(until) {
		t.Fatalf("after a restart = muted %v until %v (err %v), want muted until %v", got.Muted, got.MutedUntil, err, until)
	}
}

// The pass that ends a snooze read the row a moment before it writes. A
// snooze set again in between is a new decision, and the verdict on the old
// end must not lift it.
func TestEndSnoozeLiftsOnlyTheSnoozeThePassRead(t *testing.T) {
	st := newTestStore(t)
	enableSnooze(t, st)
	for _, id := range []string{"due", "reset", "plain"} {
		if err := st.CreateSession(sample(id, "g1")); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	read := time.Now().Add(-time.Minute)
	later := time.Now().Add(4 * time.Hour)
	if err := st.SetSnoozed("due", read); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSnoozed("reset", later); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMuted("plain", true); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyDerivedStates(time.Now(), []DerivedState{
		{ID: "due", EndSnooze: read}, {ID: "reset", EndSnooze: read}, {ID: "plain", EndSnooze: read},
	}); err != nil {
		t.Fatalf("ApplyDerivedStates: %v", err)
	}
	if got, err := st.Get("due"); err != nil || got.Muted || !got.MutedUntil.IsZero() {
		t.Fatalf("due = muted %v until %v (err %v), want it lifted", got.Muted, got.MutedUntil, err)
	}
	if got, err := st.Get("reset"); err != nil || !got.Muted || !got.MutedUntil.Equal(later) {
		t.Fatalf("reset = muted %v until %v (err %v), want the newer snooze kept", got.Muted, got.MutedUntil, err)
	}
	if got, err := st.Get("plain"); err != nil || !got.Muted {
		t.Fatalf("plain = muted %v (err %v), want a mute with no end left alone", got.Muted, err)
	}
}

func enableSnooze(t *testing.T, st *Store) {
	t.Helper()
	if err := st.SetSetting(SnoozeSetting, "on"); err != nil {
		t.Fatalf("turn on timed snooze: %v", err)
	}
}

// Timed snooze ships off: with the experiment off no snooze is stored, and
// one stored while it was on still lifts when the board's pass ends it, so
// turning the switch off leaves no session muted for good.
func TestSnoozeIsRefusedWhileOffAndAStoredOneStillLifts(t *testing.T) {
	st := newTestStore(t)
	if err := st.CreateSession(sample("a", "g1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if st.SnoozeEnabled() {
		t.Fatal("timed snooze is on in a fresh store")
	}
	until := time.Now().Add(time.Hour)
	if err := st.SetSnoozed("a", until); !errors.Is(err, ErrSnoozeOff) {
		t.Fatalf("snooze while off = %v, want ErrSnoozeOff", err)
	}
	if got, err := st.Get("a"); err != nil || got.Muted || !got.MutedUntil.IsZero() {
		t.Fatalf("after a refused snooze = muted %v until %v (err %v), want neither", got.Muted, got.MutedUntil, err)
	}

	enableSnooze(t, st)
	if err := st.SetSnoozed("a", until); err != nil {
		t.Fatalf("snooze while on: %v", err)
	}
	if err := st.SetSetting(SnoozeSetting, "off"); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyDerivedStates(time.Now(), []DerivedState{{ID: "a", EndSnooze: until}}); err != nil {
		t.Fatalf("ApplyDerivedStates: %v", err)
	}
	if got, err := st.Get("a"); err != nil || got.Muted || !got.MutedUntil.IsZero() {
		t.Fatalf("a snooze ended after the switch went off = muted %v until %v (err %v), want it lifted", got.Muted, got.MutedUntil, err)
	}
}
