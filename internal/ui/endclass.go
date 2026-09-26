package ui

import (
	"fmt"
	"slices"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/store"
)

// A dead row is a session whose agent is gone, and the startup offer used to
// treat every one of them as a loss: a session killed from the CLI, one the
// operator quit with /exit, one whose pane they closed in tmux and one a
// reboot took all came back as "restore these?". Only the last is a loss.
// Offering the others back undoes what the operator just did, and teaches
// them to dismiss the offer unread.
//
// So each dead row is classified from the evidence the board keeps, strongest
// first: an end the operator asked for is recorded the moment it happens (see
// store.RecordEnd); how the agent itself exited is recorded by the pane's
// launch script (hooks.ExitFile); and whether the tmux server it ran on has
// since restarted says whether the machine took it. What none of those
// settles is called unknown and offered with its evidence, rather than
// guessed either way.

type endVerdict int

const (
	// endUnknown is offered back, labelled, with the evidence shown.
	endUnknown endVerdict = iota
	// endDied is offered back: the operator did not choose this.
	endDied
	// endByOperator is never offered: somebody ended it on purpose.
	endByOperator
)

// endClass is one row's verdict and the plain-words reason for it.
type endClass struct {
	verdict endVerdict
	why     string
}

// endEvidence is everything the classification reads, gathered once per
// startup offer. The zero value knows nothing, which classifies every row as
// unknown: the offer still stands, it just cannot say why.
type endEvidence struct {
	ends   map[string]store.SessionEnd
	parked map[string]bool
	// exit reads the recorded exit status of a session's last agent.
	exit func(id string) (code int, at time.Time, ok bool)
	// serverStarted is when the tmux server now running started; serverUp is
	// false when there is none or it could not be read.
	serverStarted time.Time
	serverUp      bool
	// serverKnown is false when the server was never asked, so its absence
	// is not evidence.
	serverKnown bool
}

// exitClockSlack absorbs the difference between the launch stamp, taken
// before the pane starts, and a file time on a filesystem with coarse
// timestamps.
const exitClockSlack = 2 * time.Second

// serverClockSlack is how far before the server's start stamp a launch can
// fall and still have been on that server.
const serverClockSlack = 5 * time.Second

// classifyEnd decides one dead row.
func classifyEnd(sess store.Session, ev endEvidence) endClass {
	if end, ok := ev.ends[sess.ID]; ok && end.Matches(sess) {
		switch end.Reason {
		case store.EndArchived:
			return endClass{endByOperator, "archived"}
		case store.EndParked:
			return endClass{endByOperator, "parked; unpark brings it back"}
		default:
			return endClass{endByOperator, "ended on purpose (kill)"}
		}
	}
	if ev.parked[sess.ID] {
		return endClass{endByOperator, "parked; unpark brings it back"}
	}
	if ev.exit != nil {
		// A record older than this launch is the previous agent's.
		if code, at, ok := ev.exit(sess.ID); ok && !at.Before(sess.LaunchTime().Add(-exitClockSlack)) {
			return classifyExit(code)
		}
	}
	if ev.serverKnown {
		if !ev.serverUp {
			return endClass{endDied, "the tmux server it ran on is gone"}
		}
		// tmux stamps its start in whole seconds, and a session can be the
		// one whose launch started the server, so a launch just before the
		// stamp is on this server rather than an earlier one.
		if sess.LaunchTime().Add(serverClockSlack).Before(ev.serverStarted) {
			return endClass{endDied, "tmux restarted or the machine rebooted after it launched"}
		}
		// The server that ran it is still up and the pane is gone: it was
		// closed inside tmux -- kill-pane, kill-window, or a shell exited by
		// hand -- which is the operator's doing, not a loss.
		return endClass{endByOperator, "its pane was closed in tmux"}
	}
	return endClass{endUnknown, "no record of how it ended"}
}

// classifyExit reads the status the launch script recorded.
func classifyExit(code int) endClass {
	switch {
	case code == 0:
		return endClass{endByOperator, "the agent exited normally (/exit or quit)"}
	case code == 128+2:
		return endClass{endByOperator, "interrupted with ctrl+c"}
	case code >= 128+17 && code <= 128+22:
		// A job-control stop (SIGSTOP, SIGTSTP, SIGTTIN, SIGTTOU, and
		// SIGCHLD/SIGCONT around them) is recorded when the agent stopped,
		// not how it finally ended.
		return endClass{endUnknown, fmt.Sprintf("stopped (status %d), final exit not recorded", code)}
	case code > 128:
		return endClass{endDied, fmt.Sprintf("the agent was killed by signal %d", code-128)}
	default:
		return endClass{endDied, fmt.Sprintf("the agent crashed (exit status %d)", code)}
	}
}

// loadEndEvidence gathers what classifyEnd reads. Anything unreadable is left
// out, which moves rows toward unknown rather than toward a confident verdict.
func (m *Model) loadEndEvidence() endEvidence {
	var ev endEvidence
	if m.store != nil {
		if ends, err := m.store.SessionEnds(); err == nil {
			ev.ends = ends
		} else {
			logging.Info("session ends unreadable", logging.Err(err))
		}
		if parked, err := m.store.Parked(); err == nil && len(parked) > 0 {
			ev.parked = make(map[string]bool, len(parked))
			for _, id := range parked {
				ev.parked[id] = true
			}
		}
	}
	if m.hooks != nil {
		ev.exit = m.hooks.ReadExit
	}
	if m.tmux != nil {
		ev.serverStarted, ev.serverUp = m.tmux.ServerStarted()
		ev.serverKnown = true
	}
	return ev
}

// endLedger is the classification of every dead row the startup offer
// considered, by id, so the card can say why each is there.
type endLedger map[string]endClass

func (l endLedger) count(verdict endVerdict) int {
	n := 0
	for _, class := range l {
		if class.verdict == verdict {
			n++
		}
	}
	return n
}

// ids lists the rows with a verdict, sorted, for stable output.
func (l endLedger) ids(verdict endVerdict) []string {
	var out []string
	for id, class := range l {
		if class.verdict == verdict {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}
