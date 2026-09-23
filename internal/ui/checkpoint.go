package ui

import (
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// checkpointInterval is how often the log is folded back into the database.
//
// It is a trade between two costs that both land on the same disk: a
// checkpoint fsyncs, so running one every few seconds is a standing I/O load
// on a box that already sits at 11% iowait, while never running one lets the
// log grow without bound. At the rate a board writes, thirty seconds holds
// the log near the few megabytes it occupies today and pays the fsync twice a
// minute, on a connection nothing is waiting on.
const checkpointInterval = 30 * time.Second

// runCheckpoints folds the write-ahead log back into the database on its own
// clock, for as long as this process polls, so that no commit ever does it.
// store.DeferCheckpoints has what that is worth.
func (p *poller) runCheckpoints(st *store.Store) {
	for range time.Tick(checkpointInterval) {
		if err := st.Checkpoint(); err != nil {
			// One that did not land costs a longer log and nothing else,
			// and the next tick tries again -- but failing all of them is
			// the case DeferCheckpoints is trusting this not to hit.
			logging.Warn("write-ahead log checkpoint failed", "err", err)
		}
	}
}

// startCheckpoints hands checkpointing to a connection of its own, and only
// then stops commits from doing it.
//
// The order is the safety. A connection it cannot open means nothing else
// will checkpoint this database, so the inline trigger has to stay exactly
// where it is: a board that stutters once in a thousand commits is a far
// better outcome than a log that grows until the disk is full.
func (p *poller) startCheckpoints() {
	own, err := p.store.Reopen()
	if err != nil {
		logging.Warn("commits keep checkpointing: no connection of our own", "err", err)
		return
	}
	if err := p.store.DeferCheckpoints(); err != nil {
		logging.Warn("commits keep checkpointing", "err", err)
		own.Close()
		return
	}
	go p.runCheckpoints(own)
}
