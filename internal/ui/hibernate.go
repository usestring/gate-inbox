package ui

import (
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// hibernateSweepEvery is how often the board looks for sessions to
// hibernate when a rest period is set. The rest period is measured in
// hours, so a slower clock than the poll's loses nothing.
const hibernateSweepEvery = 10 * time.Minute

// settleHibernated is the board's half of hibernation, run on the poll
// clock. On every pass it wakes the hibernated rows that mail has reached:
// a child's question or finish report is written straight to its parent's
// queue, never through a send, so without this a hibernated parent would
// hold the message and never wake for it. When a rest period is set, in
// Settings or as config.toml's hibernate_after, it also sweeps for sessions
// that have rested that long, keeping at least the minimum live sessions.
//
// Both run on a command goroutine: a wake relaunches a CLI and a sweep asks
// tmux about every candidate, and neither belongs on the event loop.
func (m *Model) settleHibernated() tea.Cmd {
	if m.store == nil || m.hibernating {
		return nil
	}
	after, minSessions := m.hibernateAfter, m.hibernateMinSessions
	sweep := after > 0 && time.Since(m.hibernateSweptAt) >= hibernateSweepEvery
	if sweep {
		m.hibernateSweptAt = time.Now()
	}
	m.hibernating = true
	st := m.store
	return func() tea.Msg {
		var msg hibernateSettledMsg
		settle, err := st.HibernatedToSettle()
		if err != nil || (len(settle) == 0 && !sweep) {
			msg.err = err
			return msg
		}
		dir, err := config.Dir()
		if err != nil {
			msg.err = err
			return msg
		}
		sessions := sessioncmd.NewSessions(dir, sessioncmd.CLIVocabulary())
		var errs []error
		if len(settle) > 0 {
			msg.woken, err = sessions.WakeHibernated()
			errs = append(errs, err)
		}
		if sweep {
			result, err := sessions.HibernateIdle(after, minSessions, false)
			msg.hibernated = len(result.Hibernated)
			errs = append(errs, err)
			for _, failure := range result.Errors {
				errs = append(errs, errors.New(failure))
			}
		}
		msg.err = errors.Join(errs...)
		return msg
	}
}

// hibernateSettledMsg is what one settleHibernated pass did.
type hibernateSettledMsg struct {
	woken      []string
	hibernated int
	err        error
}

func (m *Model) applyHibernateSettled(msg hibernateSettledMsg) {
	m.hibernating = false
	if len(msg.woken) > 0 {
		logging.Info("woke hibernated sessions for their mail", "sessions", msg.woken)
	}
	if msg.hibernated > 0 {
		logging.Info("hibernated idle sessions", "count", msg.hibernated, "after", m.hibernateAfter, "min_sessions", m.hibernateMinSessions)
	}
	if msg.err != nil {
		logging.Warn("hibernate", "err", msg.err)
	}
}

// storedHibernation reads the hibernation in force: the Settings choice
// when one is saved, else config.toml's. A store that cannot be read keeps
// the config values.
func storedHibernation(st *store.Store, cfg config.Config) (time.Duration, int) {
	after, minSessions := cfg.HibernateAfter.Duration, cfg.HibernateMinSessions
	if st == nil {
		return after, minSessions
	}
	if stored, storedMin, err := st.HibernationPolicy(after, minSessions); err == nil {
		after, minSessions = stored, storedMin
	}
	return after, minSessions
}

// shownStatus is sess's status as the board draws and ranks it: a row
// hibernate stopped reads hibernating, never dead. It is resting until its
// next message or until the operator enters it, and a dead mark sent people
// to revive by hand what would have come back on its own.
func (m *Model) shownStatus(sess store.Session) string {
	return store.Shown(sess, m.hibernated)
}

// isHibernated reports whether sess is a row hibernate stopped and nothing
// has woken yet. It asks the store rather than the last refresh: a send or
// a sweep may have changed the set since the board last drew.
func (m *Model) isHibernated(sess store.Session) bool {
	if m.store == nil || sess.Archived {
		return false
	}
	hibernated, err := m.store.IsHibernated(sess.ID)
	return err == nil && hibernated
}

// wakeForFocus relaunches a hibernated session the operator is entering, on
// its own conversation, so entering it is all it takes to have it back. It
// reports whether it woke sess; a failure is already on the error bar. The
// caller checks isHibernated first. Mail held for the row stays queued and
// goes in once the agent is at its prompt, the same path as a wake for a
// send.
func (m *Model) wakeForFocus(sess store.Session) bool {
	if err := m.reviveSession(sess); err != nil {
		m.reportLaunchError(err)
		return false
	}
	if err := m.store.RemoveHibernated([]string{sess.ID}); err != nil {
		logging.Warn("woken session still on the hibernated set", "session", sess.ID, logging.Err(err))
	}
	// The row reads starting at once, as the store now holds it: until the
	// next refresh it would otherwise be a dead row off the hibernated set.
	m.hibernated = slices.DeleteFunc(slices.Clone(m.hibernated), func(id string) bool { return id == sess.ID })
	for i := range m.sessions {
		if m.sessions[i].ID == sess.ID {
			m.sessions[i].Status = status.Starting
		}
	}
	m.reportDone("woke " + m.displayName(sess) + " from hibernation on its conversation")
	m.requestRefresh()
	return true
}

// wakeAllHibernated wakes every hibernated session in the current view, each
// on its own conversation, the way entering one does. A row that really died
// is left alone: revive-all is for those. Like reviveMany it carries on past
// a failure and names the first one.
func (m *Model) wakeAllHibernated() (tea.Model, tea.Cmd) {
	// The store's set rather than the last refresh, as isHibernated reads
	// it, but once for the whole view instead of once per row.
	hibernated, err := m.store.Hibernated()
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	var woken []string
	var firstErr string
	for _, sess := range m.searchedListedSessions() {
		if sess.Archived || !slices.Contains(hibernated, sess.ID) {
			continue
		}
		if err := m.reviveSession(sess); err != nil {
			if firstErr == "" {
				firstErr = err.Error()
			}
			continue
		}
		woken = append(woken, sess.ID)
	}
	if len(woken) > 0 {
		if err := m.store.RemoveHibernated(woken); err != nil {
			logging.Warn("woken sessions still on the hibernated set", "sessions", woken, logging.Err(err))
		}
		// As in wakeForFocus: the rows read starting at once rather than dead
		// off the hibernated set until the next refresh.
		m.hibernated = slices.DeleteFunc(slices.Clone(m.hibernated), func(id string) bool { return slices.Contains(woken, id) })
		for i := range m.sessions {
			if slices.Contains(woken, m.sessions[i].ID) {
				m.sessions[i].Status = status.Starting
			}
		}
	}
	switch {
	case len(woken) == 0 && firstErr == "":
		m.errBar.text = "no hibernated sessions to wake"
	case firstErr != "":
		m.errBar.text = fmt.Sprintf("woke %d, first error: %s", len(woken), firstErr)
	default:
		m.errBar.text = ""
		m.reportDone(fmt.Sprintf("woke %d from hibernation", len(woken)))
	}
	m.rebuildRows()
	m.requestRefresh()
	return m, nil
}
