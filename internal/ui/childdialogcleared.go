package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/asks"
	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Withdrawing a relayed question once the child's dialog clears.
//
// A child's question is shown in two places at once: relayed to its parent,
// which puts it to the user with its own question tool, and on the board,
// where the user can answer at the child's pane. Whichever answer lands
// first settles it. Without this, the parent's copy stays open and the user
// answers the same question a second time, into an answer_session that can
// only fail because the child has moved on.
//
// So when a relayed dialog clears, the relay is withdrawn wherever it got
// to: dropped from the parent's queue if it was never delivered, the
// parent's own open dialog asking the same question is dismissed, and the
// parent is told the child was answered elsewhere so it does not ask again.
// The parent's dialog is dismissed only while its screen still shows one of
// the relayed questions unanswered, so a parent asking anything else, or
// typing, is never keyed into.

// paneKeyer is the slice of the tmux driver withdrawing needs; tests stand
// in for it.
type paneKeyer interface {
	CapturePane(id string) (string, error)
	SendKeys(id string, keys ...string) error
}

func (p *poller) keyer() paneKeyer {
	if p.panes != nil {
		return p.panes
	}
	if p.tmux == nil {
		return nil
	}
	return p.tmux
}

// childDialogCleared withdraws what was relayed about sess's dialog, which
// is no longer on its screen.
func (p *poller) childDialogCleared(sess store.Session, relay *childDialogRelay, now time.Time) error {
	if relay == nil || len(relay.questions) == 0 {
		return nil
	}
	// An account move or a replacement archives the row the relay came
	// from, which is exactly when its relay has to go, so an archived child
	// still has its relay withdrawn; relayTarget alone would refuse it.
	withdrawing := sess
	withdrawing.Archived = false
	parent, ok, err := p.relayTarget(withdrawing)
	if err != nil || !ok {
		return err
	}
	dropped, err := p.store.Withdraw(parent.ID, sess.ID, childDialogSubject, now)
	if err != nil {
		return ignoreDeletedSession(err)
	}
	dismissed := p.dismissParentAsk(parent, relay.questions)
	if dropped > 0 && !dismissed {
		// The parent never saw it and is not asking it: nothing to undo.
		logging.Info("withdrew an unread child-dialog relay", "session", sess.ID, "parent", parent.ID)
		return nil
	}
	answers := childRegisteredAnswers(sess, relay)
	body := clearedNotice(sess, relay.questions, answers, dismissed)
	// A dismissal is an event of its own: the same question dismissed twice
	// must reach the parent twice, so its fingerprint carries the moment.
	keyed := body
	if dismissed {
		keyed += "\n" + now.UTC().Format(time.RFC3339Nano)
	}
	_, _, err = p.store.Enqueue(store.InboxMessage{
		SessionID:   parent.ID,
		SenderID:    sess.ID,
		SenderName:  sess.Name,
		Body:        body,
		Fingerprint: textfmt.Fingerprint(keyed),
		Subject:     clearedSubject(relay.questions),
		SentAt:      now,
	}, clearedNoticeLimits)
	if err != nil {
		logging.Info("could not tell a parent its child's dialog cleared",
			"session", sess.ID, "parent", parent.ID, "dismissed", dismissed, logging.Err(err))
	}
	return nil
}

// withdrawDepartedChildDialogs withdraws the relays of children the pass no
// longer visits. seen holds every session the pass looked at. A child moved
// to another account, replaced, or archived drops out of the pass, so it
// never again reaches watchChildDialog to have its dialog read as cleared.
// Without this its relay stayed queued, or its parent stayed on its copy,
// for as long as the board ran.
func (p *poller) withdrawDepartedChildDialogs(seen map[string]bool, now time.Time) error {
	for id, relay := range p.childDialogs {
		if seen[id] {
			continue
		}
		delete(p.childDialogs, id)
		sess, err := p.store.Get(id)
		if err != nil {
			if err := ignoreDeletedSession(err); err != nil {
				return err
			}
			continue
		}
		if err := p.childDialogCleared(sess, relay, now); err != nil {
			return err
		}
	}
	return nil
}

// screenQuestions is what a parent relayed a screen dialog asks its user:
// the question of the AskUserQuestion input the relay hands it, and the
// dialog's own prompt for a parent that copied the screen by hand. A screen
// relay needs these as much as a question does: without them nothing could
// tell the parent's copy of it, and its clearing was never withdrawn.
func screenQuestions(sess store.Session, pane string, saved ...string) []dialog.Question {
	screen, ask, ok := childScreen(sess, pane, saved...)
	if !ok {
		return nil
	}
	var out []dialog.Question
	if ask != nil {
		for _, q := range ask.Questions {
			out = append(out, dialog.Question{Header: q.Header, Question: q.Question})
		}
	}
	prompt := strings.TrimSpace(logging.ScrubWrapped(screen.Prompt()))
	if prompt != "" && !askingAnyOf([]dialog.Question{{Question: prompt}}, out) {
		out = append(out, dialog.Question{Question: prompt})
	}
	return out
}

// childDialogClearedSubject labels the notice that a relayed dialog cleared.
// It is not childDialogSubject: the next relay about the child withdraws or
// replaces whatever is queued on that subject, and the notice is the
// parent's only way to tell Gate Inbox's Escape from its user declining.
const childDialogClearedSubject = "child-dialog-cleared"

