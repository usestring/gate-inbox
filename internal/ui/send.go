package ui

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/usestring/gate-inbox/internal/keymap"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// sendSentence is the guarded path behind every key that answers a session
// with a canned line: each of the operator's own snippets, from any surface.
//
// One function for every caller, because what makes this safe is the
// sequence -- archived, shell, alive, no dialog, send, un-ack -- and a second
// copy is where a guard goes missing. quoted is passed in rather than
// derived so the commentary can name the snippet the operator recognises
// rather than re-quote a sentence they just read on the footer.
//
// It reports whether the sentence actually reached the agent. Auto-proceed
// needs that answer: a refused send -- an archived session, a shell, a dead
// pane -- has left the session unanswered, and the reason is in the error bar,
// where handing the session over would take it off screen before it was read.
// See autoproceed.go.
func (m *Model) sendSentence(sess store.Session, text, quoted string) bool {
	if sess.Archived {
		m.errBar.text = m.displayName(sess) + " is archived — press " + m.cap(keymap.ContextList, keymap.Restore) + " to restore it first"
		return false
	}
	// SendText pastes and presses Enter, so on a shell row the sentence
	// would run as a command. Same guard, same reason as the quick prompt.
	if m.isShell(sess.Tool) {
		m.errBar.text = shellPromptHint(sess.Name)
		return false
	}
	if !m.tmux.Exists(sess.ID) {
		m.errBar.text = m.deadSessionHint()
		return false
	}
	if hold := m.dialogHold(sess); hold != "" {
		m.errBar.text = hold
		return false
	}
	if err := m.tmux.SendText(sess.ID, text); err != nil {
		m.errBar.text = err.Error()
		return false
	}
	m.noteSubmission(sess)
	// The agent has been given something to do, so the operator wants the
	// alert it raises when it is done with it.
	if err := m.store.SetAcked(sess.ID, false); err != nil {
		m.errBar.text = "sent, but clearing the alert ack failed: " + err.Error()
		return true
	}
	// Nothing on screen says a key that sends a sentence did anything until
	// the pane repaints, and a pane mid-launch can take a moment: the line
	// is the acknowledgement.
	m.errBar.text = "sent " + quoted + " to " + m.displayName(sess)
	m.requestRefresh()
	return true
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
// It fails open. A capture that errors says nothing about what is on the pane,
// and swallowing the operator's key over a tmux hiccup is worse than the paste
// it is guarding against; a pane that has actually gone is refused above, and
// SendText reports whatever is left.
func (m *Model) dialogHold(sess store.Session) string {
	if m.engine == nil {
		return ""
	}
	pane, err := m.tmux.CapturePane(sess.ID)
	if err != nil {
		return ""
	}
	if state, matched := m.engine.RuleMatch(sess.Tool, ansi.Strip(pane)); !matched || state != status.Waiting {
		return ""
	}
	return m.displayName(sess) + " has a dialog open - enter it to answer; a pasted line would answer the dialog"
}
