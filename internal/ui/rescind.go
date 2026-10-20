package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension"

	"github.com/usestring/gate-inbox/internal/logging"
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

// noteOperator records that the operator handed sess something from the
// board and it reached the pane: on the row, which is the clock smart triage
// measures a wait from, and to the board's observer. An extension that put a
// question to the operator learns from it that a person has answered.
func (m *Model) noteOperator(sess store.Session, via extension.OperatorVia, text string, dialog bool) {
	at := time.Now()
	m.stampOperator(sess.ID, at)
	if m.poller == nil || m.poller.observer == nil {
		return
	}
	m.poller.observer.Operator(extension.OperatorInput{
		SessionID: sess.ID,
		Via:       via,
		Text:      text,
		Dialog:    dialog,
		At:        at,
	})
}

// stampOperator stores a person's turn on a session. A stamp that did not
// land costs that session's place in the smart triage order until the next
// one, so it is logged rather than put in front of the operator.
func (m *Model) stampOperator(id string, at time.Time) {
	if m.store == nil {
		return
	}
	if err := ignoreDeletedSession(m.store.SetOperatorAt(id, at)); err != nil {
		logging.Warn("operator stamp failed", "session", id, "err", err)
	}
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
		m.errBar.text = "no active submission to undo"
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
	// The drain muted the session when the answer landed, as dealt with. The
	// answer is withdrawn and the agent stopped, so it is the operator's again
	// and the queue brings it back. A later answer still pending for it is
	// what the interrupt just cut off, so it must not land either.
	m.unmute(sess.ID)
	if pending := m.landings[sess.ID]; pending != nil {
		recordLanding(pending, "cancelled")
	}
	m.dropLanding(sess.ID)
	m.errBar.text = "undid the latest submission to " + m.displayName(sess)
	m.requestRefresh()
	return m, nil
}
