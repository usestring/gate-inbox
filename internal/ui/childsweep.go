package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/logging"
)

// sweepFinishedChildren archives the children that are over. A fan-out of
// eight leaves eight exited rows behind it, and a person clearing them by
// hand is the bloat this is here to stop -- so the facts that say a child is
// done are read on every poll pass rather than waited on.
//
// Over means the pane has exited. The sweep never kills: a child that is
// finished, idle or waiting still has an agent in its pane, and ending it
// because a report was delivered or a window passed is how eight working
// children of one parent were killed on 2026-09-11, seconds after each sent
// an interim update and came to rest. What a parent wants ended it ends
// itself, with kill_session or archive_session.
//
// It runs on the poll clock rather than the archive sweep's half-hourly one:
// a child that has exited and reported back is finished business the moment
// the report lands, and leaving it on the list until the next slow sweep is
// the same bloat half an hour later.
//
// What it asks is not cheap, and none of it is the model's to answer. The
// store query runs a correlated subquery per candidate over the single
// connection the whole program shares, and every candidate then costs a
// has-session fork to a tmux server this board shares with every pane on it
// -- measured on the operator's board at a median of 1.4 seconds for the ones
// that go slow at all, against 5.8ms for the ones that do not. Asking all of
// that on the event loop let the poll clock, rather than the operator, decide
// when the board would stop answering. So the sweep is a command now: the
// questions are asked on a command goroutine and only the answer comes back
// here, where the rows may be written.
//
// What that costs is one frame. A child this sweep files leaves the list on
// the pass after the one that noticed rather than on the same one, which
// against a window measured in half hours is not a difference anybody can
// see.
func (m *Model) sweepFinishedChildren() tea.Cmd {
	if m.store == nil {
		return nil
	}
	// One sweep out at a time. The poll clock is faster than a sweep that has
	// to wait out a busy tmux server, and a later pass arming a second would
	// queue a fresh set of forks behind the set already stuck, which is the
	// shape that makes a slow server slower.
	if m.childSweeping {
		return nil
	}
	// Read here, on the loop, because the row set is the model's. Everything
	// below belongs to the store and to tmux, and is read on their own time.
	known := make(map[string]bool, len(m.sessions))
	for _, sess := range m.sessions {
		known[sess.ID] = true
	}
	m.childSweeping = true
	st, driver := m.store, m.tmux
	window := m.childAutoArchiveAfter()
	cutoff := time.Now().Add(-window)
	return func() tea.Msg {
		children, err := st.AutoArchivableChildren(cutoff)
		if err != nil {
			return childSweptMsg{err: err}
		}
		filed := make([]string, 0, len(children))
		for _, child := range children {
			if !known[child.ID] {
				continue
			}
			// A row reading dead with a pane still up is a status the poller
			// has not caught up with. The pane is the fact; the row waits.
			if driver.Exists(child.ID) {
				logging.Info("child sweep left a dead row whose pane is still up",
					"session", child.ID)
				continue
			}
			logging.Info("child sweep archived an exited child",
				"session", child.ID, "reason", child.Reason, "window", window)
			filed = append(filed, child.ID)
		}
		if len(filed) == 0 {
			return childSweptMsg{}
		}
		missing, err := st.ArchiveKilled(filed, nil)
		if err != nil {
			return childSweptMsg{err: err}
		}
		return childSweptMsg{filed: filed, missing: missing}
	}
}

// childSweptMsg is what one sweep found, carried back to the event loop
// because that is the only place the rows may be written.
type childSweptMsg struct {
	filed   []string
	missing []string
	err     error
}

// applyChildSweep lands a finished sweep and frees the next one to go out.
func (m *Model) applyChildSweep(msg childSweptMsg) {
	m.childSweeping = false
	if msg.err != nil {
		m.errBar.text = "child sweep: " + msg.err.Error()
		return
	}
	if len(msg.filed) == 0 {
		return
	}
	m.markArchivedLocally(msg.filed, msg.missing)
	m.rebuildRows()
}

// childAutoArchiveAfter is the configured window, falling back to the
// built-in default for a model built without a config -- every test's.
func (m *Model) childAutoArchiveAfter() time.Duration {
	if d := m.cfg.Children.AutoArchiveAfter.Duration; d > 0 {
		return d
	}
	return 30 * time.Minute
}
