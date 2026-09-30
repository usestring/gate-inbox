package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Telling a parent its child has stopped on a question.
//
// Nesting a spawn under the session that asked for it drew a tree and did
// nothing else: the parent was never told when the work it delegated stopped,
// so a fan-out of nine could sit on nine questions while the session that
// wanted the answers went on believing they were running. The question landed
// in front of a person instead -- one who did not choose the task, did not
// write the prompt, and has to reconstruct both before they can answer.
//
// So the row's own parent hears about it. This is a message, not an
// escalation: it goes through the inbox every agent already reads, is
// delivered when the parent is at rest like any other, and says how to
// answer. If the parent never gets to it the question still stands on the
// board for a person, exactly as before -- nothing here takes the operator
// out of the loop, it just stops making them the first resort.
//
// A wait this cannot read is relayed too, and says less. A permission prompt
// asks whether this program may do something, which is the operator's to
// answer and not a parent's, and a plain-text question has no options to pick
// -- answer_session takes neither. But "your child is stopped and you cannot
// answer it" is still the thing the parent has to know: told nothing, it goes
// on waiting on work that has stopped, which is the whole failure this file
// exists to end. What the pane holds is deliberately left out of that message:
// these screens carry live API keys, which is why even the log does not take
// pane text by default.

// relayChildQuestion tells sess's parent that sess is holding a question.
//
// Called on the transition into waiting, and only then: a dialog that stands
// for an hour is one message, not one per poll pass. The inbox's dedupe
// window is the second guard, on the fingerprint of the question itself, so a
// child that goes waiting, is answered, and asks the same thing again inside
// the window is not relayed twice either.
func (p *poller) relayChildQuestion(sess store.Session, newStatus, pane string) error {
	// The session that spawned it, not the row it is drawn under: the tree
	// carries one level, so a grandchild's questions all went to a root that
	// had not assigned the work and could not answer them either.
	spawner := store.TrackerOf(sess)
	if newStatus != status.Waiting || spawner == "" || sess.Archived {
		return nil
	}
	// A helper whose role is silent is watched by the extension that
	// launched it; the session it works for hearing of it too is noise.
	if sessionhooks.Role(sess.Role).Silent {
		return nil
	}
	parent, err := p.store.Get(spawner)
	if err != nil {
		return ignoreDeletedSession(err)
	}
	if parent.Archived || parent.Status == status.Dead {
		return nil
	}
	// A dialog this can read is relayed with its question and the call that
	// answers it; anything else is relayed as the fact of the stop alone.
	body := childWaitMessage(sess)
	if questions := dialog.Questions(pane, childAsked(sess)); len(questions) > 0 {
		body = childQuestionsMessage(sess, questions)
	} else if held, ok := dialog.Inspect(ansi.Strip(pane)); ok && held.Guarded() {
		body = childGuardedMessage(sess, held)
	}
	_, _, err = p.store.Enqueue(store.InboxMessage{
		SessionID:   parent.ID,
		SenderID:    sess.ID,
		SenderName:  sess.Name,
		Body:        body,
		Fingerprint: textfmt.Fingerprint(body),
		SentAt:      time.Now(),
	}, store.DefaultInboxLimits)
	if err != nil {
		// A full or rate-limited queue is the inbox working: the parent is
		// already holding more than it has read. The question keeps standing
		// on the board, which is where it would have been anyway.
		logging.Info("child question not relayed to its parent",
			"session", sess.ID, "parent", parent.ID, logging.Err(err))
		return nil
	}
	logging.Info("relayed a child's question to its parent",
		"session", sess.ID, "parent", parent.ID)
	return nil
}

// childAsked is the pending AskUserQuestion call in sess's transcript, which
// spells out every question of a dialog whose pane shows one at a time.
func childAsked(sess store.Session) []convo.AskQuestion {
	if sess.Tool != "claude" || sess.AgentSessionID == "" {
		return nil
	}
	path := convo.TranscriptFor(convo.ClaudeHome(), sess.AgentSessionID, sess.Cwd)
	if path == "" {
		return nil
	}
	questions, _ := convo.PendingAsk(path)
	return questions
}

