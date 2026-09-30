package ui

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/hogs"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// hogWatch reads every live session's process tree on its own cadence, off
// the poll pass, and queues a notice for a session whose tree has been holding
// the machine's CPU or memory for long enough.
//
// The notice goes through the session's inbox like any cross-session message,
// so it is typed in only when the pass's delivery gates allow: never over a
// dialog, a line the operator is part way through, or a paste already on its
// way. Gate Inbox never signals a process itself.
type hogWatch struct {
	every   time.Duration
	sampler *hogs.Sampler
	tracker *hogs.Tracker
	// enqueue queues one notice; the store's own in production.
	enqueue func(store.InboxMessage) error

	// busy holds a sample in flight, so a slow read never stacks a second
	// one behind it.
	busy atomic.Bool

	mu     sync.Mutex
	lastAt time.Time
	badges map[string]hogs.Badge
}

// hogTarget is one live session as the watcher needs it, copied off the
// pass so the sample never reads the pass's slice.
type hogTarget struct {
	id   string
	root int
	// quiet sessions are sampled and badged but never messaged: a terminal
	// tab has no agent to read a notice.
	quiet bool
	// interrupt is whether the session's tool can have its turn stopped, so
	// a stop-tier notice is read now rather than after the step in hand.
	interrupt bool
}

// newHogWatch builds the watcher cfg describes, or nil when it is switched
// off or this platform has no /proc to read.
func newHogWatch(cfg config.Hogs, procRoot string, enqueue func(store.InboxMessage) error) *hogWatch {
	if !cfg.On() {
		return nil
	}
	sampler := hogs.NewSampler(procRoot)
	if !sampler.Supported() {
		return nil
	}
	policy := hogPolicy(cfg)
	sampler.SetPSSFloor(pssFloor(policy))
	return &hogWatch{
		every:   cfg.SampleEvery.Duration,
		sampler: sampler,
		tracker: hogs.NewTracker(policy),
		enqueue: enqueue,
	}
}

// hogPolicy converts the config's tiers. A rule that sets no condition it
// can test would hold for nothing or everything, so it is dropped with a
// warning rather than guessed at.
func hogPolicy(cfg config.Hogs) hogs.Policy {
	policy := hogs.Policy{ResetAfter: cfg.ResetAfter.Duration, Cooldown: cfg.Cooldown.Duration}
	tiers := func(t config.HogTiers, kind hogs.Kind, dst *[]hogs.Rule) {
		for _, tier := range []struct {
			tier  hogs.Tier
			rules []config.HogRule
		}{{hogs.TierNotice, t.Notice}, {hogs.TierWarn, t.Warn}, {hogs.TierStop, t.Stop}} {
			for _, r := range tier.rules {
				rule := hogs.Rule{
					Tier:               tier.tier,
					For:                r.For.Duration,
					CPUPercent:         r.Percent,
					MemBytes:           uint64(r.GiB * (1 << 30)),
					GrowthBytesPerMin:  r.GrowthGiBPerMin * (1 << 30),
					HostAvailableBelow: r.AvailableBelow,
				}
				if kind == hogs.KindCPU && rule.CPUPercent <= 0 ||
					kind == hogs.KindMemory && rule.MemBytes == 0 && rule.GrowthBytesPerMin <= 0 {
					logging.Warn("hogs: a rule with no threshold is ignored", "kind", kind.String(), "tier", tier.tier.String())
					continue
				}
				*dst = append(*dst, rule)
			}
		}
	}
	tiers(cfg.CPU, hogs.KindCPU, &policy.CPU)
	tiers(cfg.Memory, hogs.KindMemory, &policy.Memory)
	return policy
}

// pssFloor is the smallest tree size any memory rule tests. A tree under it
// is under every size rule whether it is read as RSS or as the smaller PSS,
// so only a tree at or over it pays for the precise reading.
func pssFloor(policy hogs.Policy) uint64 {
	floor := uint64(math.MaxUint64)
	for _, r := range policy.Memory {
		if r.MemBytes > 0 {
			floor = min(floor, r.MemBytes)
		}
	}
	return floor
}

