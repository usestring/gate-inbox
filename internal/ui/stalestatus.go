package ui

import (
	"hash/fnv"
	"time"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A status is the board's reading of a screen, and a reading can be wrong in
// a way nothing else notices: a pane sitting at a prompt that the rules still
// call working reads working forever, and every tool that keys on status --
// delivery, triage, a parent waiting on its child -- believes it.
//
// What gives a misread away is that nothing moves. A turn that is really
// running paints: output scrolls, a spinner turns, an elapsed timer ticks. So
// a working or starting label that has held over an unchanged screen for far
// longer than any turn takes is flagged stale on its row, and triage offers
// the row up so a person can look. The label itself is left alone: the flag
// says the reading is suspect, not what the right reading is.

// defaultStaleStatusAfter stands in for the configured threshold on a poller
// built without one.
const defaultStaleStatusAfter = 2 * time.Hour

// staleable are the labels that claim something is happening. A resting label
// over a still screen is exactly what resting looks like.
var staleable = map[string]bool{
	status.Working:  true,
	status.Starting: true,
}

// screenMark is the last screen a session showed, the status it was read as,
// and when that pair was first seen.
type screenMark struct {
	hash   uint64
	status string
	since  time.Time
}

func screenHash(clean string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(clean))
	return h.Sum64()
}

// noteScreen folds one pass's reading of a session into its screen mark and
// records the session as stale when the mark has outlived the threshold.
// Any change to the screen or to the status starts the clock again.
func (p *poller) noteScreen(sess store.Session, derived, clean string, now time.Time) {
	if p.screens == nil {
		p.screens = map[string]screenMark{}
	}
	if p.stale == nil {
		p.stale = map[string]bool{}
	}
	hash := screenHash(clean)
	mark, seen := p.screens[sess.ID]
	if !seen || mark.hash != hash || mark.status != derived {
		p.screens[sess.ID] = screenMark{hash: hash, status: derived, since: now}
		return
	}
	if !staleable[derived] {
		return
	}
	after := p.staleAfter
	if after <= 0 {
		after = defaultStaleStatusAfter
	}
	if now.Sub(mark.since) >= after {
		p.stale[sess.ID] = true
	}
}

// staleRows copies the pass's findings for the UI, for the reason
// hooklessRows copies its own.
func (p *poller) staleRows() map[string]bool {
	if len(p.stale) == 0 {
		return nil
	}
	out := make(map[string]bool, len(p.stale))
	for id := range p.stale {
		out[id] = true
	}
	return out
}

// isStale reports whether this session's status has been flagged as having
// outlived its screen. One definition, read by the row and by triage, so the
// two cannot disagree. It needs no lapse rule: the poller rebuilds the set
// every pass, and the first change on the screen clears it.
func (m *Model) isStale(sess store.Session) bool {
	return m.stale[sess.ID]
}
