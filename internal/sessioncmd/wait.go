// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package sessioncmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tracing"
)

// Waiting on another session is one parked tool call instead of a polling
// loop that reads a pane, and a screen read per tick is what makes an
// orchestrating agent expensive.
//
// Waiting on a set is the same trade made once for a whole fan-out. A
// coordinator on this board holds 44 children; one blocking call per child
// is not a strategy, and it was the only shape on offer. This call parks on
// the set, ends on the first arrival, and hands back where every member of
// the set stands -- so the parent that learns child three finished also
// learns what the other four are doing, without paying for a list.
const (
	// DefaultWaitTimeout sits under the 60s Codex CLI cuts a call at, so an
	// ordinary wait answers inside every client's own patience.
	DefaultWaitTimeout = 50 * time.Second
	// MaxWaitTimeout is what a client tolerates, not what this loop could
	// sit through. Callers cut the call at around 120s, and the 5 minute
	// ceiling this used to advertise was a maximum nobody could reach: 898
	// of 1127 waits on this board were backgrounded by the calling harness
	// instead, 33.2 hours parked, collected through 1729 notification turns
	// that each cost the manager a turn to read. A ceiling the client will
	// not honour buys a background task and a round trip rather than a
	// longer wait, so this is the longest one that still comes back as an
	// answer. A parent that needs more waits again, or waits on the whole
	// set and lets the first arrival end it.
	MaxWaitTimeout = 2 * time.Minute
	minWaitPoll    = 500 * time.Millisecond

	WaitReached  = "reached"
	WaitTimedOut = "timed_out"
	WaitDied     = "died"
	// existsEvery keeps the liveness check off most ticks, since it forks a
	// tmux process, while the status read is a cheap indexed lookup. One
	// scan answers for the whole set, so that cost does not grow with it.
	existsEvery = 4
)

// restingStates is what "the session stopped working" means. Finished is
// rewritten to idle once the manager acknowledges it, and a manager tick
// can pass through both between two polls, so waiting on the whole set is
// the only way not to miss the moment.
var restingStates = []string{status.Finished, status.Waiting, status.Idle, status.Errored, status.Dead}

// WaitOptions says what one wait is parked on. Children is the set a parent
// means when it asks whether its wave is done; naming ids is for a subset of
// it, or for sessions the caller did not spawn -- this call has never
// required the target be a child.
type WaitOptions struct {
	SessionIDs []string
	Children   bool
	Until      []string
	Timeout    time.Duration
}

// WaitStanding is one waited-on session as the wait left it. The outcomes
// are the call's own three read per session: timed_out means this one had
// not arrived when the call returned, and Session.Status says what it was
// doing instead.
type WaitStanding struct {
	Session Session `json:"session" jsonschema:"the waited-on session as it stood when the wait returned"`
	Outcome string  `json:"outcome" jsonschema:"reached (it arrived at an awaited state), died (its pane is gone and dead was not among them), or timed_out (it had not arrived yet; session.status is what it was doing)"`
}

// knownStates is every state a row can hold. Both the wait states and the
// list's status filter are read against it, so a state added to one front
// is never accepted by one caller and refused by the other.
var knownStates = map[string]bool{
	status.Starting: true, status.Working: true, status.Waiting: true,
	status.Finished: true, status.Idle: true, status.Errored: true, status.Dead: true,
}

func normalizeState(raw string) (string, error) {
	state := strings.ToLower(strings.TrimSpace(raw))
	if !knownStates[state] {
		valid := make([]string, 0, len(knownStates))
		for name := range knownStates {
			valid = append(valid, name)
		}
		sort.Strings(valid)
		return "", fmt.Errorf("unknown state %q; valid states are %s", raw, strings.Join(valid, ", "))
	}
	return state, nil
}

type WaitResult struct {
	Session      Session        `json:"session" jsonschema:"the session that decided this call: the one that arrived, or died, or the single target that did neither"`
	Reached      bool           `json:"reached" jsonschema:"true when at least one waited-on session reached one of the awaited states"`
	Outcome      string         `json:"outcome" jsonschema:"reached (one of them arrived at an awaited state), died (one died and none arrived), or timed_out"`
	Waited       string         `json:"waited" jsonschema:"how long the wait lasted"`
	ManagerAwake bool           `json:"manager_awake" jsonschema:"whether Gate Inbox is running; session status only advances while it is, so a wait against a closed manager can only ever observe dead"`
	Standing     []WaitStanding `json:"standing" jsonschema:"where every waited-on session stood when this returned, so a fan-out never needs a follow-up list to find out what it is still waiting for"`
}

