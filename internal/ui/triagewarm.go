package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const triageWarmLookahead = 3

type triageWarmMsg struct{}

type triageWarm struct {
	launched map[string]triageWarmLaunch
	failed   map[string]bool

	sensitivity    string
	reservedStarts int
}

type triageWarmLaunch struct {
	launchedAt   time.Time
	lastStatusAt time.Time
}

func (m *Model) triageWarmCandidate() (store.Session, bool) {
	if !m.triage || m.wakeAll != nil || len(m.hibernated) == 0 {
		return store.Session{}, false
	}
	if slots := sessioncmd.HibernateWakeSlots(m.triageWarm.sensitivity, m.snap, triageWarmLookahead); m.triageWarm.reservedStarts >= slots {
		return store.Session{}, false
	}
	var leftID string
	if m.ringed() {
		if sess, ok := m.selected(); ok {
			leftID = sess.ID
		}
	} else if !m.reviewPageOpen() {
		return store.Session{}, false
	}
	tried := make(map[string]bool, triageWarmLookahead+len(m.triageWarm.failed))
	for id := range m.triageWarm.failed {
		tried[id] = true
	}
	for range triageWarmLookahead {
		index, ok := m.nextTriageInput(leftID, tried)
		if !ok {
			break
		}
		if index == reviewsTurn {
			tried[reviewsTriedKey] = true
			continue
		}
		sess := m.rows[index].sess
		tried[sess.ID] = true
		if m.shownStatus(sess) == status.Hibernating && !m.triageHasHeadPane(index) {
			return sess, true
		}
	}
	return store.Session{}, false
}

func (m *Model) triageHasHeadPane(index int) bool {
	for i := index - 1; i >= 0 && m.rows[i].isHead() && m.rows[i].sess.ID == m.rows[index].sess.ID; i-- {
		if m.headPaneOf(m.rows[i].head) != nil {
			return true
		}
	}
	return false
}

func (m *Model) triageWarmCmd() tea.Cmd {
	if _, ok := m.triageWarmCandidate(); !ok {
		return nil
	}
	return func() tea.Msg { return triageWarmMsg{} }
}

func (m *Model) warmNextTriageSession() {
	// The handoff can run before this message. Recheck the queue so the
	// detached launch still follows the operator's current queue.
	sess, ok := m.triageWarmCandidate()
	if !ok || !m.isHibernated(sess) {
		return
	}
	m.triageWarm.reservedStarts++
	if err := m.wakeHibernatedSession(sess); err != nil {
		if m.triageWarm.failed == nil {
			m.triageWarm.failed = make(map[string]bool)
		}
		m.triageWarm.failed[sess.ID] = true
		logging.Warn("could not warm the next triage session", "session", sess.ID, logging.Err(err))
		return
	}
	if m.triageWarm.launched == nil {
		m.triageWarm.launched = make(map[string]triageWarmLaunch, triageWarmLookahead)
	}
	m.triageWarm.launched[sess.ID] = triageWarmLaunch{launchedAt: time.Now(), lastStatusAt: sess.LastStatusAt}
	m.rebuildRows()
	m.requestRefresh()
}

func (m *Model) isTriageWarmRest(sess store.Session) bool {
	// Keep a warm session in its old place until the operator reaches it.
	// A real question or turn from incoming mail still uses its live status.
	_, warmed := m.triageWarm.launched[sess.ID]
	return m.triage && warmed &&
		(sess.Status == status.Starting || sess.Status == status.Idle)
}

func (m *Model) reconcileTriageWarm(listedAt time.Time, sampledMemory bool) {
	starting := 0
	for id, launch := range m.triageWarm.launched {
		sess, ok := m.sessionByID(id)
		if listedAt.After(launch.launchedAt) && (!ok || sess.Archived || (sess.Status != status.Starting && sess.Status != status.Idle)) {
			delete(m.triageWarm.launched, id)
			continue
		}
		if sess.Status == status.Starting {
			starting++
		}
	}
	if sampledMemory {
		// A starting agent can still allocate memory after this sample.
		m.triageWarm.reservedStarts = starting
	}
	for id := range m.triageWarm.failed {
		if sess, ok := m.sessionByID(id); !ok || m.shownStatus(sess) != status.Hibernating {
			delete(m.triageWarm.failed, id)
		}
	}
}
