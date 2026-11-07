package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

type fakeUnparkSource struct {
	parkedAt  time.Time
	unparkErr error

	mu    sync.Mutex
	calls []sessioncmd.UnparkOptions
}

func (f *fakeUnparkSource) ParkedAt() (time.Time, error) { return f.parkedAt, nil }

func (f *fakeUnparkSource) Unpark(_ string, opts sessioncmd.UnparkOptions) (sessioncmd.UnparkResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, opts)
	return sessioncmd.UnparkResult{Revived: []sessioncmd.Session{{ID: "a"}}, Dormant: []string{"b"}}, f.unparkErr
}

func (f *fakeUnparkSource) seen() []sessioncmd.UnparkOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sessioncmd.UnparkOptions(nil), f.calls...)
}

func TestUnparkInFlightOnlyAfterTheHostRebootedSinceThePark(t *testing.T) {
	t.Parallel()
	parked := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name      string
		parkedAt  time.Time
		bootedAt  time.Time
		bootErr   error
		unparkErr error
		wantCalls int
	}{
		{name: "rebooted since the park", parkedAt: parked, bootedAt: parked.Add(time.Minute), wantCalls: 1},
		// A board restarted between park and reboot must not hand the
		// sessions to the reboot.
		{name: "board restarted before the reboot", parkedAt: parked, bootedAt: parked.Add(-time.Hour), wantCalls: 0},
		{name: "nothing ever parked", bootedAt: parked, wantCalls: 0},
		{name: "boot time unreadable", parkedAt: parked, bootErr: errors.New("no /proc"), wantCalls: 0},
		{name: "a failed unpark is logged, not retried", parkedAt: parked, bootedAt: parked.Add(time.Minute), unparkErr: errors.New("store locked"), wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := &fakeUnparkSource{parkedAt: tc.parkedAt, unparkErr: tc.unparkErr}
			booted := func() (time.Time, error) { return tc.bootedAt, tc.bootErr }
			done := make(chan struct{})
			go func() {
				unparkInFlight(context.Background(), src, booted, 10*time.Millisecond)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("unparkInFlight did not return")
			}
			calls := src.seen()
			if len(calls) != tc.wantCalls {
				t.Fatalf("unpark calls = %d, want %d", len(calls), tc.wantCalls)
			}
			// Always a plain unpark: the dormant sessions are not the board's to wake.
			for _, call := range calls {
				if call.All {
					t.Fatalf("the board ran unpark --all: %+v", calls)
				}
			}
		})
	}
}

func TestUnparkInFlightDoesNothingOnceTheBoardStops(t *testing.T) {
	t.Parallel()
	src := &fakeUnparkSource{parkedAt: time.Unix(1, 0)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	unparkInFlight(ctx, src, func() (time.Time, error) { return time.Now(), nil }, time.Hour)
	if calls := src.seen(); len(calls) != 0 {
		t.Fatalf("a board that stopped before the delay unparked: %+v", calls)
	}
}

func TestHostBootedAtIsInThePast(t *testing.T) {
	t.Parallel()
	booted, err := hostBootedAt()
	if err != nil {
		t.Skipf("boot time unreadable here: %v", err)
	}
	if booted.IsZero() || !booted.Before(time.Now()) {
		t.Fatalf("booted at %v", booted)
	}
}
