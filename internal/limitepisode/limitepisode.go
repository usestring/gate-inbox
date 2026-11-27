// Package limitepisode is the board's one answer to "what should happen to a
// session its subscription's usage limit has stopped?". The poller records
// each limit it sees as an episode (Observe), and the limit card, the
// board's own continuation after a reset, and the extensions that move
// sessions between accounts all act on the same Decide rather than each
// re-deriving it from the pane.
//
// An episode is one limit banner on one session. It belongs to the launch
// and the account it was first seen on: a relaunch replays the
// conversation, banner included, so the same text after a relaunch is the
// old limit scrolled back into view, not a new turn being cut off.
package limitepisode

import (
	"time"

	"github.com/usestring/gate-inbox/internal/accounts"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// MoveGrace is how long the limit card holds back while an extension is
// expected to move a session off its limited account in the background. A
// session still limited on the same launch after that has not been moved,
// and the card offers the operator a way off it.
const MoveGrace = 2 * time.Minute

// Observe is the episode a session's pane shows now, given the one on
// record: nil when the record already says it. banner is the pane's usage
// limit banner, never empty, and never a Codex capacity failure, which
// keeps its own retry schedule.
//
// A banner new to the session is a cut turn: nothing prints a limit but a
// turn the provider refused, whether the session was mid-task or idle until
// somebody wrote to it. The same banner on a later launch is a replay, which
// keeps the account it was raised on, so Decide can tell a session moved
// off that account from one relaunched where it was.
func Observe(prev *store.LimitRecovery, sess store.Session, banner string, now time.Time) *store.LimitRecovery {
	launch := sess.LaunchTime()
	if prev != nil && prev.CapacityDelay == 0 && prev.Banner == banner {
		if prev.LaunchAt.Equal(launch) {
			return nil
		}
		next := *prev
		next.LaunchAt = launch
		next.Replayed = true
		// A relaunch on the same account that still shows the limit is the
		// limit holding: the operator may be offered the card again.
		next.Offered = false
		return &next
	}
	next := &store.LimitRecovery{Banner: banner, LaunchAt: launch, Account: sess.Account, SeenAt: now}
	if resetAt, ok := status.LimitReset(banner, now); ok {
		next.ResetAt = resetAt
	}
	return next
}

// Over says the episode on record has ended on the session's current
// launch: its banner is gone and the session went on to other work. An
// episode from an earlier launch is kept while the relaunch's pane is still
// drawing, so its replayed banner is recognised when it shows.
func Over(ep store.LimitRecovery, sess store.Session) bool {
	return ep.LaunchAt.Equal(sess.LaunchTime())
}

// Facts is what Decide needs to know about a session besides its episode.
type Facts struct {
	Session store.Session
	// Running says the session has a live agent process. A hibernated,
	// parked or exited session is moved, if ever, when it is next woken.
	Running bool
	// Successor says the conversation was migrated to another live session
	// after the episode began (see Successor).
	Successor bool
	// Routed says the build's extension chooses accounts, so it is the one
	// that moves a limited session to another account.
	Routed bool
	// Accounts says the session's CLI can run on named accounts.
	Accounts bool
}

// Decision is what to do about a session's limit episode.
type Decision struct {
	// Limited says the session is held at a limit that is still its own: not
	// archived, not migrated away, and still on the account that hit it.
	Limited bool
	// Cut says the limit refused a live turn, so somebody is waiting on the
	// work. Only a cut session is worth relaunching and continuing; one
	// merely showing an old banner is left until it is next asked something.
	Cut bool
	// Move says an extension should move the session to another account and
	// continue it, once. Only a cut, running, unmuted session is moved.
	Move bool
	// Continue says the board may continue the session once the banner's
	// reset time passes, for a CLI that keeps no reset timer of its own.
	Continue bool
	// CardAt is when the limit card may open for the episode; zero is never.
	CardAt time.Time
	// Subscription says the card offers another account of the same CLI.
	Subscription bool
}

// Decide is the one decision about ep. A nil ep, and a capacity retry, decide
// nothing.
func Decide(ep *store.LimitRecovery, f Facts) Decision {
	if ep == nil || ep.CapacityDelay > 0 {
		return Decision{}
	}
	sess := f.Session
	if sess.Archived || f.Successor || accounts.Normalize(ep.Account) != accounts.Normalize(sess.Account) {
		return Decision{}
	}
	d := Decision{Limited: true, Cut: !ep.Replayed, Subscription: f.Accounts}
	active := f.Running && !sess.Muted
	d.Move = d.Cut && active && f.Routed && f.Accounts
	// Claude keeps its own reset timer and offers to continue at the reset
	// from its limit menu; a second scheduler would duplicate it.
	d.Continue = d.Cut && active && !ep.ResetAt.IsZero() && sess.Tool == "codex"
	if !sess.Muted && !ep.Offered {
		d.CardAt = ep.SeenAt
		if d.CardAt.IsZero() {
			d.CardAt = ep.LaunchAt
		}
		if d.Move {
			d.CardAt = d.CardAt.Add(MoveGrace)
		}
	}
	return d
}

// Successor says sessions hold a live session migrated from sess after its
// episode began: the operator, or an agent, already moved the conversation
// on, so the limit is no longer the session's to act on.
func Successor(sessions []store.Session, sess store.Session, ep store.LimitRecovery) bool {
	if sess.MigrationID == "" {
		return false
	}
	since := ep.SeenAt
	if since.IsZero() {
		since = ep.LaunchAt
	}
	for _, other := range sessions {
		if other.ID != sess.ID && !other.Archived && store.Linked(other, sess) && other.CreatedAt.After(since) {
			return true
		}
	}
	return false
}
