package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/usestring/gate-inbox/extension"

	"github.com/usestring/gate-inbox/internal/sessioncmd"
	"github.com/usestring/gate-inbox/internal/store"
)

// QuestionTyper types words into the free-text row of the question a
// session's dialog is showing and reads back what the session took:
// sessioncmd's BoardType.
type QuestionTyper func(sessionID, words string) (sessioncmd.AnsweredQuestion, error)

// UseQuestionTyper lets a snippet answer a structured question: sent to a
// session holding one with a free-text row, its text is typed there rather
// than held back. It is set before the program starts.
func (m *Model) UseQuestionTyper(typer QuestionTyper) {
	m.typeQuestion = typer
}

// questionTypedMsg is how a snippet typed into a question landed.
type questionTypedMsg struct {
	sess         store.Session
	text, quoted string
	started      time.Time
	handOver     bool
	err          error
}

// typeIntoQuestion answers the question sess is showing with text, off the
// event loop: the readback waits on the session to record the answer.
//
// No landing is armed. The dialog's own record of the answer is what
// BoardType reads back, so a nil error is the answer having landed, and the
// agent's next turn is the question's, not a new one started at the prompt.
// handOver is carried to the readback, which acts on it as a landing would.
func (m *Model) typeIntoQuestion(sess store.Session, text, quoted string, started time.Time, handOver bool) tea.Cmd {
	typer := m.typeQuestion
	m.errBar.text = "answering " + m.displayName(sess) + "'s question with " + quoted
	return func() tea.Msg {
		_, err := typer(sess.ID, text)
		return questionTypedMsg{sess: sess, text: text, quoted: quoted, started: started, handOver: handOver, err: err}
	}
}

// applyQuestionTyped does for a verified answer what answerLanded does for a
// landed snippet: the answer becomes the submission undo takes back, and with
// handOver the drain moves on if the operator is still on the session.
func (m *Model) applyQuestionTyped(msg questionTypedMsg) tea.Cmd {
	if msg.err != nil {
		m.errBar.text = "could not answer " + m.displayName(msg.sess) + "'s question with " + msg.quoted + ": " + msg.err.Error()
		return nil
	}
	m.noteSubmission(msg.sess)
	m.noteOperator(msg.sess, extension.OperatorSnippet, msg.text, true, msg.started)
	if m.heldAckID == msg.sess.ID {
		m.heldAckID = ""
	}
	// The agent has been given something to do, as by any send.
	if err := m.store.SetAcked(msg.sess.ID, false); err != nil {
		m.errBar.text = "answered, but clearing the alert ack failed: " + err.Error()
		return nil
	}
	m.errBar.text = "answered " + m.displayName(msg.sess) + "'s question with " + msg.quoted
	m.requestRefresh()
	if !msg.handOver || !m.autoProceeds() {
		return nil
	}
	sess, ok := m.selected()
	if !ok || sess.ID != msg.sess.ID {
		return nil
	}
	return m.handOverLanded(sess, time.Now())
}
