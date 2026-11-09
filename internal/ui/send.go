package ui

import (
	"database/sql"
	"errors"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/extension/textfmt"

	"github.com/usestring/gate-inbox/internal/dialog"
	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

const snippetSenderID = store.SnippetSenderID

// sendSentence is the guarded path behind every key that answers a session
// with a canned line: each of the operator's own snippets, from any surface.
//
// One function for every caller, because what makes this safe is the
// sequence -- archived, shell, alive, no dialog, send, un-ack -- and a second
// copy is where a guard goes missing. quoted is passed in rather than
// derived so the commentary can name the snippet the operator recognises
// rather than re-quote a sentence they just read on the footer.
//
// submit false types the sentence and leaves it in the prompt: the snippet's
// AutoSubmit is off, so the operator finishes the line and presses Enter
// themselves. Nothing has been sent yet, so none of what follows a send --
// undo, the operator note, clearing the ack, a landing -- happens.
//
// Reaching the pane is not the agent taking the line, so a submitted send only
// arms a landing (see landing.go): the sentence counts as submitted, and with
// handOver the drain moves on, once the agent is seen to have taken it. A
// refused send -- an archived session, a shell, a dead pane -- arms nothing,
// and its reason stays in the error bar rather than being carried off screen
// by a handover.
func (m *Model) sendSentence(sess store.Session, text, quoted string, submit, handOver bool) tea.Cmd {
	started := time.Now()
	if sess.Archived {
		m.errBar.text = m.displayName(sess) + " is archived — " + m.pressTo(keymap.ContextList, keymap.Restore, "restore it first")
		return nil
	}
	// SendText pastes and presses Enter, so on a shell row the sentence
	// would run as a command. Same guard, same reason as a snippet.
	if m.isShell(sess.Tool) {
		m.errBar.text = shellPromptHint(sess.Name)
		return nil
	}
	if !m.tmux.Exists(sess.ID) {
		if submit && m.unparkForSend(sess, text) {
			return nil
		}
		m.errBar.text = m.deadSessionHint()
		return nil
	}
	if hold, pane := m.dialogHold(sess); hold != "" {
		// A question with a free-text row takes the sentence there instead:
		// the words are typed into its "Type something" row and submitted,
		// which is the same answer the operator would key by hand.
		if submit && m.typeQuestion != nil {
			if held, ok := dialog.Inspect(pane); ok && held.TakesWords() {
				// Offline, the answer would start a turn the agent cannot
				// finish, and the outbox below types into a prompt, not a
				// dialog, so it cannot hold this one: refuse it instead.
				if m.poller.offline() {
					m.errBar.text = "offline — " + quoted + " was not sent; " + m.displayName(sess) + "'s question waits for the network"
					return nil
				}
				return m.typeIntoQuestion(sess, text, quoted, started, handOver)
			}
		}
		m.errBar.text = hold
		return nil
	}
	if !submit {
		if err := pasteFocused(m.tmux, sess.ID, text); err != nil {
			m.errBar.text = err.Error()
			return nil
		}
		m.errBar.text = "typed " + quoted + " into " + m.displayName(sess) + " — press enter to send"
		m.requestRefresh()
		return nil
	}
	// Offline, a line typed into the pane is a turn the agent starts and cannot
	// finish. It waits in the outbox as the operator's own words instead, and
	// the poller types it in once the network is back (see netwatch). No
	// landing check runs: nothing reaches the pane until then.
	if m.poller.offline() {
		id, err := m.store.EnqueueHeldSnippet(store.InboxMessage{
			SessionID:   sess.ID,
			SenderID:    snippetSenderID,
			Body:        text,
			Fingerprint: textfmt.Fingerprint(text),
			SentAt:      time.Now(),
		}, handOver)
		if err != nil {
			m.errBar.text = err.Error()
			return nil
		}
		m.armLanding(sess, started, extension.OperatorSnippet, text, false, handOver, m.echoBaseline(sess.ID))
		if m.heldSnippets == nil {
			m.heldSnippets = map[int64]*pendingLanding{}
		}
		m.heldSnippets[id] = m.landings[sess.ID]
		m.heldSnippets[id].inboxID = id
		delete(m.landings, sess.ID)
		m.errBar.text = "offline — " + quoted + " is held for " + m.displayName(sess) + " and sends when the network returns"
		m.requestRefresh()
		return nil
	}
	landing := m.armLanding(sess, started, extension.OperatorSnippet, text, false, handOver, m.echoBaseline(sess.ID))
	if err := m.tmux.SendText(sess.ID, text); err != nil {
		m.dropLanding(sess.ID)
		m.errBar.text = err.Error()
		return nil
	}
	// The agent has been given something to do, so the operator wants the
	// alert it raises when it is done with it.
	if err := m.store.SetAcked(sess.ID, false); err != nil {
		m.errBar.text = "sent, but clearing the alert ack failed: " + err.Error()
		return landing
	}
	// Nothing on screen says a key that sends a sentence did anything until
	// the pane repaints, and a pane mid-launch can take a moment: the line
	// is the acknowledgement.
	m.errBar.text = "sent " + quoted + " to " + m.displayName(sess)
	m.requestRefresh()
	return landing
}

// dialogHold reports why a pasted sentence must not be sent to this pane yet,
// or "" when the pane rests at a prompt that reads what it is handed.
//
// SendText pastes and presses Enter, and a dialog answers both: the paste
// lands on whatever row the dialog's selection is parked on and the Enter
// commits it. The sentence is never read, and the send reports success, so
// nothing on the board says why the session is still waiting.
//
// The read is RuleMatch rather than the poller's TypingHold, which looks like
// the obvious reuse and is wrong here. TypingHold answers Working the moment
// the tool has not drawn its input line, before it consults a rule at all --
// and a full-screen question dialog is exactly a pane with no input line, so
// on the shape that caused this it reported Working and let the paste through.
// Measured against the real capture in internal/status/testdata: the board
// derives waiting from the rules while TypingHold says working.
//
// Only Waiting holds. The Waiting rules are the dialog patterns, so a rule
// match is a dialog on screen; a question an agent left in prose at a resting
// prompt trips nothing and still takes the sentence. Working is the agent
// mid-turn, where a line sent at the operator's own key is steering and lands
// in the prompt to be read when the turn ends.
//
// pane is the stripped capture the hold was read from, for the caller to
// look at the dialog it found.
//
// It fails open. A capture that errors says nothing about what is on the pane,
// and swallowing the operator's key over a tmux hiccup is worse than the paste
// it is guarding against; a pane that has actually gone is refused above, and
// SendText reports whatever is left.
func (m *Model) dialogHold(sess store.Session) (hold, pane string) {
	if m.engine == nil {
		return "", ""
	}
	raw, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		return "", ""
	}
	pane = ansi.Strip(raw)
	if state, matched := m.engine.RuleMatch(sess.Tool, pane); !matched || state != status.Waiting {
		return "", pane
	}
	return m.displayName(sess) + " has a dialog open - enter it to answer; a pasted line would answer the dialog", pane
}

