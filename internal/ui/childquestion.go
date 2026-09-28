package ui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/convo"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/notify"
	"github.com/usestring/gate-inbox/internal/sessionhooks"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Telling a parent its child has stopped on a dialog, with the whole dialog.
//
// The message goes through the inbox every agent already reads, so the parent
// is told, not left to poll. It carries the dialog in full: every question of
// a several-question dialog, a multi-select's boxes and which are ticked, and
// a permission or trust prompt as drawn, credentials scrubbed. What
// answer_session cannot answer says so, and what the parent can do instead.
//
// A child that stays in its dialog is followed up, not left silently
// waiting: a relay the parent has not taken in time is sent again as an
// interrupt and the operator is pinged; one it took and left unanswered is
// repeated a bounded number of times, then handed to the operator.

const (
	// childDialogSubject labels every relay about one child's dialog, so a
	// newer one replaces an older one the parent has not read yet.
	childDialogSubject = "child-dialog"
	// childDialogRetry is how long a relay the inbox refused waits to retry.
	childDialogRetry = 30 * time.Second
	// childDialogUndelivered is how long a relay may sit undelivered before
	// it is re-sent as an interrupt and the operator is pinged.
	childDialogUndelivered = 3 * time.Minute
	// childDialogRemind is how long a delivered relay may go unanswered
	// before it is repeated, at most childDialogReminders times.
	childDialogRemind    = 10 * time.Minute
	childDialogReminders = 2
)

// childDialogRelay is what was last relayed about one child's dialog.
type childDialogRelay struct {
	key       string
	msgID     int64
	sentAt    time.Time
	settledAt time.Time
	reminders int
	escalated bool
}

// watchChildDialog runs every pass for every child. It relays a dialog the
// first time it is seen, relays again when the dialog changes, and follows
// up on one that stands.
func (p *poller) watchChildDialog(sess store.Session, newStatus, pane string, now time.Time) error {
	if newStatus != status.Waiting {
		delete(p.childDialogs, sess.ID)
		return nil
	}
	if pane == "" {
		return nil
	}
	parent, ok, err := p.relayTarget(sess)
	if err != nil || !ok {
		return err
	}
	body, key := childDialogBody(sess, pane)
	if p.childDialogs == nil {
		p.childDialogs = map[string]*childDialogRelay{}
	}
	relay := p.childDialogs[sess.ID]
	if relay == nil || relay.key != key {
		relay = &childDialogRelay{key: key}
		p.childDialogs[sess.ID] = relay
		return p.sendChildDialog(sess, parent, relay, body, false, now)
	}
	if relay.msgID == 0 {
		if now.Sub(relay.sentAt) >= childDialogRetry {
			return p.sendChildDialog(sess, parent, relay, body, false, now)
		}
		return nil
	}
	if relay.settledAt.IsZero() && relay.msgID > 0 {
		msg, err := p.store.Message(relay.msgID, sess.ID)
		if err != nil {
			return ignoreDeletedSession(err)
		}
		if !msg.DeliveredAt.IsZero() && msg.SupersededBy == 0 {
			relay.settledAt = msg.DeliveredAt
		}
	}
	if relay.settledAt.IsZero() {
		if now.Sub(relay.sentAt) >= childDialogUndelivered && !relay.escalated {
			relay.escalated = true
			p.pingOperator(sess, parent, "has not taken the relay yet")
			late := fmt.Sprintf("Not yet read after %s, so this interrupts you. ", now.Sub(relay.sentAt).Round(time.Minute))
			return p.sendChildDialog(sess, parent, relay, late+body, true, now)
		}
		return nil
	}
	if now.Sub(relay.settledAt) < childDialogRemind || relay.reminders >= childDialogReminders {
		return nil
	}
	relay.reminders++
	if relay.reminders == childDialogReminders {
		p.pingOperator(sess, parent, "has not answered it")
	}
	again := fmt.Sprintf("Reminder %d of %d: %s is still stopped on the dialog below, %s after it was relayed "+
		"to you. Answer it, put it to your user, or tell them it is waiting.\n\n",
		relay.reminders, childDialogReminders, sess.Name, now.Sub(relay.sentAt).Round(time.Minute))
	return p.sendChildDialog(sess, parent, relay, again+body, false, now)
}

