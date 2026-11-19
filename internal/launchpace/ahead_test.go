package launchpace

import (
	"errors"
	"testing"
	"time"
)

// A launch going ahead starts beside one routine launch still in flight,
// but not beside two.
func TestALaunchAheadRunsBesideOneInFlight(t *testing.T) {
	p, h := newPaced(t, Limits{})
	if _, _, err := p.Try("routine"); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(DefaultMinGap)
	if _, _, err := p.Try("routine2"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second routine launch = %v, want ErrBusy: routine launches stay one at a time", err)
	}
	if _, _, err := p.TryAhead("cut1"); err != nil {
		t.Fatalf("launch ahead beside one in flight = %v, want admitted", err)
	}
	h.now = h.now.Add(DefaultMinGap)
	if _, _, err := p.TryAhead("cut2"); !errors.Is(err, ErrBusy) {
		t.Fatalf("launch ahead beside two in flight = %v, want ErrBusy", err)
	}
}

// While a launch going ahead is refused, routine launches wait behind it,
// even once the queue frees, and it takes the next turn.
func TestRoutineLaunchesWaitBehindOneGoingAhead(t *testing.T) {
	p, h := newPaced(t, Limits{})
	if _, _, err := p.Try("a"); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(DefaultMinGap)
	if _, _, err := p.TryAhead("b"); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(DefaultMinGap)
	if _, _, err := p.TryAhead("cut"); !errors.Is(err, ErrBusy) {
		t.Fatalf("third launch = %v, want ErrBusy", err)
	}
	h.rested["a"], h.rested["b"] = true, true
	if _, _, err := p.Try("routine"); !errors.Is(err, ErrBusy) {
		t.Fatalf("routine launch with one waiting ahead = %v, want ErrBusy", err)
	}
	if _, _, err := p.TryAhead("cut"); err != nil {
		t.Fatalf("launch ahead once the queue frees = %v, want admitted", err)
	}
	h.rested["cut"] = true
	h.now = h.now.Add(DefaultMinGap)
	if _, _, err := p.Try("routine"); err != nil {
		t.Fatalf("routine launch once nothing waits ahead = %v, want admitted", err)
	}
}

// A launch going ahead that stops asking holds routine launches back only
// for aheadHold.
func TestAnAbandonedLaunchAheadLapses(t *testing.T) {
	p, h := newPaced(t, Limits{})
	if _, _, err := p.Try("a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.TryAhead("cut"); !errors.Is(err, ErrBusy) {
		t.Fatalf("launch ahead inside the gap = %v, want ErrBusy", err)
	}
	h.rested["a"] = true
	h.now = h.now.Add(aheadHold - time.Second)
	if _, _, err := p.Try("routine"); !errors.Is(err, ErrBusy) {
		t.Fatalf("routine launch inside the hold = %v, want ErrBusy", err)
	}
	h.now = h.now.Add(time.Second)
	if _, _, err := p.Try("routine"); err != nil {
		t.Fatalf("routine launch after the hold = %v, want admitted", err)
	}
}
