package ui

import (
	"fmt"
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
	// standing is the questions the dialog still waits on after this one:
	// the answer was taken, but the session is not done being asked.
	standing int
	err      error
}

// typeIntoQuestion answers the question sess is showing with text, off the
// event loop: the readback waits on the session to record the answer.
//
// No landing is armed. The dialog's own record of the answer is what
// BoardType reads back, so a nil error is the answer having landed, and the
// agent's next turn is the question's, not a new one started at the prompt.
// handOver is held in questionHandOvers until the readback, which acts on it
// as a landing would; keepLandingHere takes it back as it does a landing's.
//
// One answer runs per session at a time: a second would navigate the dialog
// from the same stale cursor, or paste after the first submitted it, so it is
// refused until the first is read back.
func (m *Model) typeIntoQuestion(sess store.Session, text, quoted string, started time.Time, handOver bool) tea.Cmd {
	if m.questionsAnswering[sess.ID] {
		m.errBar.text = "still answering " + m.displayName(sess) + "'s question — " + quoted + " was not sent"
		return nil
	}
	if m.questionsAnswering == nil {
		m.questionsAnswering = map[string]bool{}
	}
	m.questionsAnswering[sess.ID] = true
	typer := m.typeQuestion
	if handOver {
		if m.questionHandOvers == nil {
			m.questionHandOvers = map[string]bool{}
		}
		m.questionHandOvers[sess.ID] = true
	}
	m.errBar.text = "answering " + m.displayName(sess) + "'s question with " + quoted
	return func() tea.Msg {
		answered, err := typer(sess.ID, text)
		standing := 0
		if !answered.Submitted {
			standing = answered.Standing
		}
		return questionTypedMsg{sess: sess, text: text, quoted: quoted, started: started, standing: standing, err: err}
	}
}

// applyQuestionTyped does for a verified answer what answerLanded does for a
// landed snippet: the answer becomes the submission undo takes back, and with
// a handover still pending the drain moves on if the operator is still on
// the session and the dialog has no questions left.
func (m *Model) applyQuestionTyped(msg questionTypedMsg) tea.Cmd {
	handOver := m.questionHandOvers[msg.sess.ID]
	delete(m.questionHandOvers, msg.sess.ID)
	delete(m.questionsAnswering, msg.sess.ID)
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
	if msg.standing > 0 {
		// The dialog went on to its next question: the session still needs
		// the operator, so it is not handed over.
		m.errBar.text += fmt.Sprintf("; %d more to answer", msg.standing)
		return nil
	}
	if !handOver || !m.autoProceeds() {
		return nil
	}
	sess, ok := m.selected()
	if !ok || sess.ID != msg.sess.ID {
		return nil
	}
	return m.handOverLanded(sess, time.Now())
}
