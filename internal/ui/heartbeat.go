package ui

import (
	"strconv"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// runBeats writes the liveness stamps the poll pass hands over, one at a
// time, until the poller's beat channel closes.
//
// It writes only what a pass gave it. A stamp of its own would say a manager
// is home whenever this process is, and that is not what the row means: the
// session tools read it to tell a sender whether anything will deliver what
// it queued, and only a pass delivers. A poll loop that has stopped has to
// stop this row with it.
func (p *poller) runBeats(st *store.Store) {
	for stamped := range p.beats {
		if err := st.SetSetting(store.PollerHeartbeatKey, strconv.FormatInt(stamped.UnixNano(), 10)); err != nil {
			// A stamp that did not land costs a sender an accurate answer
			// about whether the manager is running, and nothing else. The
			// next pass brings another one.
			logging.Warn("liveness stamp failed", "err", err)
		}
	}
}

// beatWriter is the handle those stamps are written through: its own
// connection to the same file. The shared store keeps a single connection,
// so a stamp waiting out another process's write lock would hold it against
// the pass's own queries -- which is the stall that moving the write off the
// pass is meant to end, arriving one step later.
//
// A connection it cannot open costs liveness reporting, not the board, so it
// falls back to the shared one rather than leaving the row unstamped.
func (p *poller) beatWriter() *store.Store {
	own, err := p.store.Reopen()
	if err != nil {
		logging.Warn("liveness stamps fall back to the shared connection", "err", err)
		return p.store
	}
	return own
}
