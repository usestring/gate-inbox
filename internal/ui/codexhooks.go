package ui

import (
	"slices"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/hooks"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// The global codex hooks (hooks/codexglobal.go) answer for a thread only
// once the board says which row it belongs to, and find the pane of a
// thread nobody has claimed yet from the board's list of adopted codex
// rows (hooks.SyncCodex). This keeps that list in step with the board: every
// live codex row, launched or adopted, and nothing else.

// codexHooksEvery bounds how stale the list can be when nothing on the board
// changed, which only matters for a file someone deleted by hand.
const codexHooksEvery = 15 * time.Second

// codexHookFresh is how long a codex hook's newest event outranks the pane.
// The two events it hears are a turn's start and its end, and neither has a
// later event to correct it -- an Esc interrupt fires no Stop -- so after
// this the pane's own rules, which see the turn as it runs, take over.
const codexHookFresh = 15 * time.Second

type codexHooksState struct {
	key    string
	last   time.Time
	synced bool
}

func (p *poller) isCodex(sess store.Session) bool {
	return p.sessionStores[sess.Tool] == "codex"
}

// syncCodexHooks publishes the board's live codex rows for the hooks, when
// they changed or the last write is old.
func (p *poller) syncCodexHooks(sessions []store.Session, panes map[string]int, now time.Time) {
	if p.hooks == nil {
		return
	}
	var rows []hooks.CodexRow
	var parts []string
	for _, sess := range sessions {
		if sess.Archived || sess.Status == status.Dead || panes[sess.ID] <= 0 || !p.isCodex(sess) {
			continue
		}
		adopted := sess.TmuxPaneID != ""
		if !adopted && sess.AgentSessionID == "" {
			continue
		}
		rows = append(rows, hooks.CodexRow{
			ID: sess.ID, Thread: sess.AgentSessionID, Adopted: adopted,
			Socket: sess.TmuxSocket, Pane: sess.TmuxPaneID, Cwd: sess.Cwd,
		})
		parts = append(parts, sess.ID+"@"+sess.AgentSessionID+"@"+sess.TmuxSocket+sess.TmuxPaneID+"@"+sess.Cwd)
	}
	slices.Sort(parts)
	key := strings.Join(parts, ",")
	state := &p.codexHooks
	if state.synced && key == state.key && (len(rows) == 0 || now.Sub(state.last) < codexHooksEvery) {
		return
	}
	state.synced, state.key, state.last = true, key, now
	if err := p.hooks.SyncCodex(rows); err != nil {
		logging.Warn("codex hook rows not synced", logging.Err(err))
	}
}

// codexHookStatus is the status a codex row's hooks give this pass, or ""
// when they say nothing the pane should not decide. A turn that ran and
// ended between two passes reads as working once, as it does for Claude
// Code; a fresh event is cross-checked against the pane as a Claude hook's
// is, so a dialog or an error the pane shows still wins.
func (p *poller) codexHookStatus(sess store.Session, text string, displaced bool) string {
	if p.hooks == nil || !p.isCodex(sess) {
		return ""
	}
	hookStatus, age, ok := p.hooks.CodexHookStatus(sess.ID)
	if !ok {
		return ""
	}
	if p.missedTurn(sess, hookStatus) {
		return status.Working
	}
	if age > codexHookFresh {
		return ""
	}
	return p.applyHookStatus(sess, text, hookStatus, displaced)
}