// clearedSubject is the notice's subject for one dialog, so a notice about
// a later, different dialog never replaces an undelivered one about this.
func clearedSubject(questions []dialog.Question) string {
	var key strings.Builder
	for _, q := range questions {
		key.WriteString(q.Header)
		key.WriteString("\x00")
		key.WriteString(q.Question)
		key.WriteString("\x00")
	}
	return childDialogClearedSubject + ":" + textfmt.Fingerprint(key.String())
}

// clearedNoticeLimits exempt the notice from the per-recipient rate cap. A
// burst of relays from the same child fills that cap, and the notice it
// would then drop is the one telling the parent a dismissal was not a
// decline. Clears are bounded by relays, which the cap still holds.
var clearedNoticeLimits = func() store.InboxLimits {
	limits := store.DefaultInboxLimits
	limits.RateCap = math.MaxInt32
	return limits
}()

// clearedNotice tells the parent the dialog it was relayed is settled, what
// it asked and, when the child's record shows it, what was answered.
func clearedNotice(sess store.Session, questions []dialog.Question, answers map[string]string, dismissed bool) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s (session %s) is no longer on the dialog relayed to you earlier: it was answered "+
		"at its pane, on the board, or by another relay, or the child left it by restarting, moving to "+
		"another account or ending. Do not put it to your user, and do not call "+
		"answer_session for it.", sess.Name, sess.ID)
	if dismissed {
		out.WriteString(" Your own question to your user asking the same thing was still open, so Gate " +
			"Inbox dismissed it by pressing Escape. The \"user doesn't want to proceed\" or \"interrupted\" " +
			"result you got for that question is that Escape, not your user declining: the child's dialog " +
			"was settled at its pane or on the board. Tell your user their answer was received (what was " +
			"answered is below when Gate Inbox could read it), and carry on.")
	}
	var lines []string
	for _, q := range questions {
		if q.Question == "" {
			continue
		}
		label := q.Question
		if q.Header != "" {
			label = "[" + q.Header + "] " + q.Question
		}
		if answer, ok := answers[q.Question]; ok && answer != "" {
			lines = append(lines, fmt.Sprintf("- %s → %s", label, answer))
		} else {
			lines = append(lines, "- "+label)
		}
	}
	if len(lines) > 0 {
		if len(answers) > 0 {
			out.WriteString("\n\nWhat was answered:\n")
		} else {
			out.WriteString("\n\nThe dialog asked:\n")
		}
		out.WriteString(strings.Join(lines, "\n"))
	}
	fmt.Fprintf(&out, "\n\nread_session on %s shows what it was answered and where it went.", sess.ID)
	return logging.ScrubWrapped(out.String())
}

// childRegisteredAnswers is what the child's record shows the relayed
// dialog was answered, keyed by question text, or nil when it shows none.
func childRegisteredAnswers(sess store.Session, relay *childDialogRelay) map[string]string {
	if relay.callID == "" {
		return nil
	}
	answered, err := asks.AnsweredSince(childAskTarget(sess), time.Time{})
	if err != nil {
		return nil
	}
	for i := len(answered) - 1; i >= 0; i-- {
		if answered[i].ToolUseID != relay.callID {
			continue
		}
		got := map[string]string{}
		for question, answer := range answered[i].Answers {
			for _, r := range relay.questions {
				if r.Question != "" && dialog.SameQuestion(question, r.Question) {
					got[r.Question] = answer
				}
			}
		}
		if len(got) > 0 {
			return got
		}
	}
	return nil
}

// dismissParentAsk presses Escape on parent's own question dialog when its
// screen shows one of relayed, unanswered, and reports whether it did.
func (p *poller) dismissParentAsk(parent store.Session, relayed []dialog.Question) bool {
	k := p.keyer()
	if k == nil || parent.Status != status.Waiting {
		return false
	}
	pane, err := k.CapturePane(parent.ID)
	if err != nil {
		return false
	}
	target := childAskTarget(parent)
	target.PendingAskFile = p.pendingAskFile(parent)
	reading, ok := dialog.ReadQuestions(parent.Tool, pane, asks.PendingQuestions(target))
	if !ok || !askingAnyOf(reading.Questions, relayed) {
		return false
	}
	if err := k.SendKeys(parent.ID, "Escape"); err != nil {
		logging.Info("could not dismiss a parent's duplicate question", "parent", parent.ID, logging.Err(err))
		return false
	}
	logging.Info("dismissed a parent's question its child already had answered", "parent", parent.ID)
	return true
}

// askingAnyOf reports whether shown holds an unanswered question that is
// one of relayed.
func askingAnyOf(shown, relayed []dialog.Question) bool {
	for _, s := range shown {
		if s.Answered || s.Question == "" {
			continue
		}
		for _, r := range relayed {
			if r.Question != "" && dialog.SameQuestion(s.Question, r.Question) {
				return true
			}
		}
	}
	return false
}

// childDialogQuestions is the questions and call ID of the dialog sess's
// pane holds, or nil and an empty ID for any other screen.
func childDialogQuestions(sess store.Session, pane, savedAsk string) ([]dialog.Question, string) {
	target := childAskTarget(sess)
	target.PendingAskFile = savedAsk
	call, _ := asks.Pending(target)
	if call.Async {
		return nil, ""
	}
	reading, ok := dialog.ReadQuestions(sess.Tool, pane, call.Questions)
	if !ok {
		return nil, ""
	}
	return reading.Questions, call.ID
}
