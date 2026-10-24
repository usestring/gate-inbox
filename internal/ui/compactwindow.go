package ui

import (
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/store"
)

// syncCompactWindows keeps each claude session's own settings file carrying
// the auto-compact window its tier is configured for (compact_window).
//
// It runs on every pass rather than at each place a tier is written, because
// a tier moves by more roads than one: the p key on a session or on a group
// above it, a declaration through the priority command or its MCP tool, and
// a session filed into or out of a tiered group. The tier is
// store.InheritedTier over the same group tiers the board sorts by, so a
// child sized by its parent's tier changes when that parent's does. The file is read every pass and written only when
// it says something else, so a board with no compact_window costs nothing and
// one with it costs a small read per session.
//
// A larger window is written on the pass that sees it. A smaller one, the
// CLI's own counting as smallest, waits out compact_downgrade_delay from the
// first pass that wanted it, and a tier put back up before then drops the
// wait: a tier lowered by mistake should not cost a session its context.
// The waits are kept in the store with the time each began, so a board
// restarted mid-wait finishes it on time, rather than skipping it or starting
// it over, which would let restarts hold a downgrade off for good.
//
// Claude Code reads the window at launch only, so a change takes effect at
// the session's next revive or restart; nothing here restarts it.
//
// A write that fails is logged once per session and window and not retried
// until the window wanted changes: the board is not held up over a setting
// that, at worst, leaves a session compacting at the CLI's own window.
func (p *poller) syncCompactWindows(sessions []store.Session, groupTiers map[string]priority.Tier) {
	if len(p.compactTools) == 0 || p.hooks == nil {
		p.compactViews = nil
		return
	}
	p.loadCompactPending()
	before := maps.Clone(p.compactPending)
	now := p.compactNow()
	byID := make(map[string]store.Session, len(sessions))
	for _, sess := range sessions {
		byID[sess.ID] = sess
	}
	lookup := func(id string) (store.Session, bool) {
		sess, ok := byID[id]
		return sess, ok
	}
	views := map[string]compactView{}
	for _, sess := range sessions {
		tool, ok := p.compactTools[sess.Tool]
		if !ok || sess.Archived {
			continue
		}
		want := tool.CompactWindowFor(store.InheritedTier(groupTiers, sess, lookup))
		have, err := p.hooks.SessionCompactWindow(sess.ID)
		if err != nil {
			logging.Warn("compact window not read", "session", sess.ID, logging.Err(err))
			continue
		}
		delay := tool.CompactDowngradeDelayOrDefault()
		window := want
		if want < have {
			pending, ok := p.compactPending[sess.ID]
			if !ok {
				pending = compactDowngrade{Since: now}
			}
			pending.To = want
			if now.Before(pending.Since.Add(delay)) {
				p.compactPending[sess.ID] = pending
				window = have
			} else {
				delete(p.compactPending, sess.ID)
			}
		} else {
			delete(p.compactPending, sess.ID)
		}
		view := compactView{Window: window}
		view.Launched, view.LaunchKnown = p.hooks.LaunchedCompactWindow(sess.ID)
		if pending, ok := p.compactPending[sess.ID]; ok {
			view.PendingTo = pending.To
			view.PendingAt = pending.Since.Add(delay)
		}
		if view.Window != 0 || view.Pending() || view.Launched != 0 {
			views[sess.ID] = view
		}
		if window == have {
			delete(p.compactFailed, sess.ID)
			continue
		}
		if failed, ok := p.compactFailed[sess.ID]; ok && failed == window {
			continue
		}
		if err := p.hooks.SetSessionCompactWindow(sess.ID, window); err != nil {
			if p.compactFailed == nil {
				p.compactFailed = map[string]int{}
			}
			p.compactFailed[sess.ID] = window
			logging.Warn("compact window not written", "session", sess.ID, "window", window, logging.Err(err))
			continue
		}
		delete(p.compactFailed, sess.ID)
	}
	p.compactViews = views
	if !maps.Equal(before, p.compactPending) {
		p.saveCompactPending()
	}
}

// compactDowngrade is a smaller window waiting out the downgrade delay: the
// window it will write and when the wait began.
type compactDowngrade struct {
	To    int       `json:"to"`
	Since time.Time `json:"since"`
}

// compactView is what the rail says about a session's window: the one its
// settings file carries, the one it launched on, and a downgrade waiting to
// be written.
type compactView struct {
	Window      int
	Launched    int
	LaunchKnown bool
	PendingTo   int
	PendingAt   time.Time
}

func (v compactView) Pending() bool { return !v.PendingAt.IsZero() }

// compactFact is the rail's compact line: the window, a downgrade counting
// down to its write, and "at restart" while the running process is on a
// window other than the one it is headed for, since Claude Code reads the
// window at launch only.
func compactFact(v compactView, now time.Time) string {
	running := v.Window
	if v.LaunchKnown {
		running = v.Launched
	}
	target := v.Window
	var fact string
	switch {
	case v.Pending():
		target = v.PendingTo
		fact = compactTokens(v.Window) + " → " + compactTokens(v.PendingTo) + " in " + countdown(v.PendingAt.Sub(now))
	case running != v.Window:
		fact = compactTokens(running) + " → " + compactTokens(v.Window)
	default:
		fact = compactTokens(v.Window)
	}
	if running != target {
		fact += " at restart"
	}
	return fact
}

// compactTokens reads a window the way compact_window is written, 0 being
// the CLI's own.
func compactTokens(n int) string {
	switch {
	case n == 0:
		return "default"
	case n%1_000_000 == 0:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n%1000 == 0:
		return fmt.Sprintf("%dk", n/1000)
	}
	return humanTokens(n)
}

// countdown is a wait left, rounded up so it never reads 0m while there is
// time still to go.
func countdown(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int((d+time.Second-1)/time.Second))
	}
	minutes := int((d + time.Minute - 1) / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%02dm", minutes/60, minutes%60)
}

// compactPendingSetting is where the waiting downgrades are kept between
// board runs.
const compactPendingSetting = "compact_downgrades"

func (p *poller) compactNow() time.Time {
	if p.compactClock != nil {
		return p.compactClock()
	}
	return time.Now()
}

func (p *poller) loadCompactPending() {
	if p.compactPending != nil {
		return
	}
	p.compactPending = map[string]compactDowngrade{}
	if p.store == nil {
		return
	}
	raw, err := p.store.Setting(compactPendingSetting)
	if err != nil || raw == "" {
		return
	}
	if err := json.Unmarshal([]byte(raw), &p.compactPending); err != nil {
		logging.Warn("compact downgrades not read", logging.Err(err))
		p.compactPending = map[string]compactDowngrade{}
	}
}

func (p *poller) saveCompactPending() {
	if p.store == nil {
		return
	}
	raw, err := json.Marshal(p.compactPending)
	if err == nil {
		err = p.store.SetSetting(compactPendingSetting, string(raw))
	}
	if err != nil {
		logging.Warn("compact downgrades not saved", logging.Err(err))
	}
}
