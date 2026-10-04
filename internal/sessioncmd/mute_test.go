package sessioncmd

import (
	"testing"

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
