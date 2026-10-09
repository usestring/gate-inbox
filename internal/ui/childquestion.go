package ui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/asks"
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
// a permission or trust prompt as drawn, credentials scrubbed. Every kind
// comes with the answer_session call that settles it, because every kind is
// the parent's to settle: by answering, or by asking its own user word for
// word and relaying their choice. None is handed to whoever is at the pane.
//
// A child that stays in its dialog is followed up, not left silently
// waiting: a relay the parent has not taken in time is sent again as an
// interrupt and the operator is pinged; one it took and left unanswered is
// repeated a bounded number of times, and the operator is pinged with the
// last.

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
	body, key, urgent := childDialogBody(sess, pane, p.pendingAskFile(sess))
	if p.childDialogs == nil {
		p.childDialogs = map[string]*childDialogRelay{}
	}
	relay := p.childDialogs[sess.ID]
	if relay == nil || relay.key != key {
		relay = &childDialogRelay{key: key}
		p.childDialogs[sess.ID] = relay
		return p.sendChildDialog(sess, parent, relay, body, urgent, now)
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
		"to you. Answer it with answer_session, or ask your user word for word with your own question tool "+
		"and then answer it.\n\n",
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
	body, key, urgent := childDialogBody(sess, pane, p.pendingAskFile(sess))
	return p.sendChildDialog(sess, parent, &childDialogRelay{key: key}, body, urgent, time.Now())
}

// relayTarget is the session a child's dialog is relayed to: the one that
// spawned it, while that one is alive and the child's role is not silent.
func (p *poller) relayTarget(sess store.Session) (store.Session, bool, error) {
	spawner := store.TrackerOf(sess)
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

// childDialogBody is the relay for whatever sess's pane is holding, a key
// that names the dialog regardless of where its cursor is or what is ticked,
// and whether it cannot wait for the parent's next pause.
func childDialogBody(sess store.Session, pane string, saved ...string) (body, key string, urgent bool) {
	target := childAskTarget(sess)
	if len(saved) > 0 {
		target.PendingAskFile = saved[0]
	}
	call, _ := asks.Pending(target)
	if call.Async && len(call.Questions) > 0 {
		return childAsyncMessage(sess, call), "a:" + call.ID, false
	}
	if reading, ok := dialog.ReadQuestions(sess.Tool, pane, call.Questions); ok && len(reading.Questions) > 0 {
		var id strings.Builder
		for _, q := range reading.Questions {
			id.WriteString(q.Header + "\x00" + q.Question + "\x00")
		}
		traits := asks.TraitsOf(sess.Tool)
		return childQuestionsMessageFor(sess, reading.Questions, traits, call.AskedAt), "q:" + textfmt.Fingerprint(id.String()),
			traits.Expires > 0
	}
	if screen, ok := dialog.ReadScreenFor(sess.Tool, pane); ok {
		screen = screen.WithExact(childPendingStrings(sess)...)
		return childScreenMessage(sess, screen), "s:" + textfmt.Fingerprint(screen.Identity()), false
	}
	if lost, ok := asks.Unanswered(childAskTarget(sess)); ok {
		return childLostMessage(sess, lost), "x:" + lost.Call.ID, true
	}
	return childWaitMessage(sess), "wait", false
}

func childAsyncMessage(sess store.Session, call asks.Call) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s (session %s), which you spawned, asked a question without a dialog: %s drew nothing to "+
		"answer on its screen and carries on, and it reads the next message it is sent as the answer. It asked:\n\n",
		sess.Name, sess.ID, asks.TraitsOf(sess.Tool).Name)
	questions := make([]dialog.Question, 0, len(call.Questions))
	for i, q := range call.Questions {
		question := dialog.Question{Index: i + 1, ID: q.ID, Header: q.Header, Question: q.Question}
		for _, option := range q.Options {
			question.Options = append(question.Options, dialog.Option{Label: option.Label, Description: option.Description})
		}
		questions = append(questions, question)
	}
	out.WriteString(strings.TrimRight(logging.ScrubWrapped(dialog.RenderQuestions(questions)), "\n"))
	fmt.Fprintf(&out, "\n\nSettle it yourself if your task or your user's standing decisions already do, else put it "+
		"to your user word for word. Then answer with answer_session on session %s, which sends your answer as "+
		"that message and reads it back from the child's own record.", sess.ID)
	return out.String()
}

