package sessioncmd

import (
	"strings"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The mute command writes the persistent flag the board's own key writes, from
// the process side, and reads it back on the returned row.
func TestMuteSetsAndClearsTheStoredFlag(t *testing.T) {
	h := newSessionHarness(t)
	target := store.Session{
		ID:     "beef1234",
		Name:   "target",
		Tool:   "echoer",
		Cwd:    t.TempDir(),
		Status: status.Waiting,
	}
	if err := h.store.CreateSession(target); err != nil {
		t.Fatalf("create target: %v", err)
	}

	got, err := h.sessions.Mute(h.caller.ID, target.ID, true)
	if err != nil {
		t.Fatalf("mute: %v", err)
	}
	if !got.Muted {
		t.Fatal("mute returned a row still reading unmuted")
	}
	stored, err := h.store.Get(target.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !stored.Muted {
		t.Fatal("the mute never reached the store")
	}

	got, err = h.sessions.Mute(h.caller.ID, target.ID, false)
	if err != nil {
		t.Fatalf("unmute: %v", err)
	}
	if got.Muted {
		t.Fatal("unmute returned a row still reading muted")
	}
	stored, err = h.store.Get(target.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Muted {
		t.Fatal("unmute never reached the store")
	}
}

func TestMuteRefusesATargetThatDoesNotExist(t *testing.T) {
	h := newSessionHarness(t)
	if _, err := h.sessions.Mute(h.caller.ID, "nope0000", true); err == nil {
		t.Fatal("muting a session that does not exist succeeded")
	}
}

// A snooze writes the end beside the flag and reports it, and one that has
// already ended is refused rather than stored for the board to lift at once.
func TestSnoozeStoresTheEndAndRefusesOneInThePast(t *testing.T) {
	h := newSessionHarness(t)
	target := store.Session{ID: "beef5678", Name: "target", Tool: "echoer", Cwd: t.TempDir(), Status: status.Working}
	if err := h.store.CreateSession(target); err != nil {
		t.Fatalf("create target: %v", err)
	}
	until := time.Now().Add(4 * time.Hour)

	got, err := h.sessions.Snooze(h.caller.ID, target.ID, until)
	if err != nil {
		t.Fatalf("snooze: %v", err)
	}
	if !got.Muted || got.MutedUntil != until.Format(time.RFC3339) {
		t.Fatalf("snooze returned muted %v until %q, want until %s", got.Muted, got.MutedUntil, until.Format(time.RFC3339))
	}
	if stored, err := h.store.Get(target.ID); err != nil || !stored.Muted || !stored.MutedUntil.Equal(until) {
		t.Fatalf("stored muted %v until %v (err %v), want until %v", stored.Muted, stored.MutedUntil, err, until)
	}
	if text := FormatMuteState(got); !strings.HasPrefix(text, "muted until "+got.MutedUntil) {
		t.Fatalf("FormatMuteState = %q", text)
	}

	// An unmute takes the end with it, on the row the tools report too.
	if got, err = h.sessions.Mute(h.caller.ID, target.ID, false); err != nil || got.Muted || got.MutedUntil != "" {
		t.Fatalf("unmute = muted %v until %q (err %v)", got.Muted, got.MutedUntil, err)
	}
	if _, err := h.sessions.Snooze(h.caller.ID, target.ID, time.Now().Add(-time.Minute)); err == nil || !strings.Contains(err.Error(), "has passed") {
		t.Fatalf("a snooze ending in the past: err = %v", err)
	}
	if stored, err := h.store.Get(target.ID); err != nil || stored.Muted {
		t.Fatalf("the refused snooze muted the row: %+v err=%v", stored, err)
	}
}

func TestParseSnoozeEnd(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.FixedZone("test", 2*60*60))
	for _, tc := range []struct {
		length, until string
		want          time.Time
	}{
		{"", "", time.Time{}},
		{"90m", "", now.Add(90 * time.Minute)},
		{"4h", "", now.Add(4 * time.Hour)},
		{"2d", "", now.Add(48 * time.Hour)},
		{"", "15:30", time.Date(2026, 11, 18, 15, 30, 0, 0, now.Location())},
		// A clock time already gone today is tomorrow's.
		{"", "09:00", time.Date(2026, 11, 19, 9, 0, 0, 0, now.Location())},
		{"", "2026-11-20", time.Date(2026, 11, 20, 0, 0, 0, 0, now.Location())},
		{"", "2026-11-20 08:15", time.Date(2026, 11, 20, 8, 15, 0, 0, now.Location())},
		{"", "2026-11-20T08:15:00Z", time.Date(2026, 11, 20, 8, 15, 0, 0, time.UTC)},
	} {
		got, err := ParseSnoozeEnd(tc.length, tc.until, now)
		if err != nil || !got.Equal(tc.want) {
			t.Errorf("ParseSnoozeEnd(%q, %q) = %v, %v; want %v", tc.length, tc.until, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ length, until, want string }{
		{"1h", "15:30", "not both"},
		{"soon", "", "not a duration"},
		{"xd", "", "not a duration"},
		{"0h", "", "more than zero"},
		{"-2d", "", "more than zero"},
		{"", "teatime", "not a time"},
		{"", "2026-11-17", "has passed"},
	} {
		if _, err := ParseSnoozeEnd(tc.length, tc.until, now); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseSnoozeEnd(%q, %q) error = %v, want %q", tc.length, tc.until, err, tc.want)
		}
	}
}
