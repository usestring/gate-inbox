package ui

import (
	"testing"
	"time"
)

// A dotted phase is a stretch accumulated inside the phase above it. Laying
// it out as a span of its own would put derive.status beside derive in the
// waterfall and double the pass's apparent cost; it has to ride its parent.
func TestAccumulatedPartsRideThePhaseTheyBelongTo(t *testing.T) {
	grouped := groupPhases(passPhases{
		lock:   time.Millisecond,
		derive: 30 * time.Millisecond,
		status: 28 * time.Millisecond,
		inbox:  time.Millisecond,
		admin:  2 * time.Millisecond,
		mu:     time.Millisecond,
	})

	var names []string
	for _, phase := range grouped {
		names = append(names, phase.name)
		for _, part := range phase.parts {
			if part.Key == "derive.status" && phase.name != "derive" {
				t.Fatalf("derive.status was attached to %q", phase.name)
			}
			if part.Key == "admin.mu" && phase.name != "admin" {
				t.Fatalf("admin.mu was attached to %q", phase.name)
			}
		}
	}
	for _, dotted := range names {
		if dotted == "derive.status" || dotted == "admin.mu" {
			t.Fatalf("%q was laid out as a phase of its own, which double-counts it", dotted)
		}
	}

	var derive phaseSpan
	for _, phase := range grouped {
		if phase.name == "derive" {
			derive = phase
		}
	}
	if len(derive.parts) == 0 {
		t.Fatal("derive carries none of its parts, so a slow status is invisible on the span")
	}
	var found bool
	for _, part := range derive.parts {
		if part.Key == "derive.status" && part.Value == 28*time.Millisecond {
			found = true
		}
	}
	if !found {
		t.Fatalf("derive.status is not on the derive span: %v", derive.parts)
	}
}

// The phases are consecutive laps of one clock, so the spans have to tile the
// pass without overlapping -- a waterfall that overlaps is reporting work that
// ran twice.
func TestPhaseSpansTileThePassInOrder(t *testing.T) {
	phases := passPhases{
		lock: time.Millisecond, admin: 2 * time.Millisecond, list: 3 * time.Millisecond,
		scan: 4 * time.Millisecond, capture: 5 * time.Millisecond,
	}
	var total time.Duration
	for _, phase := range groupPhases(phases) {
		total += phase.d
	}
	if want := 15 * time.Millisecond; total != want {
		t.Fatalf("the phases sum to %v, want %v: a part is being counted beside its parent", total, want)
	}
}
