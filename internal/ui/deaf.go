package ui

import (
	"fmt"
	"time"

	"github.com/usestring/gate-inbox/internal/store"
)

// A pane can take a keystroke and do nothing with it, and until now the board
// could not tell that from a pane that is simply quiet.
//
// The send says nothing. SendRawAt reports the write, not the delivery -- over
// the pooled pipe that is a byte handed to tmux, and tmux hands it to a pty
// whether anything on the far end is reading or not. So a wedged agent and a
// working one both come back nil, and the board goes on offering the session
// as one a person can answer.
//
// The evidence to tell them apart was already being collected and discarded:
// the echo chase captures the pane after every key and knows whether the frame
// moved. Counting that is the whole mechanism.

const (
	// deafKeyRun is how many keystrokes in a row may draw no repaint at all
	// before the pane is called unresponsive.
	//
	// One means nothing: a key an agent legitimately discards repaints
	// nothing either, as focusEchoCmd's own doc says. What no live TUI does
	// is discard six in a row -- a run that long always contains an Enter, an
	// arrow or a character, and any of them moves something.
	deafKeyRun = 6
)

// deafMark records that a session's pane was found unresponsive, in the state
// it was in at the time.
//
// It is not stored on the session, for the reason a mute is not: it says
// nothing about the session, only about what the board has just observed of
// its pane. Carrying the state is what makes it lapse without a sweep -- the
// poller writes a new status the moment that pane paints anything, and the
// mark falls away with it. A kill and revive, which is the only repair for a
// wedged agent, moves the status twice over.
type deafMark struct {
	status string
	at     time.Time
}

// noteEcho folds one frame into the deaf count. Called for every frame that
// lands on the focused session, whatever fetched it.
//
// A changed frame is proof of life and clears everything, chase or not: the
// pane painted, so it is neither ignoring the operator nor stuck. An
// unchanged frame counts only when a keystroke's own chase brought it back,
// because only then does "nothing changed" mean "nothing came of a key" --
// an unchanged tick frame just means the agent is idle, which is the normal
// state of a session waiting for an answer.
func (m *Model) noteEcho(sess store.Session, chase, changed bool) {
	if changed {
		m.focusDeaf, m.focusDeafID = 0, ""
		m.undeafen(sess.ID)
		return
	}
	if !chase {
		return
	}
	if m.focusDeafID != sess.ID {
		m.focusDeaf, m.focusDeafID = 0, sess.ID
	}
	m.focusDeaf++
	if m.focusDeaf < deafKeyRun {
		return
	}
	m.markDeaf(sess)
	// Said once, on the press that crosses the line, rather than on every
	// key after it: the operator is typing, and a bar that rewrites itself
	// under them is noise. The row's own mark is what carries it from here.
	if m.focusDeaf == deafKeyRun {
		m.errBar.text = deafPaneHint(sess.Name)
	}
}

// deafPaneHint names what the board saw and what it cannot do about it. The
// repair is a restart, and it is the operator's to make: killing an agent
// mid-task is not a thing a heuristic about repaints gets to decide.
func deafPaneHint(name string) string {
	return fmt.Sprintf("%s is not acting on input: %d keys in, nothing on its pane changed. "+
		"Its agent is wedged -- revive the session to recover the conversation.", name, deafKeyRun)
}

// markDeaf files the session as unresponsive in the state it is in now.
func (m *Model) markDeaf(sess store.Session) {
	if m.deaf == nil {
		m.deaf = map[string]deafMark{}
	}
	m.deaf[sess.ID] = deafMark{status: sess.Status, at: sess.LastStatusAt}
}

func (m *Model) undeafen(id string) {
	delete(m.deaf, id)
}

// isDeaf reports whether the board has found this session's pane ignoring
// input, dropping a mark the session has outgrown as it goes. It follows
// isMuted exactly: one definition, read by the rail and by the triage walk,
// so the mark a row paints can never disagree with the queue ctrl+q walks.
func (m *Model) isDeaf(sess store.Session) bool {
	mark, ok := m.deaf[sess.ID]
	if !ok {
		return false
	}
	if sess.Status == mark.status && !sess.LastStatusAt.After(mark.at) {
		return true
	}
	delete(m.deaf, sess.ID)
	return false
}

// isHookless reports whether this session's status is coming off its pane
// because nothing is writing its hook file.
//
// It follows isMuted and isDeaf in being one definition read by everything
// that paints, so the mark on a row can never disagree with what the poller
// decided. Unlike a deaf mark it needs no lapse rule of its own: the poller
// rebuilds the set from the live process tree on every pass, so a session
// that gets its wiring back loses the mark on the next one.
func (m *Model) isHookless(sess store.Session) bool {
	return m.hookless[sess.ID]
}
