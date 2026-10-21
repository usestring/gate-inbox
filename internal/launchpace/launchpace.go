// Package launchpace spaces out the relaunches of agent sessions that are
// already on the board: an account switch, a stale-sandbox restart, a
// revive. Every launch runs the CLI's startup hooks, its MCP servers and a
// transcript read before it reaches its prompt, so a burst of them on a
// busy machine overlaps into a pile-up. On 2026-10-20 a manager restart
// re-routed 50 sessions about one every seven seconds, and the host's
// load average climbed from an already-saturated level to 120 on 8 cores
// with a run queue of 489.
//
// The pacer admits one launch at a time (Limits.MaxInFlight), holds the
// next until the last has settled at its prompt or Limits.Settle has passed,
// and keeps a gap between starts that stretches with the host's load. Its
// state is a small file under the config directory, locked while it is read
// and written, so the board, the CLI and every session's MCP server share
// one queue.
package launchpace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FileName is the pacer's state file under the config directory.
const FileName = "relaunches.json"

// Defaults, used where Limits leaves a field zero.
const (
	DefaultMaxInFlight = 1
	DefaultMinGap      = 5 * time.Second
	DefaultMaxGap      = time.Minute
	DefaultSettle      = 90 * time.Second
	// DefaultLoadPerCPU is the one-minute load average per CPU the minimum
	// gap is set for. Above it the gap grows in proportion, up to MaxGap:
	// at 16 on 8 cores it is 5s, at 64 it is 20s, at 120 it is 37.5s, and
	// from 192 it is a minute.
	DefaultLoadPerCPU = 2.0
)

// pollEvery bounds one wait between checks, so a launch settling early is
// seen soon after it does.
const pollEvery = 2 * time.Second

// ErrBusy is Try's answer when a launch may not start yet.
var ErrBusy = errors.New("another session is relaunching; this one waits its turn")

// Limits is how launches are paced. A zero field takes its default.
type Limits struct {
	// MaxInFlight is how many launches may be unsettled at once.
	MaxInFlight int
	// MinGap is the least time between two launches starting.
	MinGap time.Duration
	// MaxGap is the most the gap stretches to on a loaded host.
	MaxGap time.Duration
	// Settle is how long a launch is waited on to reach its prompt before
	// the next is let go anyway.
	Settle time.Duration
	// LoadPerCPU is the load average per CPU above which the gap stretches.
	LoadPerCPU float64
}

func (l Limits) withDefaults() Limits {
	if l.MaxInFlight <= 0 {
		l.MaxInFlight = DefaultMaxInFlight
	}
	if l.MinGap <= 0 {
		l.MinGap = DefaultMinGap
	}
	if l.MaxGap <= 0 {
		l.MaxGap = DefaultMaxGap
	}
	l.MaxGap = max(l.MaxGap, l.MinGap)
	if l.Settle <= 0 {
		l.Settle = DefaultSettle
	}
	if l.LoadPerCPU <= 0 {
		l.LoadPerCPU = DefaultLoadPerCPU
	}
	return l
}

// Gap is the time between two launch starts at a load average per CPU of
// load: MinGap up to LoadPerCPU, then in proportion to it, at most MaxGap.
func (l Limits) Gap(load float64) time.Duration {
	l = l.withDefaults()
	if load <= l.LoadPerCPU {
		return l.MinGap
	}
	return min(time.Duration(float64(l.MinGap)*load/l.LoadPerCPU), l.MaxGap)
}

// Settled says whether the session launched at since has come to rest. It
// is asked only of launches still in flight. A nil Settled lets a launch go
// only once Limits.Settle has passed.
type Settled func(id string, since time.Time) bool

// Pacer admits relaunches one slot at a time.
type Pacer struct {
	path    string
	limits  Limits
	settled Settled
	// now, sleep and load are the clock, the wait and the host's one-minute
	// load average per CPU; tests replace them.
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	load  func() (float64, bool)
}