func childLostMessage(sess store.Session, lost asks.Result) string {
	traits := asks.TraitsOf(sess.Tool)
	var out strings.Builder
	what := "expired with no answer: " + traits.Name + " resolved it by itself"
	if lost.Outcome == asks.Dismissed {
		what = "was dismissed at its pane without an answer"
	}
	fmt.Fprintf(&out, "%s (session %s), which you spawned, asked a question that %s", sess.Name, sess.ID, what)
	if !lost.At.IsZero() {
		fmt.Fprintf(&out, " at %s UTC", lost.At.UTC().Format("15:04:05"))
	}
	out.WriteString(", and carried on without it. It was never answered, and answer_session can no longer reach it. " +
		"What it asked:\n\n")
	questions := make([]dialog.Question, 0, len(lost.Call.Questions))
	for i, q := range lost.Call.Questions {
		question := dialog.Question{Index: i + 1, ID: q.ID, Header: q.Header, Question: q.Question, MultiSelect: q.MultiSelect}
		for _, option := range q.Options {
			question.Options = append(question.Options, dialog.Option{Label: option.Label, Description: option.Description})
		}
		questions = append(questions, question)
	}
	out.WriteString(strings.TrimRight(logging.ScrubWrapped(dialog.RenderQuestions(questions)), "\n"))
	fmt.Fprintf(&out, "\n\nIf the answer still matters, settle it (with your user if it is theirs) and tell the child "+
		"with send_session, which it reads as an ordinary message.")
	return out.String()
}

func childAskTarget(sess store.Session) asks.Target {
	return asks.Target{Tool: sess.Tool, AgentSessionID: sess.AgentSessionID, Cwd: sess.Cwd}
}

// childPendingStrings is what sess's one unresolved tool call was given, so a
// permission prompt is relayed with its command as written rather than as the
// pane wrapped it.
func childPendingStrings(sess store.Session) []string {
	if sess.Tool != "claude" || sess.AgentSessionID == "" {
		return nil
	}
	return convo.PendingToolStrings(convo.TranscriptFor(convo.ClaudeHome(), sess.AgentSessionID, sess.Cwd))
}

// pendingAskFile is where sess's ask-pending hook saves its dialog's call,
// or "" for a poller with no hooks manager.
func (p *poller) pendingAskFile(sess store.Session) string {
	if p.hooks == nil {
		return ""
	}
	return p.hooks.PendingAskFile(sess.ID)
}

// childQuestionsMessage is every question of the dialog in the child's words
// and how to settle them.
func childQuestionsMessage(sess store.Session, questions []dialog.Question) string {
	return childQuestionsMessageFor(sess, questions, asks.TraitsOf("claude"), time.Time{})
}

func childQuestionsMessageFor(sess store.Session, questions []dialog.Question, traits asks.Traits, askedAt time.Time) string {
	var out strings.Builder
	noun := "a question"
	if len(questions) > 1 {
		noun = fmt.Sprintf("a dialog asking %d questions", len(questions))
	}
	fmt.Fprintf(&out, "%s (session %s), which you spawned, has stopped on %s and is waiting for an "+
		"answer:\n\n", sess.Name, sess.ID, noun)
	out.WriteString(strings.TrimRight(logging.ScrubWrapped(dialog.RenderQuestions(questions)), "\n"))
	if traits.Expires > 0 && !askedAt.IsZero() {
		fmt.Fprintf(&out, "\n\n%s resolves this question by itself, with no answer, about %s after asking it: "+
			"at about %s UTC. The child then carries on without your answer, so answer before then.",
			traits.Name, traits.Expires.Round(time.Second), askedAt.Add(traits.Expires).UTC().Format("15:04:05"))
	}
	var person, multi []string
	open := 0
	for _, q := range questions {
		switch {
		case q.MultiSelect && !traits.MultiSelectAnswerable && (!q.Answered || q.OnScreen):
			person = append(person, fmt.Sprint(q.Index))
		case !q.Answered:
			open++
		}
		if q.MultiSelect && traits.MultiSelectAnswerable && (!q.Answered || q.OnScreen) {
			multi = append(multi, fmt.Sprint(q.Index))
		}
	}
	if open > 0 {
		fmt.Fprintf(&out, "\n\nAnswer yourself each question the task you gave it or your user's standing "+
			"decisions already settle. Put the rest to your user with your own question tool, copying the "+
			"header, question and options (and any recommendation) word for word. Then answer on session %s "+
			"with one answer_session call: answers, one entry per question, naming it by number or header and "+
			"giving the option's text, or your own words to %s.", sess.ID, traits.FreeText)
		if traits.MultiSelectAnswerable {
			if traits.Name != asks.TraitsOf("claude").Name {
				out.WriteString(" For a multi-select, give every option to tick, separated by commas.")
			}
		}
		if traits.Name == asks.TraitsOf("claude").Name {
			out.WriteString(" It presses Submit once every question has an answer.")
		} else {
			out.WriteString(" It submits the dialog once every question has an answer.")
		}
	}
	if len(multi) > 0 {
		fmt.Fprintf(&out, "\n\nQuestion %s is a multi-select: give its answers entry ticks, the labels of every "+
			"option to leave ticked, instead of an answer (or pass ticks alone when it is the question on the "+
			"screen). answer_session ticks and unticks the boxes, reads them back and submits it, and fails "+
			"if the boxes read back differently. If your user decides it, ask them with a multi-select of "+
			"your own, options word for word as above.", strings.Join(multi, ", "))
	}
	var approvals []string
	for _, q := range questions {
		if !q.Answered && dialog.IsApproval(q.Header) {
			approvals = append(approvals, fmt.Sprint(q.Index))
		}
	}
	if len(approvals) > 0 {
		fmt.Fprintf(&out, "\n\nQuestion %s is headed Approval: the child is asking for your user's approval. "+
			"Never answer it yourself. Your user is shown it in triage on the board and may answer it at the "+
			"child's pane; read_session first, and if it is answered there is nothing to carry. Otherwise ask "+
			"your user verbatim, then answer with relay: true; any other answer is refused.", strings.Join(approvals, ", "))
	}
	if len(person) > 0 {
		fmt.Fprintf(&out, "\n\nQuestion %s is a multi-select, which answer_session cannot tick: only a person at its pane can answer it. Put it to your user word for word, then ask the operator for the keystrokes.", strings.Join(person, ", "))
	}
	if open == 0 && len(person) == 0 {
		fmt.Fprintf(&out, "\n\nEvery question has an answer and the dialog is waiting on its Submit page: "+
			"answer_session on session %s with the answer \"Submit answers\" sends it.", sess.ID)
	}
	return out.String()
}

