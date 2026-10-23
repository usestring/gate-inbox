package store

import "github.com/usestring/gate-inbox/internal/status"

// A session waiting on the network.
//
// While the machine is offline the poller holds every send -- a queued
// message, a launch prompt, a limit continuation -- rather than typing it
// into an agent whose first request would fail. Held, the row still reads
// idle or finished, and a session with work sitting in front of it looks
// like one with nothing to do.
//
// So a row at rest with a send held for it reads pending. Like delegating it
// is never stored: the pane is what the poller derives from, and the overlay
// is laid over the rows on their way out, so the reading flips back the pass
// the network returns and the outbox drains.

// pendingFrom is the statuses a row can rest in and read pending. Working
// and starting rows would hold a send whatever the network did, and a
// waiting row has a dialog in front of it that needs a person first. An
// errored row is in: a lost connection is the likeliest reason it errored,
// and what fixes it is the held send going out once the network is back.
var pendingFrom = map[string]bool{
	status.Idle:       true,
	status.Finished:   true,
	status.Errored:    true,
	status.Delegating: true,
}

// held reports whether sess has a send the outbox would hold: an inbox
// message queued for it, per queued, or a launch prompt or directive not
// yet typed in, or a due limit continuation.
func held(sess Session, queued map[string]int, limitContinuations map[string]bool) bool {
	return queued[sess.ID] > 0 || len(sess.PendingInputs) > 0 && !sess.PendingInputClaimed || limitContinuations[sess.ID]
}

// ApplyOutbox sets Status to pending on every live row at rest with a send
// held for it. Call it only while offline. It rewrites sessions in place and
// returns it.
func ApplyOutbox(sessions []Session, queued map[string]int, limitContinuations map[string]bool) []Session {
	for i, sess := range sessions {
		if sess.Archived || !pendingFrom[sess.Status] || !held(sess, queued, limitContinuations) {
			continue
		}
		sessions[i].Status = status.Pending
	}
	return sessions
}
