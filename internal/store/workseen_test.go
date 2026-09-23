package store

import (
	"testing"
	"time"
)

func TestWorkSeenKeepsTheFirstSightingAndForgets(t *testing.T) {
	s := newTestStore(t)
	first := time.UnixMilli(1_700_000_000_000)
	if err := s.RecordWorkSeen(map[string]time.Time{"pr:o/r#1": first, "ticket:ABC-1": first}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWorkSeen(map[string]time.Time{"pr:o/r#1": first.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	seen, err := s.WorkSeen()
	if err != nil {
		t.Fatal(err)
	}
	if !seen["pr:o/r#1"].Equal(first) {
		t.Fatalf("second sighting moved the clock: %v", seen["pr:o/r#1"])
	}
	if err := s.ForgetWorkSeen([]string{"pr:o/r#1"}); err != nil {
		t.Fatal(err)
	}
	seen, _ = s.WorkSeen()
	if _, ok := seen["pr:o/r#1"]; ok || len(seen) != 1 {
		t.Fatalf("forget left %v", seen)
	}
}
