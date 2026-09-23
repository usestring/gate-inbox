package tracing

import (
	"errors"
	"testing"
	"time"
)

func span(d time.Duration, err error) Span {
	start := time.Now()
	return Span{Name: "poll.pass", Start: start, End: start.Add(d), Err: err}
}

func reason(attrs []Attr) string {
	for _, attr := range attrs {
		if attr.Key == "sample.reason" {
			return attr.Value.(string)
		}
	}
	return ""
}

// A failure is the whole reason anybody looks, so it can never be the thing
// the sampler throws away.
func TestAFailureIsAlwaysKept(t *testing.T) {
	t.Setenv(SampleEnv, "1000000")
	for range 50 {
		kept, why := keep([]Span{span(time.Millisecond, errors.New("no server"))})
		if !kept {
			t.Fatal("a failed span was sampled away")
		}
		if reason(why) != "error" {
			t.Fatalf("kept for %q, want error", reason(why))
		}
	}
}

// Slow is what the operator felt, so it survives whatever the rate says.
func TestSomethingSlowIsAlwaysKept(t *testing.T) {
	t.Setenv(SampleEnv, "1000000")
	for range 50 {
		kept, why := keep([]Span{span(slowEnough, nil)})
		if !kept {
			t.Fatal("a span over the slow threshold was sampled away")
		}
		if reason(why) != "slow" {
			t.Fatalf("kept for %q, want slow", reason(why))
		}
	}
}

// A child that failed counts, even when the root looks fine: the pass returned,
// but the tmux call under it did not.
func TestAFailedChildKeepsTheWholeTrace(t *testing.T) {
	t.Setenv(SampleEnv, "1000000")
	kept, why := keep([]Span{span(time.Millisecond, nil), span(time.Millisecond, errors.New("boom"))})
	if !kept || reason(why) != "error" {
		t.Fatalf("kept=%v reason=%q; a failing child must keep its trace", kept, reason(why))
	}
}

// The ordinary majority is what makes this affordable to leave on.
func TestOrdinaryTrafficIsMostlyDropped(t *testing.T) {
	t.Setenv(SampleEnv, "50")
	kept := 0
	for range 2000 {
		if ok, _ := keep([]Span{span(time.Millisecond, nil)}); ok {
			kept++
		}
	}
	if kept == 0 {
		t.Fatal("nothing ordinary was kept at all, so normal cannot be measured")
	}
	if kept > 200 {
		t.Fatalf("kept %d of 2000 at one-in-fifty; the sampler is not sampling", kept)
	}
}

// A dataset that dropped 98% of its traffic will be counted as if it were all
// of it unless the spans say otherwise, and every rate read off it will be
// wrong by the sample rate.
func TestAKeptSampleCarriesTheRateItStandsFor(t *testing.T) {
	t.Setenv(SampleEnv, "50")
	for range 5000 {
		ok, why := keep([]Span{span(time.Millisecond, nil)})
		if !ok {
			continue
		}
		if reason(why) != "baseline" {
			continue
		}
		for _, attr := range why {
			if attr.Key == "sample.rate" && attr.Value == 50 {
				return
			}
		}
		t.Fatalf("a baseline sample carried %v, want sample.rate=50", why)
	}
	t.Fatal("no baseline sample was kept in 5000 tries")
}

// A deliberate soak needs everything, and has to be able to say so.
func TestSampleOneKeepsEverything(t *testing.T) {
	t.Setenv(SampleEnv, "1")
	for range 100 {
		if ok, why := keep([]Span{span(time.Millisecond, nil)}); !ok {
			t.Fatalf("one-in-one dropped a span (reason %q)", reason(why))
		}
	}
}
