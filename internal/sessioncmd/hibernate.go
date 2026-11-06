package sessioncmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/usestring/gate-inbox/internal/agentsession"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
)

// Hibernate gives an idle agent's memory back without ending its work.
//
// An agent at rest still holds its whole process: one board carried 117
// Claude agents with no turn in three days, 34G between them, a median of
// 300M each. Archiving frees that but is the wrong tool -- it drops the
// queue, hands back task claims, files the fan-out and starts a seven-day
// deletion clock. Hibernate stops only the process. The row stays on the
// active list, its mail is held rather than dropped (see
// store.ResolveEndedRecipient), its claims stay, and the next message for
// it, from anyone, relaunches it on its own conversation (wakeForSend, and
// WakeHibernated on the board's poll for mail that arrives another way).
//
// What the stop loses is what a kill loses: the running process and
// anything still running under it. So only a session that has nothing in
// flight is eligible -- see hibernateBlocker. The rules are deliberately
// stricter than the child sweep's, whose looser 2026-09-11 version ended
// eight working children: a status read off a pane is not enough on its
// own here either.

// HibernateResult is what one hibernate call did.
type HibernateResult struct {
	DryRun     bool      `json:"dry_run" jsonschema:"whether this was a rehearsal that stopped nothing"`
	Hibernated []Session `json:"hibernated" jsonschema:"sessions stopped and marked to wake on their next message"`
	// Held names the sessions the sweep looked at and left running, with
	// the reason, so an operator can see why a row they expected is still up.
	Held   []string `json:"held,omitempty" jsonschema:"sessions left running, with the reason"`
	Errors []string `json:"errors,omitempty" jsonschema:"sessions that could not be hibernated, with the reason"`
}

// Hibernate stops targetID's agent and marks it to wake on its next
// message. It refuses a session that is not at rest; the reason says what
// holds it. Like park it needs no calling session, so an operator can run it
// from a plain shell, but a session cannot hibernate itself.
func (s *Sessions) Hibernate(sessionID, targetID string, dryRun bool) (result HibernateResult, err error) {
	defer start("sessioncmd.hibernate", sessionAttr(targetID)).done(&err)
	if targetID == sessionID {
		return HibernateResult{}, errors.New("a session cannot hibernate itself")
	}
	runtime, err := s.open()
	if err != nil {
		return HibernateResult{}, err
	}
	defer runtime.store.Close()
	target, err := runtime.agent(targetID)
	if err != nil {
		return HibernateResult{}, err
	}
	rows, err := runtime.store.ListSessions(false)
	if err != nil {
		return HibernateResult{}, err
	}
	if reason := s.hibernateBlocker(runtime, target, rows, 0, time.Now(), agentsession.ProcessTable); reason != "" {
		return HibernateResult{}, fmt.Errorf("session %s cannot be hibernated: %s", target.ID, reason)
	}
	result.DryRun = dryRun
	s.hibernateOne(runtime, target, &result)
	if len(result.Errors) > 0 {
		return result, errors.New(result.Errors[0])
	}
	return result, nil
}

// HibernateIdle hibernates every session on the board that has rested at
// least after and passes every check Hibernate makes. It is the opt-in
// sweep: the board runs it on [sessions] hibernate_after, and an operator
// can run it by hand, with dryRun for the plan alone.
func (s *Sessions) HibernateIdle(after time.Duration, dryRun bool) (result HibernateResult, err error) {
	defer start("sessioncmd.hibernate_idle").done(&err)
	if after <= 0 {
		return HibernateResult{}, errors.New("hibernate needs a rest period greater than zero")
	}
	runtime, err := s.open()
	if err != nil {
		return HibernateResult{}, err
	}
	defer runtime.store.Close()
	rows, err := runtime.store.ListSessions(false)
	if err != nil {
		return HibernateResult{}, err
	}
	result.DryRun = dryRun
	now := time.Now()
	procs := onceTable(agentsession.ProcessTable)
	for _, sess := range rows {
		if sess.Archived || sess.Status == status.Dead || runtime.cfg.Tools[sess.Tool].Shell {
			continue
		}
		if reason := s.hibernateBlocker(runtime, sess, rows, after, now, procs); reason != "" {
			result.Held = append(result.Held, fmt.Sprintf("%s (id %s): %s", sess.Name, sess.ID, reason))
			continue
		}
		s.hibernateOne(runtime, sess, &result)
	}
	return result, nil
}

// hibernateBlocker is why sess must keep running, or "" when nothing does.
// Each check stands for something a stop would lose.
func (s *Sessions) hibernateBlocker(runtime *runtime, sess store.Session, rows []store.Session, after time.Duration, now time.Time, procs func() ([]byte, error)) string {
	switch {
	case sess.Archived:
		return "it is archived"
	case runtime.cfg.Tools[sess.Tool].Shell:
		return "it is a terminal, not an agent"
	case sess.TmuxPaneID != "":
		// An adopted pane is somebody else's window; ending it is a kill.
		return "it is an adopted pane, not a session the board started"
	case sess.Status != status.Idle && sess.Status != status.Finished:
		// Working loses its turn; waiting is a dialog or a question someone
		// has yet to answer.
		return "it is " + sess.Status + ", not idle or finished"
	case after > 0 && now.Sub(sess.LastStatusAt) < after:
		return fmt.Sprintf("it has rested %s of %s", now.Sub(sess.LastStatusAt).Round(time.Minute), after)
	}
	if warning := reviveWarning(runtime, sess); warning != "" {
		return "it could not be woken as it was: " + warning
	}
	if err := runtime.reach(sess); err != nil {
		return err.Error()
	}
	if !runtime.driver.Exists(sess.ID) {
		return "it is not running"
	}
	if n, err := runtime.store.UndeliveredCount(sess.ID); err != nil {
		return err.Error()
	} else if n > 0 {
		return fmt.Sprintf("%d message(s) are waiting to be typed in", n)
	}
	for _, row := range rows {
		if row.ParentID != sess.ID || row.Archived {
			continue
		}
		// A child that is still going will report to this session or ask
		// it something; its parent has to be there to answer.
		switch row.Status {
		case status.Dead, status.Idle, status.Finished:
		default:
			return fmt.Sprintf("child %s (id %s) is %s", row.Name, row.ID, row.Status)
		}
	}
	if pending := backgroundPending(sess); len(pending) > 0 {
		return fmt.Sprintf("its transcript has background work running: %v", pending)
	}
	if pid, err := runtime.driver.PanePID(sess.ID); err == nil && pid > 0 {
		if shells := agentsession.ClaudeShellsUnder(pid, procs); shells > 0 {
			return fmt.Sprintf("%d background shell(s) are running under it", shells)
		}
	}
	return ""
}

