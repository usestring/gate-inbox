package store

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestHeldSnippetQueueAndIntentAreAtomic(t *testing.T) {
	st := newTestStore(t)
	msg := message("ship it", time.Now())
	msg.SenderID = "human/snippet"
	id, err := st.EnqueueHeldSnippet(msg, true)
	if err != nil {
		t.Fatal(err)
	}
	held, err := st.HeldSnippets()
	if err != nil || !held[id] {
		t.Fatalf("held = %v, err %v", held, err)
	}
	if err := st.SetSetting(heldSnippetPrefix+strconv.FormatInt(id+1, 10), "false"); err != nil {
		t.Fatal(err)
	}
	msg.Body, msg.Fingerprint = "another line", "another line"
	if _, err := st.EnqueueHeldSnippet(msg, false); err == nil {
		t.Fatal("enqueue succeeded without durable intent")
	}
	count, err := st.QueuedCount(msg.SessionID)
	if err != nil || count != 1 {
		t.Fatalf("queue = %d, err %v; rejected enqueue must roll back", count, err)
	}
	if err := st.ForgetHeldSnippet(id + 1); err != nil {
		t.Fatal(err)
	}
	if err := st.KeepHeldSnippetsHere(); err != nil {
		t.Fatal(err)
	}
	held, err = st.HeldSnippets()
	if err != nil || held[id] {
		t.Fatalf("cancelled intent = %v, err %v", held, err)
	}
	if err := st.MarkDelivered(id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.ForgetHeldSnippet(id); err != nil {
		t.Fatal(err)
	}
	held, err = st.HeldSnippets()
	if err != nil || len(held) != 0 {
		t.Fatalf("settled intent = %v, err %v", held, err)
	}
}

func TestHeldSnippetIntentSurvivesStoreReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	msg := message("ship it", time.Now())
	msg.SenderID = SnippetSenderID
	id, err := st.EnqueueHeldSnippet(msg, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	held, err := st.HeldSnippets()
	if err != nil || !held[id] {
		t.Fatalf("restored intent: %+v, %v", held, err)
	}
	msg.Body, msg.Fingerprint = "older queued snippet", "older queued snippet"
	legacy, _, err := st.Enqueue(msg, DefaultInboxLimits)
	if err != nil {
		t.Fatal(err)
	}
	held, err = st.HeldSnippets()
	if err != nil {
		t.Fatal(err)
	}
	handOver, ok := held[legacy]
	if !ok || handOver {
		t.Fatalf("legacy snippet not restored without handover: %+v", held)
	}
}