func (m *Model) resumeHeldSnippets() tea.Cmd {
	var checks []tea.Cmd
	heads, err := m.store.HeadMessages()
	if err != nil {
		m.errBar.text = err.Error()
		return nil
	}
	if !m.heldSnippetsRestored {
		held, err := m.store.HeldSnippets()
		if err != nil {
			m.errBar.text = err.Error()
			return nil
		}
		if m.heldSnippets == nil {
			m.heldSnippets = map[int64]*pendingLanding{}
		}
		for id, handOver := range held {
			if m.heldSnippets[id] == nil {
				m.heldSnippets[id] = &pendingLanding{inboxID: id, via: extension.OperatorSnippet, handOver: handOver}
			}
		}
		m.heldSnippetsRestored = true
	}
	for id, pending := range m.heldSnippets {
		msg, err := m.store.Message(id, snippetSenderID)
		if err != nil {
			m.errBar.text = err.Error()
			if errors.Is(err, sql.ErrNoRows) {
				if err := m.store.ForgetHeldSnippet(id); err != nil {
					m.errBar.text = err.Error()
					continue
				}
				m.poller.snippetRecipients.Delete(id)
				delete(m.heldSnippets, id)
			}
			continue
		}
		if !msg.DroppedAt.IsZero() || msg.SupersededBy != 0 {
			if err := m.store.ForgetHeldSnippet(id); err != nil {
				m.errBar.text = err.Error()
				continue
			}
			m.poller.snippetRecipients.Delete(id)
			delete(m.heldSnippets, id)
			continue
		}
		restoredDelivery := pending.probe == nil && !msg.DeliveredAt.IsZero()
		recipient, ready := m.poller.snippetRecipients.Load(id)
		prepare := msg.DeliveredAt.IsZero() && heads[msg.SessionID].ID == id && (!ready || recipient != msg.SessionID)
		if pending.sess.ID != msg.SessionID || prepare {
			sess, err := m.store.Get(msg.SessionID)
			if err != nil {
				m.errBar.text = err.Error()
				continue
			}
			m.landingGen++
			pending.gen = m.landingGen
			pending.sess = sess
			pending.text = msg.Body
			pending.armed = time.Now()
			pending.probe = m.landingProbeFor(sess, false, m.echoBaseline(sess.ID))
			pending.observed = *pending.probe
		}
		if msg.DeliveredAt.IsZero() {
			if prepare {
				m.poller.snippetRecipients.Store(id, msg.SessionID)
			}
			continue
		}
		delete(m.heldSnippets, id)
		m.poller.snippetRecipients.Delete(id)
		pending.deadline = time.Now().Add(landingWindow)
		if m.landings == nil {
			m.landings = map[string]*pendingLanding{}
		}
		if previous := m.landings[msg.SessionID]; previous != nil {
			m.dropLanding(msg.SessionID)
		}
		m.landings[msg.SessionID] = pending
		if restoredDelivery {
			m.dropLanding(msg.SessionID)
			recordLanding(pending, "restored-unconfirmed")
		} else {
			checks = append(checks, landingLook(msg.SessionID, pending))
		}
	}
	return tea.Batch(checks...)
}