// onceTable reads the process table on first use and hands the same rows
// to every later caller: a sweep asks about every candidate, and one ps per
// candidate on a board of a hundred agents is a hundred forks.
func onceTable(read func() ([]byte, error)) func() ([]byte, error) {
	var out []byte
	var err error
	done := false
	return func() ([]byte, error) {
		if !done {
			out, err = read()
			done = true
		}
		return out, err
	}
}

// backgroundPending is the background tasks a Claude transcript started and
// has not heard end.
func backgroundPending(sess store.Session) []string {
	if sess.AgentSessionID == "" {
		return nil
	}
	path, err := agentsession.ClaudeTranscriptPath(sess.Cwd, sess.AgentSessionID)
	if err != nil {
		return nil
	}
	ids, _ := agentsession.NewBackground().Pending(path)
	return ids
}

// hibernateOne stops sess and records it. The mark goes in before the stop:
// the board's poll drops the queue of a row it finds dead, and between the
// kill and the mark it would have read this one as ended.
func (s *Sessions) hibernateOne(runtime *runtime, sess store.Session, result *HibernateResult) {
	if result.DryRun {
		result.Hibernated = append(result.Hibernated, runtime.sessionInfo(sess, true, false))
		return
	}
	fail := func(err error) {
		result.Errors = append(result.Errors, fmt.Sprintf("%s (id %s): %v", sess.Name, sess.ID, err))
	}
	if err := runtime.store.AddHibernated([]string{sess.ID}); err != nil {
		fail(err)
		return
	}
	// endSession, not a kill: a kill hands back task claims and drops the
	// queue, and both are this row's to keep.
	if err := s.endSession(runtime, sess, store.EndHibernated); err != nil {
		_ = runtime.store.RemoveHibernated([]string{sess.ID})
		fail(err)
		return
	}
	sess.Status = status.Dead
	result.Hibernated = append(result.Hibernated, runtime.sessionInfo(sess, false, false))
}

// WakeHibernated settles the hibernated set: it relaunches every hibernated
// row with mail waiting, and forgets rows that came back some other way,
// were archived or are gone. The board calls it when
// store.HibernatedToSettle names anything, which covers mail that reached
// the queue without going through a send: a child's question or report to a
// hibernated parent is written straight to the store.
func (s *Sessions) WakeHibernated() (woken []string, err error) {
	defer start("sessioncmd.wake_hibernated").done(&err)
	runtime, err := s.open()
	if err != nil {
		return nil, err
	}
	defer runtime.store.Close()
	ids, err := runtime.store.HibernatedToSettle()
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, id := range ids {
		row, err := runtime.store.Get(id)
		if errors.Is(err, store.ErrSessionGone) || (err == nil && (row.Archived || row.Status != status.Dead)) {
			errs = append(errs, runtime.store.RemoveHibernated([]string{id}))
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.wake(runtime, row); err != nil {
			// A row that cannot come back (its directory was removed, its
			// tool left the config) would otherwise be retried on every
			// pass and hold its mail forever. It stops being hibernated, and
			// its queue is resolved the way an ended session's is: each
			// sender is told which of its messages went.
			errs = append(errs, fmt.Errorf("session %s could not be woken and is no longer hibernated: %w", id, err))
			if err := runtime.store.RemoveHibernated([]string{id}); err != nil {
				errs = append(errs, err)
			} else if _, err := runtime.store.ResolveEndedRecipient(id, time.Now()); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		woken = append(woken, id)
	}
	return woken, errors.Join(errs...)
}

// wakeForSend relaunches targetID if it is hibernated, whoever is sending:
// a hibernated row is resting, and the message is what it rests until. It
// returns what it did for the send's result, or "" when the target is not
// hibernated.
func (s *Sessions) wakeForSend(runtime *runtime, targetID string) (string, error) {
	hibernated, err := runtime.store.IsHibernated(targetID)
	if err != nil || !hibernated {
		return "", err
	}
	target, err := runtime.agent(targetID)
	if err != nil || target.Archived {
		return "", nil
	}
	if err := s.wake(runtime, target); err != nil {
		return "", fmt.Errorf("session %s is hibernated and could not be woken to take this message: %w", target.ID, err)
	}
	return fmt.Sprintf("woke hibernated session %s on its conversation", target.ID), nil
}

// wake relaunches a hibernated row on its own conversation. relaunch takes
// it out of the set once the pane is up.
func (s *Sessions) wake(runtime *runtime, target store.Session) error {
	if err := runtime.reach(target); err != nil {
		return err
	}
	if runtime.driver.Exists(target.ID) {
		return runtime.store.RemoveHibernated([]string{target.ID})
	}
	slot, err := s.pace(runtime, target.ID, true)
	if err != nil {
		return err
	}
	_, err = s.relaunch(runtime, target, "", slot)
	return err
}