// relayChildQuestion tells sess's parent, once, what dialog sess is holding.
func (p *poller) relayChildQuestion(sess store.Session, newStatus, pane string) error {
	if newStatus != status.Waiting {
		return nil
	}
	parent, ok, err := p.relayTarget(sess)
	if err != nil || !ok {
		return err
	}
	body, key := childDialogBody(sess, pane)
	return p.sendChildDialog(sess, parent, &childDialogRelay{key: key}, body, false, time.Now())
}

// relayTarget is the session a child's dialog is relayed to: the one that
// spawned it, while that one is alive and the child's role is not silent.
func (p *poller) relayTarget(sess store.Session) (store.Session, bool, error) {
	spawner := store.SpawnerOf(sess)
	if spawner == "" || sess.Archived || sessionhooks.Role(sess.Role).Silent {
		return store.Session{}, false, nil
	}
	parent, err := p.store.Get(spawner)
	if err != nil {
		return store.Session{}, false, ignoreDeletedSession(err)
	}
	if parent.Archived || parent.Status == status.Dead {
		return store.Session{}, false, nil
	}
	return parent, true, nil
}

func (p *poller) sendChildDialog(sess, parent store.Session, relay *childDialogRelay, body string, interrupt bool, now time.Time) error {
	relay.sentAt = now
	relay.settledAt = time.Time{}
	id, _, err := p.store.Enqueue(store.InboxMessage{
		SessionID:   parent.ID,
		SenderID:    sess.ID,
		SenderName:  sess.Name,
		Body:        body,
		Fingerprint: textfmt.Fingerprint(body),
		Subject:     childDialogSubject,
		Interrupt:   interrupt,
		SentAt:      now,
	}, store.DefaultInboxLimits)
	switch {
	case errors.Is(err, store.ErrInboxDuplicate):
		relay.msgID, relay.settledAt = -1, now
		return nil
	case err != nil:
		relay.msgID = 0
		logging.Info("child dialog not relayed to its parent yet",
			"session", sess.ID, "parent", parent.ID, logging.Err(err))
		return nil
	}
	relay.msgID = id
	logging.Info("relayed a child's dialog to its parent",
		"session", sess.ID, "parent", parent.ID, "interrupt", interrupt)
	return nil
}

func (p *poller) pingOperator(sess, parent store.Session, why string) {
	if p.escalate == nil {
		return
	}
	p.escalate(notify.Note{
		Subject: sess.Name,
		Body:    fmt.Sprintf("Waiting on a dialog; its parent %s %s.", parent.Name, why),
		Kind:    notify.Waiting,
	})
}

