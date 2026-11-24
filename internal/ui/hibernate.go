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

const hibernateSweepEvery = 10 * time.Minute

const hibernateSensitivitySetting = "hibernate_sensitivity"

func (m *Model) hibernateSensitivity() string {
	value, err := m.store.Setting(hibernateSensitivitySetting)
	if err != nil {
		m.errBar.text = "reading hibernation sensitivity: " + err.Error()
		return "off"
	}
	if value == "" {
		value = m.cfg.HibernateSensitivity
	}
	return sessioncmd.NormalizeHibernateSensitivity(value)
}

// settleHibernated is the board's half of hibernation, run on the poll
// clock. On every pass it wakes the hibernated rows that mail has reached:
// a child's question or finish report is written straight to its parent's
// queue, never through a send, so without this a hibernated parent would
// hold the message and never wake for it. It also frees memory when the
// live session count exceeds the memory-sensitive budget.
//
// Both run on a command goroutine: a wake relaunches a CLI and a sweep asks
// tmux about every candidate, and neither belongs on the event loop.
func (m *Model) settleHibernated() tea.Cmd {
	if m.store == nil || m.hibernating {
		return nil
	}
	sensitivity := m.hibernateSensitivity()
	memory := m.snap
	sweep := m.hibernateSweepDue(sensitivity, time.Now())
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
			result, err := sessions.HibernateAdaptive(sensitivity, memory, false)
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

func (m *Model) hibernateSweepDue(sensitivity string, now time.Time) bool {
	// The poller already tracks live statuses; the full sweep verifies processes before stopping any.
	active := 0
	for _, sess := range m.sessions {
		if !sess.Archived && !m.isShell(sess.Tool) && sess.Status != status.Dead {
			active++
		}
	}
	pressure := sessioncmd.HibernateNeeded(sensitivity, m.snap, active)
	crossed := pressure && !m.hibernatePressure
	m.hibernatePressure = pressure
	if sensitivity == "off" || !m.snap.MemOK || m.snap.MemTotal == 0 {
		return false
	}
	if !crossed && now.Sub(m.hibernateSweptAt) < hibernateSweepEvery {
		return false
	}
	m.hibernateSweptAt = now
	return true
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
		logging.Info("hibernated idle sessions", "count", msg.hibernated, "sensitivity", m.hibernateSensitivity())
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
	if err := m.wakeHibernatedSession(sess); err != nil {
		m.reportLaunchError(err)
		return false
	}
	m.reportDone("woke " + m.displayName(sess) + " from hibernation on its conversation")
	m.requestRefresh()
	return true
}

func (m *Model) wakeHibernatedSession(sess store.Session) error {
	if err := m.reviveSession(sess); err != nil {
		return err
	}
	if err := m.store.RemoveHibernated([]string{sess.ID}); err != nil {
		logging.Warn("woken session still on the hibernated set", "session", sess.ID, logging.Err(err))
	}
	m.hibernated = slices.DeleteFunc(slices.Clone(m.hibernated), func(id string) bool { return id == sess.ID })
	return nil
}

// wakeAllStagger is the gap between one wake-all relaunch and the next. A
// relaunch starts a whole CLI, and starting every hibernated one in the
// same instant is the memory and CPU spike hibernation exists to avoid.
const wakeAllStagger = 2 * time.Second

// wakeAllStepMsg asks for the next relaunch of a wake-all run.
type wakeAllStepMsg struct{}

// wakeAllRun is a wake-all in progress: the rows still to relaunch, in
// order, and what the run has done so far.
type wakeAllRun struct {
	queue    []string
	woken    int
	firstErr string
}

// wakeAllHibernated wakes every hibernated session in the current view, each
// on its own conversation, the way entering one does. A row that really died
// is left alone: revive-all is for those. The relaunches go out one at a
// time, wakeAllStagger apart, and a press while a run is still going only
// says how far it has got: there is only ever one run. Like reviveMany it
// carries on past a failure and names the first one.
func (m *Model) wakeAllHibernated() (tea.Model, tea.Cmd) {
	if m.wakeAll != nil {
		m.errBar.text = fmt.Sprintf("already waking hibernated sessions: %d woken, %d to go", m.wakeAll.woken, len(m.wakeAll.queue))
		return m, nil
	}
	// The store's set rather than the last refresh, as isHibernated reads
	// it, but once for the whole view instead of once per row.
	hibernated, err := m.store.Hibernated()
	if err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	var queue []string
	for _, sess := range m.searchedListedSessions() {
		if !sess.Archived && slices.Contains(hibernated, sess.ID) {
			queue = append(queue, sess.ID)
		}
	}
	if len(queue) == 0 {
		m.errBar.text = "no hibernated sessions to wake"
		return m, nil
	}
	m.wakeAll = &wakeAllRun{queue: queue}
	return m, m.wakeAllStep()
}

// wakeAllStep relaunches the next row of the wake-all run that still needs
// it and schedules the one after, or reports the run once nothing is left.
// A row woken some other way in the meantime is passed over without
// spending a stagger on it.
func (m *Model) wakeAllStep() tea.Cmd {
	run := m.wakeAll
	if run == nil {
		return nil
	}
	attempted := false
	for !attempted && len(run.queue) > 0 {
		id := run.queue[0]
		run.queue = run.queue[1:]
		attempted = m.wakeOneForAll(run, id)
	}
	m.rebuildRows()
	m.requestRefresh()
	if len(run.queue) == 0 {
		m.wakeAll = nil
		switch {
		case run.firstErr != "":
			m.errBar.text = fmt.Sprintf("woke %d, first error: %s", run.woken, run.firstErr)
		case run.woken == 0:
			m.errBar.text = "no hibernated sessions to wake"
		default:
			m.errBar.text = ""
			m.reportDone(fmt.Sprintf("woke %d from hibernation", run.woken))
		}
		return nil
	}
	m.errBar.text = fmt.Sprintf("waking hibernated sessions: %d woken, %d to go", run.woken, len(run.queue))
	return tea.Tick(wakeAllStagger, func(time.Time) tea.Msg { return wakeAllStepMsg{} })
}

// wakeOneForAll relaunches one queued row if it is still hibernated, and
// reports whether it tried: in the seconds the row waited, entering it or
// mail for it may have woken it already, or the operator archived it.
func (m *Model) wakeOneForAll(run *wakeAllRun, id string) bool {
	idx := slices.IndexFunc(m.sessions, func(s store.Session) bool { return s.ID == id })
	if idx < 0 || m.sessions[idx].Archived {
		return false
	}
	if still, err := m.store.IsHibernated(id); err != nil || !still {
		return false
	}
	if err := m.wakeHibernatedSession(m.sessions[idx]); err != nil {
		if run.firstErr == "" {
			run.firstErr = err.Error()
		}
		return true
	}
	run.woken++
	return true
}
