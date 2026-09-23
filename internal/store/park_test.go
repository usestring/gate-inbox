package store

import (
	"reflect"
	"testing"
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
