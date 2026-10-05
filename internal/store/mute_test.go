package store

import "testing"

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
