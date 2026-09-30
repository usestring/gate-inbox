package hogs

import (
	"time"
)

// Tier is how firmly a session is told about what its tree is using.
type Tier int

const (
	TierNone Tier = iota
	// TierNotice asks the session to confirm the work is intended and cap it.
	TierNotice
	// TierWarn tells it to stop or cap the named processes now unless they
	// are the task's deliverable.
	TierWarn
	// TierStop tells it to stop them immediately and report what it stopped.
	TierStop
)

func (t Tier) String() string {
	switch t {
	case TierNotice:
		return "notice"
	case TierWarn:
		return "warn"
	case TierStop:
		return "stop"
	}
	return "none"
}

// Kind is the resource a rule watches.
type Kind int

const (
	KindCPU Kind = iota
	KindMemory
)

func (k Kind) String() string {
	if k == KindMemory {
		return "memory"
	}
	return "cpu"
}

// Rule is one condition that, held for For, puts a session at Tier. A tier's
// rules are alternatives: any one of them held long enough is enough.
//
// A CPU rule reads CPUPercent. A memory rule holds while every condition it
// sets is true: the tree is at least MemBytes, it grew at least
// GrowthBytesPerMin over the last growthLookback, and the host has less than
// HostAvailableBelow percent of its memory available. A zero field sets no
// condition.
type Rule struct {
	Tier               Tier
	For                time.Duration
	CPUPercent         float64
	MemBytes           uint64
	GrowthBytesPerMin  float64
	HostAvailableBelow float64
}

// Policy is the whole configuration the tracker applies.
type Policy struct {
	// ResetAfter is how long every rule of a kind must stay below its
	// threshold before the episode ends. A dip shorter than this keeps each
	// rule's window open, so a build that idles between steps is still one
	// sustained load.
	ResetAfter time.Duration
	// Cooldown is the least time between two notices of the same tier to one
	// session, within an episode or across two.
	Cooldown time.Duration
	CPU      []Rule
	Memory   []Rule
}

// growthLookback is the span a memory growth rate is measured over. Two
// samples seconds apart are dominated by allocator noise; a minute is long
// enough to call a slope a slope and short enough to see a leak start.
const growthLookback = time.Minute

// Alert is one kind reaching a tier this sample that the session should be
// told about.
type Alert struct {
	Kind Kind
	Tier Tier
	// Rule is the rule that put the session at Tier, and Held how long its
	// window has been open.
	Rule Rule
	Held time.Duration
}

// Badge is what the board draws on a row: the tier each kind's open episode
// has been told, TierNone when there is none.
type Badge struct {
	CPU    Tier
	Memory Tier
}

// Any reports whether the badge has anything to draw.
func (b Badge) Any() bool { return b.CPU != TierNone || b.Memory != TierNone }

type window struct {
	open     bool
	since    time.Time
	lastTrue time.Time
}

type kindState struct {
	windows []window
	// sent is the highest tier this episode has been told, and sentAt when.
	sent   Tier
	sentAt time.Time
	// lastSent is when each tier was last told, and outlives the episode, so
	// a load that flaps across ResetAfter is not re-told every time it
	// returns.
	lastSent map[Tier]time.Time
}

type memPoint struct {
	at    time.Time
	bytes uint64
}

type sessionState struct {
	cpu, mem kindState
	history  []memPoint
}

// Tracker turns a stream of samples into alerts. It is not safe for
// concurrent use.
type Tracker struct {
	policy   Policy
	sessions map[string]*sessionState
}

func NewTracker(policy Policy) *Tracker {
	return &Tracker{policy: policy, sessions: map[string]*sessionState{}}
}