// childScreenMessage is a permission prompt, a trust dialog or any other
// dialog that is not a question, relayed as drawn with credentials scrubbed,
// and the relayed answer_session call that settles it.
func childScreenMessage(sess store.Session, screen dialog.Screen) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s (session %s), which you spawned, has stopped on a %s and is waiting. "+
		"It reads:\n\n", sess.Name, sess.ID, screen.Kind)
	for _, line := range strings.Split(logging.ScrubWrapped(screen.Text()), "\n") {
		out.WriteString("  | " + line + "\n")
	}
	if screen.Legend != "" {
		fmt.Fprintf(&out, "\nKeys: %s\n", screen.Legend)
	}
	if len(screen.Choices) < 2 {
		fmt.Fprintf(&out, "\nanswer_session cannot read this as choices, but it is still yours to settle. Ask "+
			"your user with your own question tool, quoting the lines above word for word and offering the "+
			"keys to press as the options (for example \"Enter\", or \"Down Enter\"). Then call answer_session "+
			"on session %s with keys set to the keys they chose and relay: true; it sends them and reads back "+
			"that the screen changed.", sess.ID)
		return out.String()
	}
	labels := make([]string, len(screen.Choices))
	for i, choice := range screen.Choices {
		labels[i] = fmt.Sprintf("%q", logging.ScrubWrapped(choice.Label))
	}
	fmt.Fprintf(&out, "\nWhether %s is your user's call, never yours. Ask your user with your own question "+
		"tool, copying word for word:\n\n  question: %q\n  options: %s\n\nThen call answer_session on session "+
		"%s with answer set to the option they chose and relay: true. It picks that choice by its text and "+
		"reads back that the dialog cleared; it keys nothing unless your transcript holds that question and "+
		"their answer.", screenDecision(screen.Kind), logging.ScrubWrapped(screen.Prompt()),
		strings.Join(labels, ", "), sess.ID)
	if len(labels) > 4 {
		out.WriteString(" Your question tool takes four options: give the four likeliest, and your user can " +
			"type any other choice word for word.")
	}
	return out.String()
}

// screenDecision is what a dialog of kind asks the user to decide.
func screenDecision(kind dialog.ScreenKind) string {
	switch kind {
	case dialog.ScreenPermission:
		return "the child may do this"
	case dialog.ScreenWorkspaceTrust:
		return "to trust this folder"
	case dialog.ScreenMCPTrust:
		return "to use this MCP server"
	}
	return "to make this choice"
}

// childWaitMessage is a stop on nothing this can read as a dialog.
func childWaitMessage(sess store.Session) string {
	return fmt.Sprintf("%s (session %s), which you spawned, has stopped and is waiting for input, but not "+
		"on a dialog Gate Inbox can read. Call read_session on it to see what it is showing. A question it "+
		"asked in prose at its input line takes send_session. Any other screen waiting on a choice is still "+
		"yours to settle: ask your user with your own question tool, quoting it word for word and offering "+
		"the keys to press as the options, then call answer_session on session %s with keys set to what they "+
		"chose and relay: true.",
		sess.Name, sess.ID, sess.ID)
}