// childDialogBody is the relay for whatever sess's pane is holding, and a key
// that names the dialog regardless of where its cursor is or what is ticked.
func childDialogBody(sess store.Session, pane string) (body, key string) {
	if questions := dialog.Questions(pane, childAsked(sess)); len(questions) > 0 {
		var id strings.Builder
		for _, q := range questions {
			id.WriteString(q.Header + "\x00" + q.Question + "\x00")
		}
		return childQuestionsMessage(sess, questions), "q:" + textfmt.Fingerprint(id.String())
	}
	if screen, ok := dialog.ReadScreen(pane); ok {
		return childScreenMessage(sess, screen), "s:" + textfmt.Fingerprint(screen.Identity())
	}
	return childWaitMessage(sess), "wait"
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

// childQuestionsMessage is every question of the dialog in the child's words
// and how to settle them.
func childQuestionsMessage(sess store.Session, questions []dialog.Question) string {
	var out strings.Builder
	noun := "a question"
	if len(questions) > 1 {
		noun = fmt.Sprintf("a dialog asking %d questions", len(questions))
	}
	fmt.Fprintf(&out, "%s (session %s), which you spawned, has stopped on %s and is waiting for an "+
		"answer:\n\n", sess.Name, sess.ID, noun)
	out.WriteString(strings.TrimRight(logging.ScrubWrapped(dialog.RenderQuestions(questions)), "\n"))
	var person []string
	open := 0
	for _, q := range questions {
		switch {
		case q.MultiSelect && (!q.Answered || q.OnScreen):
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
	if len(person) > 0 {
		fmt.Fprintf(&out, "\n\nQuestion %s is a multi-select, which answer_session cannot tick: only a "+
			"person at its pane can answer it. Put it to your user with your own question tool, options "+
			"and ticks word for word as above, then either have them answer at the pane of %s or ask the "+
			"operator for the keystrokes: on that question, Up/Down moves the ❯ marker, Enter ticks or "+
			"unticks the box under it, and Right (or Tab) moves on to the next question.",
			strings.Join(person, ", "), sess.Name)
	}
	if open == 0 && len(person) == 0 {
		fmt.Fprintf(&out, "\n\nEvery question has an answer and the dialog is waiting on its Submit page: "+
			"answer_session on session %s with the answer \"Submit answers\" sends it.", sess.ID)
	}
	return out.String()
}

// childScreenMessage is a dialog answer_session does not answer, relayed as
// drawn with credentials scrubbed, and what the parent can do about it.
func childScreenMessage(sess store.Session, screen dialog.Screen) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s (session %s), which you spawned, has stopped on a %s and is waiting. "+
		"It reads:\n\n", sess.Name, sess.ID, screen.Kind)
	for _, line := range strings.Split(logging.ScrubWrapped(screen.Text()), "\n") {
		out.WriteString("  | " + line + "\n")
	}
	if len(screen.Choices) > 0 {
		out.WriteString("\nThe choices (current = where the ❯ marker is):\n")
		for i, choice := range screen.Choices {
			label := logging.ScrubWrapped(choice.Label)
			if choice.Number > 0 {
				label = fmt.Sprintf("%d. %s", choice.Number, label)
			} else {
				label = fmt.Sprintf("%d) %s", i+1, label)
			}
			if choice.Cursor {
				label += " (current)"
			}
			out.WriteString("  " + label + "\n")
		}
	}
	fmt.Fprintf(&out, "\nKeys: %s\n\n", screen.Legend)
	switch screen.Kind {
	case dialog.ScreenPermission:
		out.WriteString("answer_session cannot answer a permission prompt: whether the child may do this " +
			"is your user's call, never an agent's. Put it to your user word for word -- the tool, the " +
			"command or path, and the choices -- and have them answer at its pane, or ask the operator for " +
			"the one keystroke. ")
	case dialog.ScreenWorkspaceTrust, dialog.ScreenMCPTrust:
		out.WriteString("answer_session cannot answer a trust dialog: whether to trust this folder or " +
			"MCP server is your user's call. Put it to your user word for word and have them answer at its " +
			"pane, or ask the operator for the one keystroke. ")
	default:
		out.WriteString("answer_session cannot answer this dialog. Put it to your user word for word and " +
			"have them answer at its pane, or ask the operator for the keystroke. ")
	}
	out.WriteString(screenKeystroke(screen))
	out.WriteString(" If you are not blocked on it, carry on with other work, but tell your user it is waiting.")
	return out.String()
}

// screenKeystroke says which key picks each choice.
func screenKeystroke(screen dialog.Screen) string {
	if len(screen.Choices) == 0 {
		return "The keys it takes are listed above."
	}
	if screen.Choices[0].Number > 0 {
		return "Pressing a choice's number at its pane picks that choice."
	}
	if cursor := screen.Cursor(); cursor >= 0 {
		return fmt.Sprintf("Enter picks the current choice; Up/Down moves the ❯ marker first (it is on "+
			"choice %d of %d).", cursor+1, len(screen.Choices))
	}
	return "Up/Down moves the ❯ marker and Enter picks."
}

// childWaitMessage is a stop on nothing this can read as a dialog.
func childWaitMessage(sess store.Session) string {
	return fmt.Sprintf("%s (session %s), which you spawned, has stopped and is waiting for input, but not "+
		"on a dialog Gate Inbox can read, so answer_session cannot answer it. Call read_session on it to see "+
		"what it is showing: a question it asked in prose at its input line takes send_session, and a "+
		"prompt asking permission is your user's to answer at its pane.",
		sess.Name, sess.ID)
}
