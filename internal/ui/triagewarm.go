package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const triageWarmLimit = 2

type triageWarmMsg struct{}

type triageWarm struct {
	launched map[string]time.Time
	failedID string
}

func (m *Model) triageWarmCandidate() (store.Session, bool) {
	if !m.triage || m.hibernatePressure || m.wakeAll != nil || len(m.hibernated) == 0 || len(m.triageWarm.launched) >= triageWarmLimit {
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
	tried := make(map[string]bool, len(m.triageWarm.launched))
	for id := range m.triageWarm.launched {
		if sess, ok := m.sessionByID(id); ok && m.isTriageWarmRest(sess) {
			tried[id] = true
		}
	}
	index, ok := m.nextTriageInput(leftID, tried)
	if !ok || index == reviewsTurn {
		return store.Session{}, false
	}
	sess := m.rows[index].sess
	if sess.ID == m.triageWarm.failedID || m.shownStatus(sess) != status.Hibernating {
		return store.Session{}, false
	}
	for i := index - 1; i >= 0 && m.rows[i].isHead() && m.rows[i].sess.ID == sess.ID; i-- {
		if m.headPaneOf(m.rows[i].head) != nil {
			return store.Session{}, false
		}
	}
	return sess, true
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
	if err := m.wakeHibernatedSession(sess); err != nil {
		m.triageWarm.failedID = sess.ID
		logging.Warn("could not warm the next triage session", "session", sess.ID, logging.Err(err))
		return
	}
	if m.triageWarm.launched == nil {
		m.triageWarm.launched = make(map[string]time.Time, triageWarmLimit)
	}
	m.triageWarm.launched[sess.ID] = time.Now()
	m.triageWarm.failedID = ""
	m.rebuildRows()
	m.requestRefresh()
}

func (m *Model) isTriageWarmRest(sess store.Session) bool {
	// Keep a warm session in its old place until the operator reaches it.
	// A real question or turn from incoming mail still uses its live status.
	return m.triage && !m.triageWarm.launched[sess.ID].IsZero() &&
		(sess.Status == status.Starting || sess.Status == status.Idle)
}

func (m *Model) reconcileTriageWarm(listedAt time.Time) {
	for id, launchedAt := range m.triageWarm.launched {
		if !listedAt.After(launchedAt) {
			continue
		}
		if sess, ok := m.sessionByID(id); !ok || sess.Archived || sess.Status == status.Dead {
			delete(m.triageWarm.launched, id)
		}
	}
}