// New is the pacer whose state lives in dir.
func New(dir string, limits Limits, settled Settled) *Pacer {
	return &Pacer{
		path:    filepath.Join(dir, FileName),
		limits:  limits.withDefaults(),
		settled: settled,
		now:     time.Now,
		sleep:   sleepCtx,
		load:    hostLoad,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// launch is one admitted launch still waited on.
type launch struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

type state struct {
	// Last is when the newest launch was admitted.
	Last     time.Time `json:"last"`
	InFlight []launch  `json:"in_flight"`
}

// Slot is an admitted launch. Cancel gives it back when the launch did not
// happen, so the next is not held for one that never started.
type Slot struct {
	p  *Pacer
	id string
	at time.Time
}

// Cancel drops the slot from the queue. A zero Slot ignores it.
func (s Slot) Cancel() {
	if s.p == nil {
		return
	}
	_ = s.p.update(func(st *state) {
		for i, l := range st.InFlight {
			if l.ID == s.id && l.At.Equal(s.at) {
				st.InFlight = append(st.InFlight[:i], st.InFlight[i+1:]...)
				break
			}
		}
	})
}

// Try admits a launch of id now, or says ErrBusy and how long until the
// next look is worth making.
func (p *Pacer) Try(id string) (Slot, time.Duration, error) {
	var (
		slot Slot
		wait time.Duration
	)
	err := p.update(func(st *state) {
		now := p.now()
		kept := st.InFlight[:0]
		for _, l := range st.InFlight {
			if l.ID == id || now.Sub(l.At) >= p.limits.Settle || now.Before(l.At) || p.settled != nil && p.settled(l.ID, l.At) {
				continue
			}
			kept = append(kept, l)
		}
		st.InFlight = kept
		load, ok := p.load()
		if !ok {
			load = 0
		}
		gapLeft := time.Duration(0)
		if !st.Last.IsZero() && !now.Before(st.Last) {
			gapLeft = st.Last.Add(p.limits.Gap(load)).Sub(now)
		}
		switch {
		case len(st.InFlight) >= p.limits.MaxInFlight:
			wait = pollEvery
			for _, l := range st.InFlight {
				wait = min(wait, max(l.At.Add(p.limits.Settle).Sub(now), time.Millisecond))
			}
			wait = max(wait, gapLeft)
		case gapLeft > 0:
			wait = gapLeft
		default:
			st.Last = now
			st.InFlight = append(st.InFlight, launch{ID: id, At: now})
			slot = Slot{p: p, id: id, at: now}
		}
	})
	if err != nil {
		// The queue file cannot be read or written: pacing fails open, since
		// refusing every relaunch would be worse than an unpaced one.
		return Slot{}, 0, nil
	}
	if slot.p == nil {
		return Slot{}, wait, ErrBusy
	}
	return slot, 0, nil
}

// Record enters a launch of id that has already started without waiting --
// one the operator asked for on the board, whose screen cannot block -- so
// the paced launches behind it wait on it all the same.
func (p *Pacer) Record(id string) {
	_ = p.update(func(st *state) {
		now := p.now()
		st.Last = now
		st.InFlight = append(st.InFlight, launch{ID: id, At: now})
	})
}

// Wait admits a launch of id once the queue lets it, or returns ctx's error.
func (p *Pacer) Wait(ctx context.Context, id string) (Slot, error) {
	for {
		slot, wait, err := p.Try(id)
		if !errors.Is(err, ErrBusy) {
			return slot, err
		}
		if err := p.sleep(ctx, min(wait, pollEvery)); err != nil {
			return Slot{}, err
		}
	}
}

// update rewrites the state file under its lock.
func (p *Pacer) update(fn func(*state)) error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return err
	}
	unlock, err := lockFile(p.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	var st state
	if raw, err := os.ReadFile(p.path); err == nil {
		// A broken file is an empty queue: the worst it costs is one
		// launch that was not waited for.
		_ = json.Unmarshal(raw, &st)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fn(&st)
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p.path); err != nil {
		return fmt.Errorf("relaunch queue: %w", err)
	}
	return nil
}
