package store

import (
	"reflect"
	"testing"
	"time"
)

func TestParkedSetAddsRemovesAndClears(t *testing.T) {
	st := newTestStore(t)
	if ids, err := st.Parked(); err != nil || len(ids) != 0 {
		t.Fatalf("fresh store parked = %v, %v", ids, err)
	}
	if err := st.AddParked([]string{"a", "b"}); err != nil {
		t.Fatalf("AddParked: %v", err)
	}
	// A second park keeps the first one's ids and never repeats one.
	if err := st.AddParked([]string{"b", "c"}); err != nil {
		t.Fatalf("AddParked again: %v", err)
	}
	ids, err := st.Parked()
	if err != nil || !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("parked after two adds = %v, %v", ids, err)
	}
	if err := st.RemoveParked([]string{"b", "missing"}); err != nil {
		t.Fatalf("RemoveParked: %v", err)
	}
	ids, err = st.Parked()
	if err != nil || !reflect.DeepEqual(ids, []string{"a", "c"}) {
		t.Fatalf("parked after remove = %v, %v", ids, err)
	}
	if err := st.RemoveParked([]string{"a", "c"}); err != nil {
		t.Fatalf("RemoveParked all: %v", err)
	}
	if ids, err := st.Parked(); err != nil || len(ids) != 0 {
		t.Fatalf("parked after clearing = %v, %v", ids, err)
	}
}

func TestDormantSetIsIndependentOfTheOthers(t *testing.T) {
	st := newTestStore(t)
	if err := st.AddParked([]string{"a", "b", "c"}); err != nil {
		t.Fatalf("AddParked: %v", err)
	}
	if err := st.AddDormant([]string{"b", "c"}); err != nil {
		t.Fatalf("AddDormant: %v", err)
	}
	if err := st.RemoveDormant([]string{"c"}); err != nil {
		t.Fatalf("RemoveDormant: %v", err)
	}
	if ids, err := st.Dormant(); err != nil || !reflect.DeepEqual(ids, []string{"b"}) {
		t.Fatalf("dormant = %v, %v", ids, err)
	}
	if ids, _ := st.Parked(); !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("parked = %v, want the dormant set to leave it alone", ids)
	}
	if ids, _ := st.Interrupted(); len(ids) != 0 {
		t.Fatalf("interrupted = %v", ids)
	}
}

func TestInterruptedSetAndPendingInputQueue(t *testing.T) {
	st := newTestStore(t)
	if err := st.AddInterrupted([]string{"a", "b"}); err != nil {
		t.Fatalf("AddInterrupted: %v", err)
	}
	if err := st.RemoveInterrupted([]string{"a"}); err != nil {
		t.Fatalf("RemoveInterrupted: %v", err)
	}
	ids, err := st.Interrupted()
	if err != nil || !reflect.DeepEqual(ids, []string{"b"}) {
		t.Fatalf("interrupted = %v, %v", ids, err)
	}
	// The two sets are independent rows.
	if parked, _ := st.Parked(); len(parked) != 0 {
		t.Fatalf("parked = %v", parked)
	}
	sess := sample("q1", "")
	sess.PendingInputs = []string{"first"}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.QueuePendingInput("q1", "continue"); err != nil {
		t.Fatalf("QueuePendingInput: %v", err)
	}
	got, err := st.Get("q1")
	if err != nil || !reflect.DeepEqual(got.PendingInputs, []string{"first", "continue"}) {
		t.Fatalf("pending after queue = %v, %v", got.PendingInputs, err)
	}
	if err := st.QueuePendingInput("missing", "continue"); err == nil {
		t.Fatal("queueing on a missing row should fail")
	}
}

func TestParkedAtRoundTrips(t *testing.T) {
	st := newTestStore(t)
	if at, err := st.ParkedAt(); err != nil || !at.IsZero() {
		t.Fatalf("fresh store parked_at = %v, %v", at, err)
	}
	when := time.Unix(1_800_000_000, 0)
	if err := st.SetParkedAt(when); err != nil {
		t.Fatalf("SetParkedAt: %v", err)
	}
	if at, err := st.ParkedAt(); err != nil || !at.Equal(when) {
		t.Fatalf("parked_at = %v, %v, want %v", at, err, when)
	}
}
