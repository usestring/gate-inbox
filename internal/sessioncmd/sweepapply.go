package sessioncmd

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/usestring/gate-inbox/extension"
	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sweep"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// SweepApplyOptions choose how much of a sweep's report an apply carries
// out. The zero value is the safe subset: hibernate or archive rows nobody
// is using, and hold everything else.
type SweepApplyOptions struct {
	// DryRun makes every check and changes nothing.
	DryRun bool
	// Revive carries out the revive proposals too. Each one starts a
	// process, so it is asked for rather than assumed.
	Revive bool
	// KillWaiting stops sessions parked on a dialog nobody answered. The
	// question is lost with the process.
	KillWaiting bool
	// Force archives or kills a row although it has mail waiting, children
	// still on the board, task claims or work running under it.
	Force bool
}

// SweepApply carries out a sweep's proposals through the code the archive,
// revive, kill and hibernate tools run, and says what became of each.
//
// A row proposed for archive that is idle or finished and passes every
// check hibernate makes is hibernated instead: an archive hands back task
// claims, files the fan-out and starts the seven-day deletion clock, where
// a hibernated row keeps all three and wakes on its next message. Whatever
// would lose something is held with the reason, unless opts asks for it.
//
// Like park and hibernate it needs no calling session, so an operator runs
// it from a plain shell; from inside a session, that session and the ones
// above it are left alone.
func (s *Sessions) SweepApply(sessionID string, proposals []sweep.Proposal, opts SweepApplyOptions) (applied sweep.Applied, err error) {
	op := start("sessioncmd.sweep_apply", tracing.Attr{Key: "dry_run", Value: opts.DryRun})
	defer func() { op.done(&err, tracing.Attr{Key: "proposals", Value: len(proposals)}) }()
	runtime, err := s.open()
	if err != nil {
		return sweep.Applied{}, err
	}
	defer runtime.store.Close()
	// Every check below starts from the status the board last wrote. With
	// no board polling, a row that reads idle may have been working for
	// days, so the plan can be printed but not carried out.
	if awake, err := runtime.managerAwake(time.Now()); err != nil {
		return sweep.Applied{}, err
	} else if !awake && !opts.DryRun {
		return sweep.Applied{}, errors.New("the board is not running, so the statuses the sweep read may be out of date; open it and run the sweep again, or pass --dry-run to see the plan")
	}
	pass := sweepPass{
		s: s, runtime: runtime, opts: opts, callerID: sessionID,
		now: time.Now(), procs: onceTable(agentsession.ProcessTable),
	}
	applied = sweep.Applied{DryRun: opts.DryRun, Outcomes: []sweep.Outcome{}, Counts: map[string]int{}}
	for _, proposal := range proposals {
		outcome := sweep.Outcome{SessionID: proposal.SessionID, Name: proposal.Name, Proposed: proposal.Action}
		outcome.Outcome, outcome.Detail, outcome.ReleasedTasks = pass.apply(proposal)
		applied.Outcomes = append(applied.Outcomes, outcome)
		applied.Counts[outcome.Outcome]++
	}
	return applied, nil
}

// sweepPass is one apply: the board is read again before every proposal,
// since each action changes what the next one finds.
type sweepPass struct {
	s        *Sessions
	runtime  *runtime
	opts     SweepApplyOptions
	callerID string
	now      time.Time
	procs    func() ([]byte, error)
}

// apply carries out one proposal and returns the outcome, its detail and
// the task claims it released.
func (p *sweepPass) apply(proposal sweep.Proposal) (string, string, int) {
	held := func(format string, args ...any) (string, string, int) {
		return sweep.Held, fmt.Sprintf(format, args...), 0
	}
	if proposal.Action == sweep.Check {
		return held("a check is a question for message_status, not an action")
	}
	rows, err := p.runtime.store.ListSessions(true)
	if err != nil {
		return sweep.Failed, err.Error(), 0
	}
	at := slices.IndexFunc(rows, func(row store.Session) bool { return row.ID == proposal.SessionID })
	if at < 0 {
		return held("it is no longer on the board")
	}
	sess := rows[at]
	since := sess.LastStatusAt
	if since.IsZero() {
		since = sess.CreatedAt
	}
	// The proposal was made from an earlier read. A row that has moved
	// since is somebody using it.
	if sess.Status != proposal.Status || sess.Archived != proposal.Archived || since.UTC().Format(time.RFC3339) != proposal.Since {
		return held("it changed after the sweep read it; it is now %s", sess.Status)
	}
	if hibernated, err := p.runtime.store.IsHibernated(sess.ID); err != nil {
		return sweep.Failed, err.Error(), 0
	} else if hibernated && !sess.Archived {
		return held("it is hibernated, and wakes on its next message")
	}
	if p.callerID != "" {
		if sess.ID == p.callerID {
			return held("it is the session running this sweep")
		}
		if slices.ContainsFunc(store.Descendants(rows, sess.ID), func(row store.Session) bool { return row.ID == p.callerID }) {
			return held("the session running this sweep is under it")
		}
	}
	if p.runtime.cfg.Tools[sess.Tool].Shell {
		return held("it is a terminal, not an agent; close it with the terminal tools")
	}
	if sess.TmuxPaneID != "" {
		// What the child cleanup says of one: it is somebody's own window.
		return held("it is a pane the board adopted rather than started; act on it by id if you mean to")
	}
	switch proposal.Action {
	case sweep.Archive:
		return p.archive(sess, rows)
	case sweep.Revive:
		if !p.opts.Revive {
			return held("a revive starts a process; pass --revive to carry it out")
		}
		return p.revive(sess)
	case sweep.Kill:
		if !p.opts.KillWaiting {
			return held("it is waiting on a dialog, and a kill loses the question; pass --kill-waiting to stop it")
		}
		return p.kill(sess, rows)
	}
	return held("the sweep has no way to apply %q", proposal.Action)
}

