package ui

import (
	"time"

	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// A turn that ended with a shell left running behind it.
//
// Claude names the shells, monitors and tasks a turn left running in a tail
// on its turn-end summary, and the pane reads that as working: Claude wakes
// itself when one ends, so the session is not done. But it is not working
// either. Nothing is on screen and nothing spends tokens, and the shell may
// be a wait loop on something only a person can unblock, so the row reads
// background: off the working count, out of the busy tint, and open to a
// queued message, while the child sweep still keeps its hands off it.
//
// The tail also outlives the work. On 2026-10-12, 8 of the 33 working rows
// were turns that had ended between 1 and 129 hours before: one had no shell
// left at all, and the rest were wait loops for files and builds that were
// never coming, idle at 0% CPU. Each held its row working, kept the child
// sweep off it, and held its parent delegating.
//
// Work that is still going writes: the shell ends and Claude takes a turn,
// or a background agent streams into its own transcript. So once the
// conversation's transcript has been quiet for status.BackgroundTailTrust,
// the tail is no longer believed and the row reads as the turn that ended.
// When the shell does end, Claude's next turn makes it working again.

// settleBackground returns derived, or what a working pane reads as when
// only a background tail keeps it working: background while the tail is
// believed, and the resting state of the turn once it has gone quiet.
func (p *poller) settleBackground(sess store.Session, pane, derived string, now time.Time) string {
	if derived != status.Working {
		return derived
	}
	settled, ok := status.SettleBackground(p.engine, sess.Tool, pane, func() (time.Duration, bool) {
		return agentsession.ClaudeTranscriptAge(sess.Cwd, sess.AgentSessionID, now)
	})
	if !ok {
		return derived
	}
	if settled == status.Finished && sess.Acked {
		return status.Idle
	}
	return settled
}