// unparkForSend brings back a session park or hibernate stopped when the
// operator sends to it, and queues the sentence for the poller to type once
// the agent is at its prompt (see maybeSendPendingInputWhenReady). Both
// leave a row that reads dead on purpose and is meant to come back on its
// next message: unpark brings parked sessions back one at a time, which on a
// large set takes the best part of an hour, and a hibernated one waits for
// mail. A reply typed on the board is that message, so it brings the row
// back now rather than showing the dead-row hint. A dead row neither
// stopped gets the hint as before. It reports whether it took the send.
func (m *Model) unparkForSend(sess store.Session, text string) bool {
	if m.store == nil {
		return false
	}
	parked, err := m.store.Parked()
	if err != nil {
		return false
	}
	isParked := slices.Contains(parked, sess.ID)
	hibernated, err := m.store.IsHibernated(sess.ID)
	if err != nil || (!isParked && !hibernated) {
		return false
	}
	why := "is parked"
	if !isParked {
		why = "is hibernated"
	}
	// Queued before the revive, so a revive that fails keeps the message
	// for the next one instead of dropping what the operator typed.
	if err := m.store.QueuePendingInput(sess.ID, text); err != nil {
		m.errBar.text = m.displayName(sess) + " " + why + " and the message could not be queued for it: " + err.Error()
		return true
	}
	if err := m.reviveSession(sess); err != nil {
		m.reportLaunchError(err)
		return true
	}
	ids := []string{sess.ID}
	for _, drop := range []func([]string) error{m.store.RemoveParked, m.store.RemoveDormant, m.store.RemoveInterrupted, m.store.RemoveHibernated} {
		if err := drop(ids); err != nil {
			logging.Warn("revived session still on a parked or hibernated set", "session", sess.ID, logging.Err(err))
		}
	}
	verb := "brought " + m.displayName(sess) + " back from park"
	if !isParked {
		verb = "woke " + m.displayName(sess) + " from hibernation"
	}
	m.errBar.text = ""
	m.reportDone(verb + "; the message goes in once it is at its prompt")
	m.requestRefresh()
	return true
}
