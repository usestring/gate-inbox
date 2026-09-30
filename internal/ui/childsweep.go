package ui

import (
	"maps"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// sweepFinishedChildren archives the children that are over. A fan-out of
// eight leaves eight exited rows behind it, and a person clearing them by
// hand is the bloat this is here to stop -- so the facts that say a child is
// done are read on every poll pass rather than waited on.
//
// Over means the pane has exited, or -- the one case where the sweep ends a
// live agent -- the child finished and its spawner has taken the finish in
// and left it alone for the grace window. A report or a rest on its own is
// not enough: ending a child because a report was delivered or a window
// passed is how eight working children of one parent were killed on
// 2026-09-11, seconds after each sent an interim update and came to rest.
// Those children were finished on the screen but their spawner had not
// absorbed the finish; see store.RetirableChildren for what counts, and
// retireFinishedChildren for what else holds a child back.
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
	retire := !m.cfg.Children.KeepFinished
	grace := m.childFinishedGrace()
	rows := slices.Clone(m.sessions)
	owned := maps.Clone(m.extOwned)
	shells := m.shellTools()
	return func() tea.Msg {
		var retiring []string
		if retire {
			found, err := st.RetirableChildren(time.Now().Add(-grace))
			if err != nil {
				return childSweptMsg{err: err}
			}
			for _, child := range found {
				if !known[child.ID] || owned[child.ID] {
					continue
				}
				if retireHeld(rows, child.ID, shells) {
					continue
				}
				// A finished row over a pane that has gone is the exited case,
				// which the query above files once the poller writes it dead.
				if !driver.Exists(child.ID) {
					continue
				}
				logging.Info("child sweep retiring a finished child",
					"session", child.ID, "absorbed", child.Via, "absorbedAt", child.AbsorbedAt, "grace", grace)
				retiring = append(retiring, child.ID)
			}
		}
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
			// The terminals nested under the child go with it, as they do
			// under x. This sweep ends nothing that is still running, so a
			// live one holds the child back until its shell exits; filing
			// the child alone would strand that shell under a row that had
			// gone.
			shellsUnder, held := nestedShells(rows, child.ID, shells, driver.Exists)
			if held {
				logging.Info("child sweep left an exited child whose terminal is still up",
					"session", child.ID)
				continue
			}
			logging.Info("child sweep archived an exited child",
				"session", child.ID, "reason", child.Reason, "window", window, "terminals", len(shellsUnder))
			for _, id := range append(shellsUnder, child.ID) {
				if !slices.Contains(filed, id) {
					filed = append(filed, id)
				}
			}
		}
		if len(filed) == 0 {
			return childSweptMsg{retire: retiring}
		}
		missing, err := st.ArchiveKilled(filed, nil)
		if err != nil {
			return childSweptMsg{err: err}
		}
		return childSweptMsg{filed: filed, missing: missing, retire: retiring}
	}
}

// childSweptMsg is what one sweep found, carried back to the event loop
// because that is the only place the rows may be written.
type childSweptMsg struct {
	filed   []string
	missing []string
	// retire is the finished children to end and file, which happens back
	// on the loop through the board's own teardown.
	retire []string
	err    error
}

// applyChildSweep lands a finished sweep and frees the next one to go out.
func (m *Model) applyChildSweep(msg childSweptMsg) {
	m.childSweeping = false
	if msg.err != nil {
		m.errBar.text = "child sweep: " + msg.err.Error()
		return
	}
	if len(msg.filed) > 0 {
		m.markArchivedLocally(msg.filed, msg.missing)
	}
	retired := m.retireFinishedChildren(msg.retire)
	if len(msg.filed) > 0 || retired {
		m.rebuildRows()
	}
}

// retireFinishedChildren ends and files each finished child the sweep found,
// with whatever sits under it, through the same teardown x uses: the screen
// is kept, the row goes to the archived view where u restores it, and the
// retention sweep deletes it a week on.
//
// Everything is asked again against the rows as they are now, since the
// sweep read them a frame ago: a child sent a follow-up in between is
// working, and one of its own children may have started.
func (m *Model) retireFinishedChildren(ids []string) bool {
	retired := false
	shells := m.shellTools()
	for _, id := range ids {
		sess, ok := m.sessionByID(id)
		if !ok || sess.Archived || sess.Status != status.Finished {
			continue
		}
		if retireHeld(m.sessions, id, shells) {
			continue
		}
		set := append(liveBelow(m.sessions, id), sess)
		live := m.livePanes()
		if err := m.snapshotLive(set, live); err != nil {
			m.errBar.text = "child sweep: " + err.Error()
			continue
		}
		if text := m.archiveSessions(set, false, "", live); text != "" {
			m.errBar.text = "child sweep: " + text
			continue
		}
		logging.Info("child sweep archived a finished child", "session", id, "with", len(set)-1)
		retired = true
	}
	return retired
}

// retireHeld reports whether a finished child must stay even though its
// spawner has taken the finish in: a session under it, at any depth, is
// still live. A finished one counts, since only the
// child can take that finish in and it will be retired on its own first. A
// terminal it opened goes with it, as it does under x, and so does a
// descendant whose pane has already exited.
func retireHeld(rows []store.Session, id string, shells map[string]bool) bool {
	for _, kid := range liveBelow(rows, id) {
		if !shells[kid.Tool] && kid.Status != status.Dead {
			return true
		}
	}
	return false
}

// nestedShells is the active terminals nested directly under id, and whether
// any of them still has a pane up.
func nestedShells(rows []store.Session, id string, shells map[string]bool, exists func(string) bool) ([]string, bool) {
	var ids []string
	for _, row := range rows {
		if row.ParentID != id || row.Archived || !shells[row.Tool] {
			continue
		}
		if exists(row.ID) {
			return nil, true
		}
		ids = append(ids, row.ID)
	}
	return ids, false
}

// liveBelow is every session under id, by who spawned it, still on the
// active list.
func liveBelow(rows []store.Session, id string) []store.Session {
	var out []store.Session
	for _, kid := range store.Descendants(rows, id) {
		if !kid.Archived {
			out = append(out, kid)
		}
	}
	return out
}

// shellTools is the configured tools that open a shell rather than an agent.
func (m *Model) shellTools() map[string]bool {
	shells := map[string]bool{}
	for name, tool := range m.cfg.Tools {
		if tool.Shell {
			shells[name] = true
		}
	}
	return shells
}

// childFinishedGrace is how long a finished child is kept once its spawner
// has taken the finish in, falling back to the built-in default for a model
// built without a config.
func (m *Model) childFinishedGrace() time.Duration {
	if d := m.cfg.Children.FinishedGrace.Duration; d > 0 {
		return d
	}
	return 10 * time.Minute
}

// childAutoArchiveAfter is the configured window, falling back to the
// built-in default for a model built without a config -- every test's.
func (m *Model) childAutoArchiveAfter() time.Duration {
	if d := m.cfg.Children.AutoArchiveAfter.Duration; d > 0 {
		return d
	}
	return 30 * time.Minute
}
