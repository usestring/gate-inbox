package ui

import (
	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/store"
)

// childBackgroundWork says why a finished child must not be ended yet: the
// background tasks its transcript has not heard end, and whether a shell its
// agent started is still running under the pane. Ending the agent ends both,
// and the work and the report it was waiting on with them -- how a child
// with two judge runs going was archived on 2026-10-02, ten minutes after
// its spawner read a finish that was only the turn's.
//
// It is the sweep's own check, asked again just before it ends anything,
// whatever the row's status says: a status read off the pane can be wrong
// for as long as the pane is drawn in a way the rules do not expect.
func childBackgroundWork(tracker *agentsession.Background, sess store.Session, panePID func(string) (int, error)) (pending []string, shells int) {
	pending = claudeBackgroundPending(tracker, sess)
	pid, err := panePID(sess.ID)
	if err != nil || pid <= 0 {
		return pending, 0
	}
	return pending, agentsession.ClaudeShellsUnder(pid, agentsession.ProcessTable)
}

// rowByID is the row for id among rows the sweep copied off the loop.
func rowByID(rows []store.Session, id string) (store.Session, bool) {
	for _, row := range rows {
		if row.ID == id {
			return row, true
		}
	}
	return store.Session{}, false
}
