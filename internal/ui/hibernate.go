package ui

import (
	"errors"
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
// hibernate when [hibernate_after] is set. The rest period is measured in
// hours, so a slower clock than the poll's loses nothing.
const hibernateSweepEvery = 10 * time.Minute

// settleHibernated is the board's half of hibernation, run on the poll
// clock. On every pass it wakes the hibernated rows that mail has reached:
// a child's question or finish report is written straight to its parent's
// queue, never through a send, so without this a hibernated parent would
// hold the message and never wake for it. When hibernate_after is set it
// also sweeps for sessions that have rested that long.
//
// Both run on a command goroutine: a wake relaunches a CLI and a sweep asks
// tmux about every candidate, and neither belongs on the event loop.
func (m *Model) settleHibernated() tea.Cmd {
	if m.store == nil || m.hibernating {
		return nil
	}
	after := m.cfg.HibernateAfter.Duration
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
			result, err := sessions.HibernateIdle(after, false)
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
		logging.Info("hibernated idle sessions", "count", msg.hibernated, "after", m.cfg.HibernateAfter.Duration)
	}
	if msg.err != nil {
		logging.Warn("hibernate", "err", msg.err)
	}
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
