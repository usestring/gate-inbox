package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

type triageWarmMsg struct{}

type triageWarm struct {
	id         string
	launchedAt time.Time
}

func (m *Model) triageWarmCandidate() (store.Session, bool) {
	if !m.triage || m.hibernatePressure || m.wakeAll != nil || len(m.hibernated) == 0 || !m.triageWarm.launchedAt.IsZero() {
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
	index, ok := m.nextTriageInput(leftID, nil)
	if !ok || index == reviewsTurn {
		return store.Session{}, false
	}
	sess := m.rows[index].sess
	if sess.ID == m.triageWarm.id || m.shownStatus(sess) != status.Hibernating {
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
	// detached launch stays one session ahead of the operator.
	sess, ok := m.triageWarmCandidate()
	if !ok || !m.isHibernated(sess) {
		return
	}
	m.triageWarm = triageWarm{id: sess.ID}
	if err := m.wakeHibernatedSession(sess); err != nil {
		logging.Warn("could not warm the next triage session", "session", sess.ID, logging.Err(err))
		return
	}
	m.triageWarm.launchedAt = time.Now()
	m.rebuildRows()
	m.requestRefresh()
}

func (m *Model) isTriageWarmRest(sess store.Session) bool {
	// Keep a warm session in its old place until the operator reaches it.
	// A real question or turn from incoming mail still uses its live status.
	return m.triage && sess.ID == m.triageWarm.id && !m.triageWarm.launchedAt.IsZero() &&
		(sess.Status == status.Starting || sess.Status == status.Idle)
}

func (m *Model) reconcileTriageWarm(listedAt time.Time) {
	if m.triageWarm.launchedAt.IsZero() || !listedAt.After(m.triageWarm.launchedAt) {
		return
	}
	if sess, ok := m.sessionByID(m.triageWarm.id); !ok || sess.Archived || sess.Status == status.Dead {
		m.triageWarm = triageWarm{}
	}
}