func normalizeWaitStates(until []string) ([]string, error) {
	if len(until) == 0 {
		return restingStates, nil
	}
	seen := map[string]bool{}
	states := make([]string, 0, len(until))
	for _, raw := range until {
		state, err := normalizeState(raw)
		if err != nil {
			return nil, err
		}
		if !seen[state] {
			seen[state] = true
			states = append(states, state)
		}
	}
	return states, nil
}

// Wait parks until one of the target sessions reaches one of the given
// states, or the timeout expires. A timeout is an outcome rather than a
// failure: the caller gets each session's current state and decides what to
// do with it.
//
// The first arrival ends the call for the whole set, which is what makes
// this usable as a loop: park, act on whoever moved, park again on what is
// left. So does the set running out: every target settled, whether it
// arrived or died.
//
// Those two are the whole rule, and a death is deliberately not a third.
// Over one target "everything settled" is "that one died", so a single wait
// still returns the moment its session's pane goes -- the status of a
// session with no pane will never move again. Over five it is not: one
// child dying is no reason to stop waiting on the four still working, and
// ending there would make a children wait useless the moment a wave left a
// dead row behind. It would return that same death instantly, forever. The
// death is reported in the standing whenever the call does return.
func (s *Sessions) Wait(ctx context.Context, sessionID string, opts WaitOptions) (result WaitResult, err error) {
	timeout := opts.Timeout
	// This one is long on purpose, so its duration says nothing on its own;
	// the outcome is what makes the span worth having. A board where waits
	// end in timed_out rather than reached is one whose poller is not
	// advancing statuses, and nothing else reports that in aggregate.
	op := start("sessioncmd.wait", sessionAttr(sessionID))
	defer func() {
		op.done(&err,
			tracing.Attr{Key: "outcome", Value: result.Outcome},
			tracing.Attr{Key: "targets", Value: len(result.Standing)},
			tracing.Attr{Key: "timeout", Value: timeout})
	}()
	states, err := normalizeWaitStates(opts.Until)
	if err != nil {
		return WaitResult{}, err
	}
	switch {
	case timeout < 0 || timeout > MaxWaitTimeout:
		return WaitResult{}, fmt.Errorf("timeout %s is outside 0 to %s; wait again when this one returns", timeout, MaxWaitTimeout)
	case timeout == 0:
		timeout = DefaultWaitTimeout
	}
	runtime, err := s.open()
	if err != nil {
		return WaitResult{}, err
	}
	defer runtime.store.Close()
	caller, err := runtime.caller(sessionID)
	if err != nil {
		return WaitResult{}, err
	}
	targets, err := runtime.waitTargets(caller, opts)
	if err != nil {
		return WaitResult{}, err
	}

	wanted := map[string]bool{}
	for _, state := range states {
		wanted[state] = true
	}
	poll := runtime.cfg.PollInterval.Duration
	if poll < minWaitPoll {
		poll = minWaitPoll
	}
	started := time.Now()
	deadline := started.Add(timeout)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	// Waking only on the poll would overshoot a timeout shorter than the
	// interval, which is the one thing a caller asked this call to bound.
	expiry := time.NewTimer(time.Until(deadline))
	defer expiry.Stop()

	// The liveness reading, carried between ticks so a scan that failed can
	// leave the last one standing. An id missing from this map reads as a
	// death, and inventing one for a session that is running is the single
	// answer this call must never give.
	live := make(map[string]bool, len(targets))
	for _, target := range targets {
		live[target.ID] = true
	}

	for tick := 0; ; tick++ {
		current := make([]store.Session, 0, len(targets))
		for _, target := range targets {
			// The manager writes status only for a session it can still
			// see, so a pane that vanished reads dead here before the next
			// poll lands.
			sess, err := runtime.agent(target.ID)
			if err != nil {
				return WaitResult{}, err
			}
			current = append(current, sess)
		}
		if tick%existsEvery == 0 {
			runtime.refreshLive(live)
		}
		seen, outcome, settled := observeWait(current, live, wanted)
		if outcome == WaitReached || settled == len(seen) || !time.Now().Before(deadline) {
			// The cheap ticks trust the last scan; the answer we hand back
			// is worth one more fork to get right.
			runtime.refreshLive(live)
			seen, outcome, _ = observeWait(current, live, wanted)
			return runtime.waitResult(seen, outcome, started)
		}
		select {
		case <-ctx.Done():
			return WaitResult{}, ctx.Err()
		case <-ticker.C:
		case <-expiry.C:
		}
	}
}

// waitObservation is one target read on one tick, before it becomes the
// Session record the caller sees -- which costs a tmux call per running
// session and is only worth paying once, on the way out.
type waitObservation struct {
	session store.Session
	running bool
	state   string
	outcome string
}