// childQuestionsMessage is what the parent reads: whose dialog it is, every
// question in it in the child's words -- including the ones a several-question
// dialog is not showing -- and how to settle them: itself where its brief
// already does, through its own user for the rest, then one answer_session
// call. The questions are the message, so the parent can put them to its user
// at once rather than reading the child's pane first.
func childQuestionsMessage(sess store.Session, questions []dialog.Question) string {
	var out strings.Builder
	noun := "a question"
	if len(questions) > 1 {
		noun = fmt.Sprintf("a dialog asking %d questions", len(questions))
	}
	fmt.Fprintf(&out, "%s (session %s), which you spawned, has stopped on %s and is waiting for an "+
		"answer:\n\n", sess.Name, sess.ID, noun)
	out.WriteString(strings.TrimRight(dialog.RenderQuestions(questions), "\n"))
	var person []string
	open := 0
	for _, q := range questions {
		switch {
		case q.MultiSelect && !q.Answered:
			person = append(person, fmt.Sprint(q.Index))
		case !q.Answered:
			open++
		}
	}
	if open > 0 {
		fmt.Fprintf(&out, "\n\nAnswer yourself each question the task you gave it or your user's standing "+
			"decisions already settle. Put the rest to your user with your own question tool, copying the "+
			"header, question and options (and any recommendation) word for word. Then answer on session %s "+
			"with one answer_session call: answers, one entry per question, naming it by number or header and "+
			"giving the option's text, or your own words to type instead. It presses Submit once every "+
			"question has an answer.", sess.ID)
	}
	var approvals []string
	for _, q := range questions {
		if !q.Answered && dialog.IsApproval(q.Header) {
			approvals = append(approvals, fmt.Sprint(q.Index))
		}
	}
	if len(approvals) > 0 {
		fmt.Fprintf(&out, "\n\nQuestion %s is headed Approval: the child is asking for your user's approval. "+
			"Never answer it yourself. Ask your user verbatim, then answer with relay: true; any other answer "+
			"is refused.", strings.Join(approvals, ", "))
	}
	if len(person) > 0 {
		fmt.Fprintf(&out, "\n\nQuestion %s is a multi-select, which answer_session cannot tick: only a "+
			"person at its pane can answer it, so tell your user it is waiting.", strings.Join(person, ", "))
	}
	if open == 0 && len(person) == 0 {
		fmt.Fprintf(&out, "\n\nEvery question has an answer and the dialog is waiting on its Submit page: "+
			"answer_session on session %s with the answer \"Submit answers\" sends it.", sess.ID)
	}
	return out.String()
}

// childGuardedMessage is a stop only a person may answer: a permission prompt,
// or Codex's first-run trust prompt. It names the kind, and no part of the
// pane, so a permission prompt quoting a command with a key in it does not
// travel into another session's context.
func childGuardedMessage(sess store.Session, held dialog.Dialog) string {
	return fmt.Sprintf("%s (session %s), which you spawned, has stopped on %s. answer_session cannot "+
		"answer it: only a person at its pane can. If you are blocked on what it was doing, tell your user "+
		"which session is waiting rather than going on waiting; otherwise carry on without it.",
		sess.Name, sess.ID, held.Refusal())
}

// childWaitMessage is what the parent reads about a stop on nothing this can
// read: which child, that answer_session will not take it, and how to find
// out what it is waiting for. Like childGuardedMessage it names no part of
// the pane.
//
// Its text does not vary with the screen, so the inbox's fingerprint window
// collapses a child that stops, is answered and stops again inside that
// window into one message. That is the same bargain the question path takes,
// and it errs the right way: the row is on the board throughout.
func childWaitMessage(sess store.Session) string {
	return fmt.Sprintf("%s (session %s), which you spawned, has stopped and is waiting for input, but not "+
		"on a dialog Gate Inbox can read, so answer_session cannot answer it. Call read_session on it to see "+
		"what it is showing: a question it asked in prose at its input line takes send_session, and a "+
		"prompt asking permission is your user's to answer at its pane.",
		sess.Name, sess.ID)
}
