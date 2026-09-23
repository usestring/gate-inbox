package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const submissionRescindWindow = 30 * time.Second

type submissionRescind struct {
	sessionID    string
	sentAt       time.Time
	status       string
	lastStatusAt time.Time
}

func (m *Model) noteSubmission(sess store.Session) {
	m.latestSubmission = submissionRescind{
		sessionID:    sess.ID,
		sentAt:       time.Now(),
		status:       sess.Status,
		lastStatusAt: sess.LastStatusAt,
	}
}

func (m *Model) canRescindLatestSubmission() bool {
	_, ok := m.rescindableSubmission()
	return ok
}

func (m *Model) rescindableSubmission() (store.Session, bool) {
	mark := m.latestSubmission
	if mark.sessionID == "" || time.Since(mark.sentAt) > submissionRescindWindow {
		return store.Session{}, false
	}
	sess, ok := m.sessionByID(mark.sessionID)
	if !ok || sess.Archived || sess.Status == status.Dead {
		return store.Session{}, false
	}
	active := sess.Status == status.Working ||
		(sess.Status == mark.status && sess.LastStatusAt.Equal(mark.lastStatusAt))
	return sess, active
}

func (m *Model) submissionInterruptKeys(tool string) []string {
	if keys := m.cfg.Tools[tool].InterruptKeys; len(keys) > 0 {
		return keys
	}
	return []string{"Escape"}
}

func (m *Model) rescindLatestSubmission() (tea.Model, tea.Cmd) {
	sess, ok := m.rescindableSubmission()
	if !ok {
		m.latestSubmission = submissionRescind{}
		m.errBar.text = "no active submission to rescind"
		return m, nil
	}
	if !m.tmux.Exists(sess.ID) {
		m.latestSubmission = submissionRescind{}
		m.errBar.text = m.deadSessionHint()
		return m, nil
	}
	if err := m.tmux.SendKeys(sess.ID, m.submissionInterruptKeys(sess.Tool)...); err != nil {
		m.errBar.text = err.Error()
		return m, nil
	}
	m.latestSubmission = submissionRescind{}
	m.poller.noteOperatorInput(sess.ID)
	m.errBar.text = "rescinded the latest submission to " + m.displayName(sess)
	m.requestRefresh()
	return m, nil
}
