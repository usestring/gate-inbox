package ui

import (
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
// a session filed into or out of a tiered group. The tier is store.EffectiveTier
// over the same group tiers the board sorts by, so the window follows the
// order the operator sees. The file is read every pass and written only when
// it says something else, so a board with no compact_window costs nothing and
// one with it costs a small read per session.
//
// Claude Code reads the window at launch only, so a change takes effect at
// the session's next revive or restart; nothing here restarts it.
//
// A write that fails is logged once per session and window and not retried
// until the window wanted changes: the board is not held up over a setting
// that, at worst, leaves a session compacting at the CLI's own window.
func (p *poller) syncCompactWindows(sessions []store.Session, groupTiers map[string]priority.Tier) {
	if len(p.compactTools) == 0 || p.hooks == nil {
		return
	}
	for _, sess := range sessions {
		tool, ok := p.compactTools[sess.Tool]
		if !ok || sess.Archived {
			continue
		}
		window := tool.CompactWindowFor(store.EffectiveTier(groupTiers, sess))
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
}
