package scoring

import (
	"testing"
	"time"
)

// The recency bonus is absolute: sessions all touched two days ago read the
// same as ones never touched, and only a fresh touch earns it.
func TestTriageRecencyIsAbsoluteNotARank(t *testing.T) {
	w := DefaultTriageWeights()
	recency := func(in TriageInput) float64 {
		term, _ := Triage(in, w).Term(TermRecency)
		return term.Points()
	}
	if got := recency(TriageInput{Touched: false}); got != 0 {
		t.Fatalf("never touched: recency = %v, want 0", got)
	}
	if got := recency(TriageInput{Touched: true, SinceOperator: 48 * time.Hour}); got > 1e-6 {
		t.Fatalf("touched two days ago: recency = %v, want ~0", got)
	}
	if got := recency(TriageInput{Touched: true, SinceOperator: 30 * time.Second}); got < 1000 {
		t.Fatalf("touched 30s ago: recency = %v, want a bonus worth minutes of a question's wait", got)
	}
}

// With the default weights the bonuses add nothing for a cold session with no
// estimate, so the total is the old weighted wait and orderings keep.
func TestTriageDefaultsReduceToTheWeightedWaitForAColdSession(t *testing.T) {
	s := Triage(TriageInput{Wait: 9 * time.Minute, StateWeight: 2, CacheStake: 5}, DefaultTriageWeights())
	if got, want := s.Total(), (10*time.Minute).Seconds()*2; got != want {
		t.Fatalf("total = %v (%s), want %v", got, s, want)
	}
}

// A long expected turn lifts the score; the horizon saturates.
func TestTriageHorizonLiftsALongTurn(t *testing.T) {
	w := DefaultTriageWeights()
	base := Triage(TriageInput{Wait: time.Minute, StateWeight: 1}, w).Total()
	long := Triage(TriageInput{Wait: time.Minute, StateWeight: 1, ExpectedTurn: HorizonReference}, w).Total()
	longer := Triage(TriageInput{Wait: time.Minute, StateWeight: 1, ExpectedTurn: 4 * HorizonReference}, w).Total()
	if long <= base || longer != long {
		t.Fatalf("base %v, long %v, longer %v: want long > base and the horizon saturated", base, long, longer)
	}
}