// Observe folds one sample in and returns, per session, what to tell it now.
// A session missing from the sample has gone and its state is dropped.
func (t *Tracker) Observe(sample Sample) map[string][]Alert {
	now := sample.At
	out := map[string][]Alert{}
	for id := range t.sessions {
		if _, ok := sample.Sessions[id]; !ok {
			delete(t.sessions, id)
		}
	}
	for id, usage := range sample.Sessions {
		st := t.sessions[id]
		if st == nil {
			st = &sessionState{}
			t.sessions[id] = st
		}
		st.history = append(st.history, memPoint{at: now, bytes: usage.MemBytes})
		// Keep one point at or before the lookback edge, so the slope is
		// always measured across the whole minute once there is one.
		for len(st.history) > 1 && now.Sub(st.history[1].at) >= growthLookback {
			st.history = st.history[1:]
		}
		growth, growthOK := growthPerMin(st.history, now)

		var alerts []Alert
		if usage.CPUValid {
			if a, ok := t.step(&st.cpu, t.policy.CPU, now, func(r Rule) bool {
				return r.CPUPercent > 0 && usage.CPUPercent >= r.CPUPercent
			}); ok {
				alerts = append(alerts, Alert{Kind: KindCPU, Tier: a.Tier, Rule: a.Rule, Held: a.Held})
			}
		}
		host := sample.Host
		if a, ok := t.step(&st.mem, t.policy.Memory, now, func(r Rule) bool {
			if r.MemBytes == 0 && r.GrowthBytesPerMin == 0 {
				return false
			}
			if usage.MemBytes < r.MemBytes {
				return false
			}
			if r.GrowthBytesPerMin > 0 && (!growthOK || growth < r.GrowthBytesPerMin) {
				return false
			}
			if r.HostAvailableBelow > 0 && (!host.OK || host.AvailablePercent() >= r.HostAvailableBelow) {
				return false
			}
			return true
		}); ok {
			alerts = append(alerts, Alert{Kind: KindMemory, Tier: a.Tier, Rule: a.Rule, Held: a.Held})
		}
		if len(alerts) > 0 {
			out[id] = alerts
		}
	}
	return out
}

// growthPerMin is the tree's memory slope over the lookback, in bytes a
// minute. It is not known until the history spans at least half the lookback.
func growthPerMin(history []memPoint, now time.Time) (float64, bool) {
	if len(history) < 2 {
		return 0, false
	}
	first := history[0]
	span := now.Sub(first.at)
	if span < growthLookback/2 {
		return 0, false
	}
	last := history[len(history)-1]
	return (float64(last.bytes) - float64(first.bytes)) / span.Minutes(), true
}

// step advances one kind's rule windows and decides whether this sample
// tells the session anything.
func (t *Tracker) step(st *kindState, rules []Rule, now time.Time, holds func(Rule) bool) (Alert, bool) {
	if len(st.windows) != len(rules) {
		st.windows = make([]window, len(rules))
	}
	var best Alert
	open := false
	for i, rule := range rules {
		w := &st.windows[i]
		cond := holds(rule)
		if cond {
			if !w.open {
				w.open, w.since = true, now
			}
			w.lastTrue = now
		} else if w.open && now.Sub(w.lastTrue) >= t.policy.ResetAfter {
			*w = window{}
		}
		if w.open {
			open = true
		}
		if cond && w.open && now.Sub(w.since) >= rule.For && rule.Tier > best.Tier {
			best = Alert{Tier: rule.Tier, Rule: rule, Held: now.Sub(w.since)}
		}
	}
	if !open {
		// Every rule has been below for ResetAfter: the episode is over, and
		// the next one starts from the bottom again.
		st.sent, st.sentAt = TierNone, time.Time{}
		return Alert{}, false
	}
	if best.Tier == TierNone || best.Tier < st.sent {
		return Alert{}, false
	}
	if best.Tier == st.sent && now.Sub(st.sentAt) < t.policy.Cooldown {
		return Alert{}, false
	}
	if last, told := st.lastSent[best.Tier]; told && now.Sub(last) < t.policy.Cooldown {
		return Alert{}, false
	}
	if st.lastSent == nil {
		st.lastSent = map[Tier]time.Time{}
	}
	st.sent, st.sentAt = best.Tier, now
	st.lastSent[best.Tier] = now
	return best, true
}

// Badges is every session whose episode has been told something, for the
// board. Sessions with nothing open are left out.
func (t *Tracker) Badges() map[string]Badge {
	var out map[string]Badge
	for id, st := range t.sessions {
		badge := Badge{CPU: st.cpu.sent, Memory: st.mem.sent}
		if !badge.Any() {
			continue
		}
		if out == nil {
			out = map[string]Badge{}
		}
		out[id] = badge
	}
	return out
}
