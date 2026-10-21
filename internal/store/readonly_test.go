package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestOpenReadOnlyReadsAndRefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(Session{ID: "a1b2c3d4", Name: "worker", Tool: "claude", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, delivered := range []bool{false, true} {
		id, _, err := st.Enqueue(InboxMessage{SessionID: "a1b2c3d4", SenderID: "e5f6a7b8", SenderName: "boss",
			Body: "hello", Fingerprint: string(rune('a' + i)), SentAt: now}, DefaultInboxLimits)
		if err != nil {
			t.Fatal(err)
		}
		if delivered {
			if err := st.MarkDelivered(id, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	st.Close()

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	sessions, err := ro.ListSessions(true)
	if err != nil || len(sessions) != 1 || sessions[0].ID != "a1b2c3d4" {
		t.Fatalf("ListSessions = %+v, %v", sessions, err)
	}
	waiting, err := ro.WaitingMessages()
	if err != nil || len(waiting) != 1 || waiting[0].SenderName != "boss" {
		t.Fatalf("WaitingMessages = %+v, %v; want the one undelivered message", waiting, err)
	}
	if err := ro.CreateSession(Session{ID: "deadbeef", Name: "x", Tool: "claude"}); err == nil {
		t.Fatal("a write through the read-only handle succeeded")
	}
}

func TestOpenReadOnlyNeedsAnExistingFile(t *testing.T) {
	if _, err := OpenReadOnly(filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Fatal("OpenReadOnly created a database")
	}
}
