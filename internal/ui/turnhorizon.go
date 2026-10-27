package ui

import (
	"regexp"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/scoring"
)

// The horizon term's input: how long each session's next turn is expected to
// run. For now the only estimate is plan mode, which is binary -- a session in
// plan mode is expected to run a full scoring.HorizonReference, every other
// session has no estimate -- because the turn that follows an approved plan is
// the implementation, the longest kind of turn a board runs.

// planModeTurn is the turn a session in plan mode is expected to run.
const planModeTurn = scoring.HorizonReference

// claudeModeLine is Claude Code's permission-mode footer: "⏸ plan mode on
// (shift+tab to cycle)", "⏸ manual mode on", "⏵⏵ auto mode on · 1 shell".
// The mode is the words before "mode on". The glyphs are escaped because
// they are matched, not drawn: the board's own literals stay in the widely
// drawn repertoire.
var claudeModeLine = regexp.MustCompile(`(?m)^\s*(?:\x{23F8}|\x{23F5}\x{23F5}|\x{23F5})\s+([a-z][a-z ]*?) mode on\b`)

// paneMode is the permission mode a pane's footer shows, false when it shows
// none. The footer is the bottom-most match: a transcript scrolled above it
// can quote one.
func paneMode(tool, clean string) (string, bool) {
	if tool != "claude" {
		return "", false
	}
	found := claudeModeLine.FindAllStringSubmatch(clean, -1)
	if len(found) == 0 {
		return "", false
	}
	return found[len(found)-1][1], true
}

// notePaneMode records the mode a session's capture shows. A capture with no
// footer keeps the mode last read: a dialog draws over the footer, and the
// dialog a plan ends on -- approving it -- is exactly when the session is
// about to start its long turn.
func (p *poller) notePaneMode(modes map[string]string, sess store.Session, clean string) {
	if mode, ok := paneMode(sess.Tool, clean); ok {
		modes[sess.ID] = mode
		return
	}
	if mode, ok := p.paneModes[sess.ID]; ok {
		modes[sess.ID] = mode
	}
}

// expectedTurnRows is the pass's estimates for the UI. A session is on it
// only with an estimate, so the map is nil on a board with none.
func expectedTurnRows(modes map[string]string) map[string]time.Duration {
	var out map[string]time.Duration
	for id, mode := range modes {
		if mode != "plan" {
			continue
		}
		if out == nil {
			out = map[string]time.Duration{}
		}
		out[id] = planModeTurn
	}
	return out
}