// observeWait reads the whole set against what was awaited and returns the
// call's own outcome with it, plus how many of the set have stopped moving.
// The outcome is the best standing in the set, so a death is still what a
// caller is told about when nothing arrived; the count is what says the set
// is spent, and only that ends a wait the arrival did not.
func observeWait(current []store.Session, live, wanted map[string]bool) ([]waitObservation, string, int) {
	seen := make([]waitObservation, 0, len(current))
	overall := WaitTimedOut
	settled := 0
	for _, sess := range current {
		running := live[sess.ID]
		state := sess.Status
		if !running {
			state = status.Dead
		}
		observed := waitObservation{session: sess, running: running, state: state, outcome: WaitTimedOut}
		switch {
		case wanted[state]:
			observed.outcome = WaitReached
		case !running:
			observed.outcome = WaitDied
		}
		if observed.outcome != WaitTimedOut {
			settled++
		}
		if observed.outcome == WaitReached || (observed.outcome == WaitDied && overall == WaitTimedOut) {
			overall = observed.outcome
		}
		seen = append(seen, observed)
	}
	return seen, overall, settled
}

// waitTargets resolves what this call parks on. A children wait reads
// parent_id as the board stores it, so a parent's set is exactly what the
// board says it spawned.
func (r *runtime) waitTargets(caller store.Session, opts WaitOptions) ([]store.Session, error) {
	if opts.Children {
		if len(opts.SessionIDs) > 0 {
			return nil, errors.New("wait on children or on named sessions, not both; children is every session you spawned")
		}
		return r.callerChildren(caller)
	}
	if len(opts.SessionIDs) == 0 {
		return nil, fmt.Errorf(
			"nothing to wait on; name the sessions by id from %s, or wait on children to park on every session you spawned",
			r.words.ListSessions)
	}
	seen := map[string]bool{}
	targets := make([]store.Session, 0, len(opts.SessionIDs))
	for _, id := range opts.SessionIDs {
		target, err := r.agent(id)
		if err != nil {
			return nil, err
		}
		if target.ID == caller.ID {
			return nil, errors.New("a session cannot wait on itself; it is the one making this call")
		}
		if target.Archived {
			return nil, fmt.Errorf("session %s is archived, so its status no longer advances; restore it with %s first", target.ID, r.words.Restore)
		}
		if seen[target.ID] {
			continue
		}
		seen[target.ID] = true
		targets = append(targets, target)
	}
	return targets, nil
}

// callerChildren is the caller's fan-out as the board holds it. An archived
// child is left out rather than refused the way a named one is: it is no
// longer part of the wave, and one filed away should not stop a wait on the
// four still running. A terminal is a shell, never an agent with a turn to
// end.
func (r *runtime) callerChildren(caller store.Session) ([]store.Session, error) {
	sessions, err := r.store.ListSessions(true)
	if err != nil {
		return nil, err
	}
	children := make([]store.Session, 0, len(sessions))
	for _, child := range sessions {
		if child.ParentID != caller.ID || child.Archived || r.cfg.Tools[child.Tool].Shell {
			continue
		}
		children = append(children, child)
	}
	if len(children) == 0 {
		return nil, errors.New(
			"this session has no children to wait on; spawn them first, or name the sessions to wait on by id")
	}
	return children, nil
}

// refreshLive replaces the liveness reading in place when tmux answers. One
// scan covers the whole set, adopted panes on other servers included, so
// this costs a fork per tick rather than a fork per child. A scan that
// failed is not an answer about anybody, so the previous reading stands and
// the wait is late rather than wrong.
func (r *runtime) refreshLive(live map[string]bool) {
	panes, err := r.driver.Panes()
	if err != nil {
		logging.Info("wait could not read pane liveness", "error", err)
		return
	}
	for id := range live {
		_, running := panes[id]
		live[id] = running
	}
}

func (r *runtime) waitResult(seen []waitObservation, outcome string, started time.Time) (WaitResult, error) {
	awake, err := r.managerAwake(time.Now())
	if err != nil {
		return WaitResult{}, err
	}
	result := WaitResult{
		Reached:      outcome == WaitReached,
		Outcome:      outcome,
		Waited:       time.Since(started).Round(time.Second).String(),
		ManagerAwake: awake,
		Standing:     make([]WaitStanding, 0, len(seen)),
	}
	for _, observed := range seen {
		info := r.sessionInfo(observed.session, observed.running, false)
		info.Status = observed.state
		result.Standing = append(result.Standing, WaitStanding{Session: info, Outcome: observed.outcome})
	}
	// Session is whichever one decided the call, so a single-target wait
	// answers exactly as it always did and a fan-out leads with the session
	// that moved rather than an arbitrary member of the set.
	result.Session = result.Standing[0].Session
	for _, standing := range result.Standing {
		if standing.Outcome == outcome {
			result.Session = standing.Session
			break
		}
	}
	return result, nil
}
