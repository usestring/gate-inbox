package launchpace

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// fakeHost is a clock that only moves when the pacer sleeps, a load
// reading, and the set of launches that have come to rest.
type fakeHost struct {
	now    time.Time
	load   float64
	rested map[string]bool
	slept  time.Duration
}

func newPaced(t *testing.T, limits Limits) (*Pacer, *fakeHost) {
	t.Helper()
	h := &fakeHost{now: time.Date(2026, 10, 20, 22, 54, 41, 0, time.UTC), rested: map[string]bool{}}
	p := New(t.TempDir(), limits, func(id string, _ time.Time) bool { return h.rested[id] })
	p.now = func() time.Time { return h.now }
	p.load = func() (float64, bool) { return h.load, true }
	p.sleep = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		h.now = h.now.Add(d)
		h.slept += d
		return nil
	}
	return p, h
}

func TestGapStretchesWithLoadUpToTheCap(t *testing.T) {
	var l Limits
	for _, tc := range []struct {
		load float64
		want time.Duration
	}{
		{0, 5 * time.Second},
		{2, 5 * time.Second},
		{8, 20 * time.Second},
		{15, 37500 * time.Millisecond},
		{30, time.Minute},
	} {
		if got := l.Gap(tc.load); got != tc.want {
			t.Errorf("Gap(%v) = %v, want %v", tc.load, got, tc.want)
		}
	}
}

func TestOneLaunchAtATimeUntilItSettles(t *testing.T) {
	p, h := newPaced(t, Limits{})
	if _, _, err := p.Try("a"); err != nil {
		t.Fatalf("first launch: %v", err)
	}
	h.now = h.now.Add(30 * time.Second)
	if _, wait, err := p.Try("b"); !errors.Is(err, ErrBusy) || wait <= 0 {
		t.Fatalf("second launch while the first is starting = %v, wait %v; want ErrBusy", err, wait)
	}
	h.rested["a"] = true
	if _, _, err := p.Try("b"); err != nil {
		t.Fatalf("second launch once the first rests: %v", err)
	}
}

func TestAnUnsettledLaunchIsLetGoAfterSettle(t *testing.T) {
	p, h := newPaced(t, Limits{Settle: 90 * time.Second})
	if _, _, err := p.Try("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if h.slept != 90*time.Second {
		t.Fatalf("waited %v for a launch that never settled, want the 90s settle", h.slept)
	}
}

func TestMinimumGapEvenWhenTheLastSettledAtOnce(t *testing.T) {
	p, h := newPaced(t, Limits{})
	if _, _, err := p.Try("a"); err != nil {
		t.Fatal(err)
	}
	h.rested["a"] = true
	if _, wait, err := p.Try("b"); !errors.Is(err, ErrBusy) || wait != DefaultMinGap {
		t.Fatalf("Try right after = %v, wait %v; want ErrBusy and %v", err, wait, DefaultMinGap)
	}
}

// A restart's worth of relaunches on a loaded host is spread over the
// gap the load sets, never two in the same gap.
func TestABurstIsSpreadByLoad(t *testing.T) {
	p, h := newPaced(t, Limits{})
	h.load = 8 // 64 on 8 cores
	var starts []time.Time
	for i := range 10 {
		id := fmt.Sprintf("s%d", i)
		if _, err := p.Wait(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		starts = append(starts, h.now)
		h.rested[id] = true
	}
	for i := 1; i < len(starts); i++ {
		if gap := starts[i].Sub(starts[i-1]); gap < 20*time.Second {
			t.Fatalf("launch %d started %v after the one before, want at least 20s at load 8/CPU", i, gap)
		}
	}
}

func TestMaxInFlightAdmitsThatMany(t *testing.T) {
	p, h := newPaced(t, Limits{MaxInFlight: 2, MinGap: time.Second})
	if _, _, err := p.Try("a"); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(time.Second)
	if _, _, err := p.Try("b"); err != nil {
		t.Fatalf("second of two: %v", err)
	}
	h.now = h.now.Add(time.Second)
	if _, _, err := p.Try("c"); !errors.Is(err, ErrBusy) {
		t.Fatalf("third with two in flight = %v, want ErrBusy", err)
	}
}

func TestCancelFreesTheSlot(t *testing.T) {
	p, h := newPaced(t, Limits{})
	slot, _, err := p.Try("a")
	if err != nil {
		t.Fatal(err)
	}
	slot.Cancel()
	h.now = h.now.Add(DefaultMinGap)
	if _, _, err := p.Try("b"); err != nil {
		t.Fatalf("after a cancelled launch: %v", err)
	}
}

// The queue is a file, so a second pacer on the same directory -- another
// process -- waits on the first one's launch.
func TestTheQueueIsSharedThroughTheDirectory(t *testing.T) {
	p, h := newPaced(t, Limits{})
	if _, _, err := p.Try("a"); err != nil {
		t.Fatal(err)
	}
	other := New(dirOf(p), Limits{}, func(string, time.Time) bool { return false })
	other.now = p.now
	other.load = p.load
	h.now = h.now.Add(10 * time.Second)
	if _, _, err := other.Try("b"); !errors.Is(err, ErrBusy) {
		t.Fatalf("other process's launch = %v, want ErrBusy", err)
	}
}

func TestWaitStopsWithItsContext(t *testing.T) {
	p, _ := newPaced(t, Limits{})
	if _, _, err := p.Try("a"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Wait(ctx, "b"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait on a cancelled context = %v", err)
	}
}

func dirOf(p *Pacer) string { return p.path[:len(p.path)-len(FileName)-1] }

// A launch the operator started without waiting holds the paced ones
// behind it.
func TestARecordedLaunchHoldsThePacedOnes(t *testing.T) {
	p, h := newPaced(t, Limits{})
	p.Record("operator")
	h.now = h.now.Add(time.Minute)
	if _, _, err := p.Try("auto"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Try behind an unsettled recorded launch = %v, want ErrBusy", err)
	}
	h.rested["operator"] = true
	if _, _, err := p.Try("auto"); err != nil {
		t.Fatalf("Try once it rests: %v", err)
	}
}
