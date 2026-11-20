package ui

import (
	"fmt"
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
	parent, ok, err := p.relayTarget(sess)
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
	var out strings.Builder
	fmt.Fprintf(&out, "%s (session %s) is no longer on the dialog relayed to you earlier: it was answered "+
		"at its pane, on the board, or by another relay. Do not put it to your user, and do not call "+
		"answer_session for it; read_session on %s shows what it was answered and where it went.",
		sess.Name, sess.ID, sess.ID)
	if dismissed {
		out.WriteString(" Your own question to your user asking the same thing was still open, so Gate " +
			"Inbox dismissed it; your user has already answered it once.")
	}
	body := out.String()
	_, _, err = p.store.Enqueue(store.InboxMessage{
		SessionID:   parent.ID,
		SenderID:    sess.ID,
		SenderName:  sess.Name,
		Body:        body,
		Fingerprint: textfmt.Fingerprint(body),
		Subject:     childDialogSubject,
		SentAt:      now,
	}, store.DefaultInboxLimits)
	if err != nil {
		logging.Info("could not tell a parent its child's dialog cleared",
			"session", sess.ID, "parent", parent.ID, logging.Err(err))
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

// childDialogQuestions is the questions of the dialog sess's pane holds, or
// nil for any other screen.
func childDialogQuestions(sess store.Session, pane, savedAsk string) []dialog.Question {
	target := childAskTarget(sess)
	target.PendingAskFile = savedAsk
	call, _ := asks.Pending(target)
	if call.Async {
		return nil
	}
	reading, ok := dialog.ReadQuestions(sess.Tool, pane, call.Questions)
	if !ok {
		return nil
	}
	return reading.Questions
}
