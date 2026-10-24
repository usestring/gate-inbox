package ui

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/priority"
	"github.com/usestring/gate-inbox/internal/store"
)

// syncCompactWindows holds each session's sizing tier and keeps each claude
// session's own settings file carrying the auto-compact window that tier is
// configured for (compact_window).
//
// It runs on every pass rather than at each place a tier is written, because
// a tier moves by more roads than one: the p key on a session or on a group
// above it, a declaration through the priority command or its MCP tool, and
// a session filed into or out of a tiered group. The tier is
// store.InheritedTier over the same group tiers the board sorts by, so a
// child sized by its parent's tier changes when that parent's does.
//
// A raise applies on the pass that sees it; a drop waits out the tool's
// compact_downgrade_delay (store.SizingHold). The holds are what extensions
// read off the pass as SizingTier, so a board extension that compacts by tier
// follows the same tier, held the same way, as the window here. They are kept
// in the store with the time each wait began, so a board restarted mid-wait
// finishes it on time, rather than skipping it or starting it over, which
// would let restarts hold a drop off for good.
//
// The settings file is read every pass and written only when it says
// something else, so a board with no compact_window costs nothing and one
// with it costs a small read per session. Claude Code reads the window at
// launch only, so a change takes effect at the session's next revive or
// restart; nothing here restarts it.
//
// A write that fails is logged once per session and window and not retried
// until the window wanted changes: the board is not held up over a setting
// that, at worst, leaves a session compacting at the CLI's own window.
func (p *poller) syncCompactWindows(sessions []store.Session, groupTiers map[string]priority.Tier) {
	p.loadSizingHolds()
	before := maps.Clone(p.sizingHolds)
	now := p.compactNow()
	byID := make(map[string]store.Session, len(sessions))
	for _, sess := range sessions {
		byID[sess.ID] = sess
	}
	lookup := func(id string) (store.Session, bool) {
		sess, ok := byID[id]
		return sess, ok
	}
	sizing := make(map[string]store.Sizing, len(sessions))
	views := map[string]compactView{}
	for _, sess := range sessions {
		if sess.Archived {
			continue
		}
		delay := p.downgradeDelay(sess.Tool)
		held, known := p.sizingHolds[sess.ID]
		held = held.Step(store.InheritedTier(groupTiers, sess, lookup), known, now, delay)
		p.sizingHolds[sess.ID] = held
		sized := held.Sizing(delay)
		sizing[sess.ID] = sized
		tool, ok := p.compactTools[sess.Tool]
		if !ok || p.hooks == nil {
			continue
		}
		window := tool.CompactWindowFor(sized.Tier)
		view := compactView{Window: window}
		view.Launched, view.LaunchKnown = p.hooks.LaunchedCompactWindow(sess.ID)
		if to := tool.CompactWindowFor(sized.LowersTo); sized.LowersAt.After(now) && to != window {
			view.PendingTo, view.PendingAt = to, sized.LowersAt
		}
		if view.Window != 0 || view.Pending() || view.Launched != 0 {
			views[sess.ID] = view
		}
		have, err := p.hooks.SessionCompactWindow(sess.ID)
		if err != nil {
			logging.Warn("compact window not read", "session", sess.ID, logging.Err(err))
			continue
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
	p.sizing = sizing
	p.compactViews = views
	if !maps.Equal(before, p.sizingHolds) {
		p.saveSizingHolds()
	}
}

func (p *poller) downgradeDelay(tool string) time.Duration {
	if delay, ok := p.downgradeDelays[tool]; ok {
		return delay
	}
	return config.DefaultCompactDowngradeDelay
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

// compactFact is the rail's auto-compact line: the window the session runs
// on, the one its settings file holds for the next launch when that differs
// ("now · … after restart", since Claude Code reads the window at launch
// only), and a downgrade counting down to its write.
func compactFact(v compactView, now time.Time) string {
	running := v.Window
	if v.LaunchKnown {
		running = v.Launched
	}
	fact := compactTokens(v.Window)
	if running != v.Window {
		fact = compactTokens(running) + " now · " + compactTokens(v.Window) + " after restart"
	}
	if v.Pending() {
		fact += " · lowers to " + compactTokens(v.PendingTo) + " in " + countdown(v.PendingAt.Sub(now))
	}
	return fact
}

// compactLabel is the detail line that says where a session compacts.
const compactLabel = "auto-compact"

// wideFact is a detail line whose label is wider than the column the others
// share, so the value follows the label rather than the column, and wraps at
// its " · " breaks: cut short, it would lose its end, where a countdown sits.
func wideFact(pad, label, text string, room int) []string {
	label = textfmt.TruncateWidth(label, max(room/2, 1), "…") + "  "
	valueRoom := max(room-textfmt.Width(label), 1)
	lead := pad + labelStyle.Render(label)
	var lines []string
	for _, part := range wrapParts(strings.Split(text, " · "), " · ", valueRoom) {
		lines = append(lines, lead+textfmt.TruncateWidth(part, valueRoom, "…"))
		lead = pad + spaces(textfmt.Width(label))
	}
	return lines
}

// extensionFactText is an extension's line as painted at now: its Then
// counted down while its At is ahead.
func extensionFactText(fact extension.Fact, now time.Time) string {
	if fact.Then == "" || !fact.At.After(now) {
		return fact.Text
	}
	return fact.Text + " · " + fact.Then + " in " + countdown(fact.At.Sub(now))
}

// wrapParts joins parts with sep into as few lines no wider than width as it
// can. A part too wide for a line of its own is broken between its words.
func wrapParts(parts []string, sep string, width int) []string {
	var lines []string
	add := func(piece, glue string) {
		if n := len(lines); n > 0 && textfmt.Width(lines[n-1]+glue+piece) <= width {
			lines[n-1] += glue + piece
			return
		}
		lines = append(lines, piece)
	}
	for _, part := range parts {
		if textfmt.Width(part) <= width {
			add(part, sep)
			continue
		}
		for i, word := range strings.Fields(part) {
			glue := " "
			if i == 0 {
				glue = sep
			}
			add(word, glue)
		}
	}
	return lines
}

// compactTokens reads a window the way compact_window is written, 0 being
// the CLI's own.
func compactTokens(n int) string {
	switch {
	case n == 0:
		return "claude default"
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

// sizingHoldsSetting is where the sizing holds are kept between board runs.
const sizingHoldsSetting = "sizing_holds"

func (p *poller) compactNow() time.Time {
	if p.compactClock != nil {
		return p.compactClock()
	}
	return time.Now()
}

func (p *poller) loadSizingHolds() {
	if p.sizingHolds != nil {
		return
	}
	p.sizingHolds = map[string]store.SizingHold{}
	if p.store == nil {
		return
	}
	raw, err := p.store.Setting(sizingHoldsSetting)
	if err != nil || raw == "" {
		return
	}
	if err := json.Unmarshal([]byte(raw), &p.sizingHolds); err != nil {
		logging.Warn("sizing holds not read", logging.Err(err))
		p.sizingHolds = map[string]store.SizingHold{}
	}
}

func (p *poller) saveSizingHolds() {
	if p.store == nil {
		return
	}
	raw, err := json.Marshal(p.sizingHolds)
	if err == nil {
		err = p.store.SetSetting(sizingHoldsSetting, string(raw))
	}
	if err != nil {
		logging.Warn("sizing holds not saved", logging.Err(err))
	}
}
