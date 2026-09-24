package ui

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/extension/textfmt"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// messageContext is what a reader would otherwise spend a turn re-deriving
// before it can act on a message: how long the message waited, whether the
// session that wrote it still exists, where the reader's own last word to
// that session sits against it, and whether newer ones are already waiting
// behind this one.
//
// 15% of inbound messages on the board this was measured against were handled
// as already obsolete. Every fact needed to see that was in the store when the
// message was typed, and none of it was on the message.
type messageContext struct {
	// Age is how long the message waited between being written and being
	// typed into the pane. A message can wait days behind an agent that
	// never rests, and a wall-clock stamp alone does not tell a reader that.
	Age time.Duration
	// SenderEnded is set when the session that wrote this has been archived,
	// killed or deleted since. A reply cannot reach it.
	SenderEnded bool
	// Successor is the session that took the sender's work over, set only
	// where the store records the link. A migration records one; an ordinary
	// archive-and-respawn records nothing, and a guess from a shared name
	// would name the wrong session exactly as confidently as the right one.
	Successor store.Session
	// LastSentToSender is when this recipient last sent the sender a message
	// of its own, and is zero if it never has.
	//
	// Sent, not delivered, because sent is the comparison that stays true
	// either way: a message the sender wrote before this one existed cannot
	// have taken it into account, whether or not it ever reached the pane.
	// Delivery is the tempting field and it is not readable here without
	// knowing whether the row was dropped, which the report query does not
	// return.
	LastSentToSender time.Time
	// NewerQueued is how many later messages from the same sender are
	// already waiting behind this one.
	NewerQueued int
}

// inboxContextScan is how far back the two context reads look. A recipient's
// whole queue is capped well below it by store.DefaultInboxLimits.QueueCap, so
// in practice it reaches past everything waiting and some way into delivered
// history. It is a bound rather than a guarantee: a queue depth read off a
// truncated scan comes back low, which understates the count and never
// invents one.
const inboxContextScan = 64

// messageContext gathers what the store already knows about one message
// about to be typed.
//
// This is three indexed reads per delivered message, which the pass can afford
// only because deliveries are rare and each one is already paying for a tmux
// paste. It is not the shape HeadMessages exists to avoid -- that was a query
// per session per tick, on the one connection a keypress writes through.
//
// Every read degrades to nothing rather than to an error. The context is a
// courtesy on top of delivery, and a message that is ready to go must not be
// held in the queue because a lookup beside it failed.
func (p *poller) messageContext(sess store.Session, msg store.InboxMessage, now time.Time) messageContext {
	ctx := messageContext{Age: now.Sub(msg.SentAt)}
	sender, err := p.store.Get(msg.SenderID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// The row is gone entirely, so there is no successor to find and
		// nothing left to reply to.
		ctx.SenderEnded = true
	case err != nil:
		logging.Warn("inbox context: could not read the sender's row",
			"sender", msg.SenderID, logging.Err(err))
	case sender.Archived || sender.Status == status.Dead:
		ctx.SenderEnded = true
		ctx.Successor = p.successorOf(sender)
	}
	ctx.LastSentToSender = p.lastSentTo(msg.SenderID, sess.ID)
	ctx.NewerQueued = p.newerQueuedFrom(sess.ID, msg)
	return ctx
}

// successorOf finds the live session that took an ended one's work over.
//
// Only a recorded link counts. A migration names its source, so that pair is
// a fact; two sessions sharing a name under one parent is not, and naming a
// wrong session as the place to reply is worse than naming none.
func (p *poller) successorOf(sender store.Session) store.Session {
	live, err := p.store.ListSessions(false)
	if err != nil {
		logging.Warn("inbox context: could not list sessions for a successor",
			"sender", sender.ID, logging.Err(err))
		return store.Session{}
	}
	for _, cand := range live {
		if cand.ID != sender.ID && cand.MigrationID == sender.ID && cand.Status != status.Dead {
			return cand
		}
	}
	return store.Session{}
}

// lastSentTo is when fromID last put a message of its own into toID's queue.
func (p *poller) lastSentTo(toID, fromID string) time.Time {
	reports, err := p.store.ReportsTo(toID, inboxContextScan)
	if err != nil {
		logging.Warn("inbox context: could not read the sender's own inbox",
			"session", toID, logging.Err(err))
		return time.Time{}
	}
	var latest time.Time
	for _, report := range reports {
		if report.SenderID == fromID && report.SentAt.After(latest) {
			latest = report.SentAt
		}
	}
	return latest
}

// newerQueuedFrom counts the messages this sender has waiting behind the one
// about to be typed. A message with a delivery stamp is either delivered or
// dropped; either way it is no longer in the queue.
func (p *poller) newerQueuedFrom(sessionID string, msg store.InboxMessage) int {
	reports, err := p.store.ReportsTo(sessionID, inboxContextScan)
	if err != nil {
		logging.Warn("inbox context: could not count what is queued behind a message",
			"session", sessionID, logging.Err(err))
		return 0
	}
	behind := 0
	for _, report := range reports {
		if report.SenderID == msg.SenderID && report.ID > msg.ID && report.DeliveredAt.IsZero() {
			behind++
		}
	}
	return behind
}

// contextWords is the part of the header that differs per message and that
// the reader would otherwise reconstruct for itself.
//
// Nothing here is emitted unconditionally: a message typed in promptly, from
// a session that is still running, with nothing behind it and no earlier
// exchange, carries none of these clauses and costs nothing for them.
func contextWords(msg store.InboxMessage, ctx messageContext) string {
	var parts []string
	switch {
	case ctx.SenderEnded && ctx.Successor.ID != "":
		parts = append(parts, fmt.Sprintf(
			"That session has ended since writing this and %q (session %s) holds its work now, so reply there and read this as a record rather than a live report.",
			textfmt.OneLine(ctx.Successor.Name), ctx.Successor.ID))
	case ctx.SenderEnded:
		parts = append(parts, "That session has ended since writing this, so a reply cannot reach it; read this as a record rather than a live report.")
	}
	if !ctx.LastSentToSender.IsZero() {
		when := ctx.LastSentToSender.Format("15:04")
		if ctx.LastSentToSender.After(msg.SentAt) {
			parts = append(parts, fmt.Sprintf(
				"It was written before your %s message to it, which it had not seen.", when))
		} else {
			parts = append(parts, fmt.Sprintf(
				"You last wrote to it at %s, before this was written.", when))
		}
	}
	switch {
	case ctx.NewerQueued == 1:
		parts = append(parts, "1 newer message from it is already queued behind this one.")
	case ctx.NewerQueued > 1:
		parts = append(parts, fmt.Sprintf(
			"%d newer messages from it are already queued behind this one.", ctx.NewerQueued))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

// ageWords puts a waited message's age beside its timestamp, because a
// reader knows what time the message says and not what time it is now --
// which is the whole of the crossing problem.
//
// Nothing is said below the threshold: a message typed in as it arrives is
// the common case and "0m ago" is noise on every one of them.
const inboxAgeFloor = 5 * time.Minute

func ageWords(age time.Duration) string {
	if age < inboxAgeFloor {
		return ""
	}
	age = age.Round(time.Minute)
	days, rest := age/(24*time.Hour), age%(24*time.Hour)
	hours, minutes := rest/time.Hour, (rest%time.Hour)/time.Minute
	switch {
	case days > 0:
		return fmt.Sprintf(" (%dd%dh ago)", days, hours)
	case hours > 0:
		return fmt.Sprintf(" (%dh%dm ago)", hours, minutes)
	default:
		return fmt.Sprintf(" (%dm ago)", minutes)
	}
}