// hogTargets picks the sessions the watcher reads this pass.
func (p *poller) hogTargets(sessions []store.Session, panes map[string]int) []hogTarget {
	var out []hogTarget
	for _, sess := range sessions {
		if sess.Archived || sess.Status == status.Dead || panes[sess.ID] <= 0 {
			continue
		}
		out = append(out, hogTarget{
			id:        sess.ID,
			root:      panes[sess.ID],
			quiet:     p.shellTools[sess.Tool],
			interrupt: len(p.interruptKeys[sess.Tool]) > 0,
		})
	}
	return out
}

// offer hands the watcher this pass's live sessions. It returns at once: a
// sample is started in the background when one is due and none is running.
func (w *hogWatch) offer(now time.Time, targets []hogTarget) {
	if w == nil {
		return
	}
	w.mu.Lock()
	due := w.lastAt.IsZero() || now.Sub(w.lastAt) >= w.every
	w.mu.Unlock()
	if !due || !w.busy.CompareAndSwap(false, true) {
		return
	}
	w.mu.Lock()
	w.lastAt = now
	w.mu.Unlock()
	go func() {
		defer w.busy.Store(false)
		w.run(now, targets)
	}()
}

// run takes one sample and queues whatever it calls for.
func (w *hogWatch) run(now time.Time, targets []hogTarget) {
	roots := make(map[string]int, len(targets))
	byID := make(map[string]hogTarget, len(targets))
	for _, t := range targets {
		roots[t.id] = t.root
		byID[t.id] = t
	}
	sample := w.sampler.Sample(now, roots)
	alerts := w.tracker.Observe(sample)
	badges := w.tracker.Badges()
	w.mu.Lock()
	w.badges = badges
	w.mu.Unlock()
	for id, raised := range alerts {
		target := byID[id]
		usage := sample.Sessions[id]
		tier := hogs.Highest(raised)
		logging.Info("hogs: session over its resource tier",
			"session", id, "tier", tier.String(), "cpu_percent", usage.CPUPercent, "mem_bytes", usage.MemBytes,
			"host_available_percent", sample.Host.AvailablePercent())
		if target.quiet {
			continue
		}
		w.sampler.Name(&usage)
		body := hogs.Compose(raised, usage, sample.Host)
		err := w.enqueue(store.InboxMessage{
			SessionID:   id,
			SenderID:    store.SystemSenderID,
			SenderName:  "Gate Inbox",
			Body:        body,
			Fingerprint: textfmt.Fingerprint(body),
			// One live notice per session: a firmer one replaces a softer
			// one the session has not read yet.
			Subject:   hogs.Subject,
			SentAt:    now,
			Interrupt: tier == hogs.TierStop && target.interrupt,
		})
		if err != nil && !errors.Is(err, store.ErrInboxDuplicate) {
			logging.Warn("hogs: notice not queued", "session", id, "tier", tier.String(), logging.Err(err))
		}
	}
}

// badgeRows copies the latest badges for the UI.
func (w *hogWatch) badgeRows() map[string]hogs.Badge {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.badges) == 0 {
		return nil
	}
	out := make(map[string]hogs.Badge, len(w.badges))
	for id, b := range w.badges {
		out[id] = b
	}
	return out
}

// hogBadge is the word a row wears beside its status while one of its
// resource episodes has been told something, tinted by the firmest tier.
func hogBadge(b hogs.Badge) string {
	if !b.Any() {
		return ""
	}
	label := "cpu hog"
	switch {
	case b.CPU != hogs.TierNone && b.Memory != hogs.TierNone:
		label = "cpu+mem hog"
	case b.Memory != hogs.TierNone:
		label = "mem hog"
	}
	tint := status.Waiting
	if max(b.CPU, b.Memory) >= hogs.TierWarn {
		tint = status.Errored
	}
	return statusTint(tint, label)
}