// archive hibernates sess where hibernate would take it, and archives it
// otherwise unless something would be lost.
func (p *sweepPass) archive(sess store.Session, rows []store.Session) (string, string, int) {
	resting := sess.Status == status.Idle || sess.Status == status.Finished
	var blocker string
	if resting {
		// The proposal already held the row to the sweep's own threshold,
		// so no rest period is asked for here.
		if blocker = p.s.hibernateBlocker(p.runtime, sess, rows, 0, p.now, p.procs); blocker == "" {
			return p.hibernate(sess)
		}
	}
	if !p.opts.Force {
		if lost := p.lost(sess, rows, true); lost != "" {
			return sweep.Held, lost, 0
		}
	}
	if p.opts.DryRun {
		return sweep.Archived, archiveDetail(blocker), 0
	}
	if err := p.runtime.reach(sess); err != nil {
		return sweep.Failed, err.Error(), 0
	}
	below, err := p.s.fileDescendants(p.runtime, sess, p.callerID)
	if err != nil {
		return sweep.Failed, err.Error(), below
	}
	filed, err := p.s.file(p.runtime, sess, true)
	if err != nil {
		return sweep.Failed, err.Error(), below
	}
	return sweep.Archived, archiveDetail(blocker), below + filed.ReleasedTasks
}

// archiveDetail says why a resting row was archived rather than hibernated.
func archiveDetail(blocker string) string {
	if blocker == "" {
		return ""
	}
	return "not hibernated: " + blocker
}

func (p *sweepPass) hibernate(sess store.Session) (string, string, int) {
	const detail = "its row, mail and task claims are kept; its next message wakes it"
	result := HibernateResult{DryRun: p.opts.DryRun}
	p.s.hibernateOne(p.runtime, sess, &result)
	if len(result.Errors) > 0 {
		return sweep.Failed, result.Errors[0], 0
	}
	return sweep.Hibernated, detail, 0
}

func (p *sweepPass) revive(sess store.Session) (string, string, int) {
	if p.opts.DryRun {
		return sweep.Revived, "", 0
	}
	detail := ""
	if sess.Archived {
		if _, err := p.s.file(p.runtime, sess, false); err != nil {
			return sweep.Failed, err.Error(), 0
		}
		sess.Archived = false
		detail = "restored to the active list first"
	}
	if _, err := p.s.reviveRow(p.runtime, sess); err != nil {
		return sweep.Failed, err.Error(), 0
	}
	return sweep.Revived, detail, 0
}

func (p *sweepPass) kill(sess store.Session, rows []store.Session) (string, string, int) {
	if !p.opts.Force {
		if lost := p.lost(sess, rows, false); lost != "" {
			return sweep.Held, lost, 0
		}
	}
	if p.opts.DryRun {
		return sweep.Killed, "", 0
	}
	if err := p.runtime.reach(sess); err != nil {
		return sweep.Failed, err.Error(), 0
	}
	killed, err := p.s.stopSession(p.runtime, sess, extension.KillByCLI, p.callerID, killFor(p.runtime, sess.ID))
	if err != nil {
		return sweep.Failed, err.Error(), 0
	}
	return sweep.Killed, "", killed.ReleasedTasks
}

// lost is what ending sess would cost, or "" when nothing: a session under
// it that is still on the board, a task claim, which goes back to pending,
// mail, which is dropped, and anything running under the process. archive
// says the row is to be filed away too, which takes every session under it
// along and overrides the keep its spawner asked for.
func (p *sweepPass) lost(sess store.Session, rows []store.Session, archive bool) string {
	hibernated, err := p.runtime.store.Hibernated()
	if err != nil {
		return err.Error()
	}
	for _, row := range liveDescendants(rows, sess.ID) {
		shown := store.Shown(row, hibernated)
		if shown == status.Dead {
			continue
		}
		if archive {
			return fmt.Sprintf("%s (id %s) under it is %s and would be archived with it; pass --force to archive both", row.Name, row.ID, shown)
		}
		return fmt.Sprintf("%s (id %s) under it is %s and would be left with nobody to report to; pass --force to kill it anyway", row.Name, row.ID, shown)
	}
	claims, err := p.runtime.store.QueryTasks(store.TaskQuery{Owner: sess.ID, States: []string{store.TaskInProgress}, SkipBody: true})
	if err != nil {
		return err.Error()
	}
	if n := len(claims.Tasks); n > 0 {
		return fmt.Sprintf("it holds %d task claim(s), which would go back to pending; pass --force to release them", n)
	}
	if n, err := p.runtime.store.UndeliveredCount(sess.ID); err != nil {
		return err.Error()
	} else if n > 0 {
		return fmt.Sprintf("%d message(s) are waiting to be typed in; pass --force to drop them", n)
	}
	if archive {
		if keep, err := p.runtime.store.KeepChild(sess.ID); err != nil {
			return err.Error()
		} else if keep {
			return "it was spawned with keep; pass --force to archive it anyway"
		}
	}
	if err := p.runtime.reach(sess); err != nil {
		return err.Error()
	}
	if !p.runtime.driver.Exists(sess.ID) {
		return ""
	}
	if busy := p.s.inFlight(p.runtime, sess, nil, p.procs); busy != "" {
		return busy + "; pass --force to end it anyway"
	}
	return ""
}
