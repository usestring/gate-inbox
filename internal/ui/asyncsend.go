package ui

import (
	"errors"
	"strconv"
	"time"
)

// A paste is not finished when it reaches the pane: the Enter behind it has
// to wait for the pane to draw what it was given, or the pane takes the
// carriage return as part of the paste and the message sits unsent in the
// composer. That wait is capped at a second, and a pane mid-turn -- which is
// every pane an agent is busy in -- spends the whole of it.
//
// The poll pass used to spend it. Measured on a live 76-session board,
// derive.inbox landed on 1.044s, 1.046s, 1.065s, 1.065s and 1.275s -- five
// values inside twenty milliseconds of the cap, which is a timeout and not
// work -- and passes carrying several sends reached 4.8s and 10.5s. Nothing
// in the UI blocks on a pass, so the cost was staleness: rows describing a
// board seconds old, a refresh owed after a form submit or a launch stuck
// behind the pass, and later messages waiting on panes that had nothing to
// do with them.
//
// So the pass hands the paste off and moves on. What makes that safe is that
// every send here already claims its work in the store before the paste goes
// out: the claim is the at-most-once token, and it is written by the pass,
// synchronously, before anything is handed over. A send in flight is one
// this manager already owns, and the settle below is the only thing that
// records the outcome.
const (
	// sendsInFlight caps the pastes this manager may have out at once. The
	// cost of one is the window it waits, not CPU, so the cap is about how
	// many tmux processes a burst may hold rather than throughput: a
	// broadcast to a full board drains in a few passes instead of stalling
	// one, and a pass that finds every slot taken simply leaves the rest
	// queued for the next one. Nothing is claimed for a send that has no
	// slot, so nothing can be stranded by the cap.
	sendsInFlight = 16
	sendDrainPoll = 2 * time.Millisecond
)

// The in-flight keys are prefixed so a message id can never collide with a
// session id.
func messageSend(id int64) string    { return "message:" + strconv.FormatInt(id, 10) }
func inputSend(sessID string) string { return "input:" + sessID }
func limitSend(sessID string) string { return "limit:" + sessID }

// reserveSend takes one of the in-flight slots for key, reporting false when
// they are all taken. A caller reserves before it claims, so a pass that
// cannot send has written nothing it would have to unwind.
func (p *poller) reserveSend(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sendsOut >= sendsInFlight {
		return false
	}
	if p.sending == nil {
		p.sending = make(map[string]bool)
	}
	p.sendsOut++
	p.sending[key] = true
	return true
}

// releaseSend gives back a slot a caller reserved and then decided not to
// use, because the claim behind it did not land.
func (p *poller) releaseSend(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sendsOut--
	delete(p.sending, key)
}

// sendInFlight reports whether this manager has a paste out for key. The
// claim rules read it: a claim with no delivery recorded is abandoned work
// to be retired only when it belongs to a manager that died, and this is how
// a pass tells that from its own paste still waiting for a pane.
func (p *poller) sendInFlight(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sending[key]
}

// runSend pastes text into a session's pane and records the outcome once the
// pane has been given its window and the Enter has gone out, both off the
// pass. settle is called exactly once, with the error the send cost or nil,
// and whatever it returns is surfaced on the next pass the way a background
// capture failure is.
//
// The caller must hold a slot from reserveSend for key; runSend gives it
// back.
func (p *poller) runSend(sessID, key, text string, settle func(error) error) {
	finish := func(sendErr error) {
		// Stamped before settle writes: a pass that reads the outcome must
		// also find the stamp, or it can type the next message against a
		// capture taken before this one was submitted.
		p.mu.Lock()
		if p.settledAt == nil {
			p.settledAt = make(map[string]time.Time)
		}
		p.settledAt[sessID] = time.Now()
		p.mu.Unlock()
		if err := settle(sendErr); err != nil {
			p.mu.Lock()
			p.sendErr = errors.Join(p.sendErr, err)
			p.mu.Unlock()
		}
		p.releaseSend(key)
		// The row this send changed -- a message off the queue, an input
		// consumed -- is worth a frame now rather than at the next tick.
		p.requestRefresh()
	}
	if err := p.tmux.SendTextAsync(sessID, text, finish); err != nil {
		// The paste never reached the pane, so no submit is coming and
		// finish has to be the one to record it.
		finish(err)
	}
}

// settledSince reports whether a send to sessID finished at or after at,
// which leaves a capture taken at that moment describing the pane from
// before the send was submitted. Once a capture is newer the stamp can no
// longer hold anything, so it is dropped.
func (p *poller) settledSince(sessID string, at time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	settled, ok := p.settledAt[sessID]
	if !ok {
		return false
	}
	if at.After(settled) {
		delete(p.settledAt, sessID)
		return false
	}
	return true
}

// awaitSends blocks until every paste this manager has out has been
// submitted and recorded. Tests use it to pick up after a pass; nothing on
// the poll path waits here.
func (p *poller) awaitSends(within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		p.mu.Lock()
		out := p.sendsOut
		p.mu.Unlock()
		if out == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(sendDrainPoll)
	}
}
