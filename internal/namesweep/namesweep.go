// Package namesweep decides which running agents may be asked to name
// themselves, and refuses the rest.
//
// The mechanism is a sentence typed into a live pane, which is the same
// mechanism as answering a permission dialog and the same mechanism as
// starting a paid turn. So the whole package is two gates and the reasons it
// gives for closing them: a pane must be at an idle prompt, and its prompt
// cache must still be warm. Everything a gate refuses is kept and reported
// rather than dropped -- the skips are what tells an operator whether the
// sweep understood their board.
package namesweep

import (
	"fmt"
	"time"

	"github.com/usestring/gate-inbox/internal/promptcache"
	"github.com/usestring/gate-inbox/internal/status"
)

// Candidate is one session as the sweep needs to see it.
type Candidate struct {
	ID     string
	Name   string
	Tool   string
	Cwd    string
	Status string
	// AgentSessionID is the conversation id when one is known, which names
	// the transcript exactly. Adopted panes have none.
	AgentSessionID string
	// Adopted marks a pane the manager did not start. Only these are swept:
	// a session the manager launched already carried the rename directive in
	// its first prompt or as its first message.
	Adopted bool
	// CacheReadable marks a tool whose prompt cache this program can price.
	// Only Claude Code writes the numbers; every other tool is unpriceable,
	// and unpriceable is treated as expensive.
	CacheReadable bool
	Archived      bool
	Shell         bool
}

// Verdict is one candidate and what the gates decided about it.
type Verdict struct {
	Candidate
	Cache promptcache.State
	// Send is true only when both gates opened.
	Send bool
	// Reason says why, in the words an operator needs: what would have
	// happened, not which branch was taken.
	Reason string
	// Directive is the exact text that would be typed into this pane.
	Directive string
}

// Plan is the dry run: what would be sent, what would not, and why.
type Plan struct {
	Targets []Verdict
	Skipped []Verdict
	// OutOfScope counts the sessions the sweep never considered -- managed,
	// archived or shell rows -- so the totals on screen add up.
	OutOfScope int
}

func (p Plan) Empty() bool { return len(p.Targets) == 0 }

// Gate 1 -- SAFE. Typing into a pane that is not at an idle prompt makes the
// message that pane's answer, and a pane showing a permission dialog would
// have the directive approve or reject it. Idle is the only status that means
// nothing is waiting on this keystroke: working, waiting, starting, errored
// and dead each mean something is, so none of them is added here for
// convenience later.
func atRestingPrompt(state string) bool {
	return state == status.Idle
}

// Gate 2 -- WARM. A message typed into a session whose prompt cache has
// expired re-creates the whole context at full price, and these sessions
// carry hundreds of thousands of tokens each. The transcript says which
// lifetime the last turn bought and when it started, so the margin is what
// keeps a session that expires between the plan and the send out of the set.
func stillWarm(state promptcache.State, now time.Time) bool {
	return state.Warm(now, promptcache.Margin(state.TTL))
}

// Build runs both gates over the candidates. cache reads one session's
// transcript and directive renders the text that session would receive;
// both are supplied by the caller so this package touches neither disk nor
// tmux and the gates can be tested on their own.
func Build(candidates []Candidate, cache func(Candidate) promptcache.State, directive func(Candidate) string, now time.Time) Plan {
	var plan Plan
	// A transcript two sessions both resolve to identifies neither of them,
	// so its warmth is somebody else's. Counted first, over the whole set,
	// because the collision is a property of the pair rather than of a row.
	shared := map[string]int{}
	states := make(map[string]promptcache.State, len(candidates))
	for _, candidate := range candidates {
		if !inScope(candidate) {
			continue
		}
		state := cache(candidate)
		states[candidate.ID] = state
		if state.Transcript != "" {
			shared[state.Transcript]++
		}
	}

	for _, candidate := range candidates {
		if !inScope(candidate) {
			plan.OutOfScope++
			continue
		}
		state := states[candidate.ID]
		verdict := Verdict{Candidate: candidate, Cache: state}
		switch {
		case !atRestingPrompt(candidate.Status):
			verdict.Reason = unsafeReason(candidate.Status)
		case !candidate.CacheReadable:
			verdict.Reason = fmt.Sprintf("unpriceable: %s keeps no readable cache record, so a send could be a cold one", candidate.Tool)
		case !state.Known:
			verdict.Reason = "unpriceable: no transcript found for " + candidate.Cwd
		case shared[state.Transcript] > 1:
			verdict.Reason = "ambiguous: another pane in " + candidate.Cwd + " resolves to the same transcript"
		case !stillWarm(state, now):
			verdict.Reason = coldReason(state, now)
		default:
			verdict.Send = true
			verdict.Reason = warmReason(state, now)
			verdict.Directive = directive(candidate)
		}
		if verdict.Send {
			plan.Targets = append(plan.Targets, verdict)
		} else {
			plan.Skipped = append(plan.Skipped, verdict)
		}
	}
	return plan
}

// inScope is what the sweep is for: live agent panes the manager adopted.
// A managed session was told to name itself when it launched, a shell has no
// agent to ask, and an archived row has no pane to type into.
func inScope(candidate Candidate) bool {
	return candidate.Adopted && !candidate.Archived && !candidate.Shell
}

func unsafeReason(state string) string {
	switch state {
	case status.Waiting:
		return "waiting: the directive would answer whatever dialog is on screen"
	case status.Working:
		return "working: mid-turn, the directive would interrupt it"
	case status.Starting:
		return "starting: the agent has not drawn its prompt yet"
	case status.Dead:
		return "dead: nothing is running in the pane"
	case status.Errored:
		return "errored: the pane is showing a failure, not a prompt"
	case StateTyping:
		return "typing: somebody has a line part written at that prompt"
	default:
		return state + ": not at a resting prompt"
	}
}

func coldReason(state promptcache.State, now time.Time) string {
	ttl := shortDuration(state.TTL)
	age := state.Age(now)
	expired := age - state.TTL
	if expired >= 0 {
		return fmt.Sprintf("cold: %s TTL expired %s ago, %s ctx", ttl, shortDuration(expired), Tokens(state.ContextTokens))
	}
	return fmt.Sprintf("expiring: %s TTL has %s left, inside the safety margin, %s ctx",
		ttl, shortDuration(-expired), Tokens(state.ContextTokens))
}

func warmReason(state promptcache.State, now time.Time) string {
	return fmt.Sprintf("warm: %s TTL, last turn %s ago, %s ctx",
		shortDuration(state.TTL), shortDuration(state.Age(now)), Tokens(state.ContextTokens))
}

// Tokens renders a context size the way it is talked about.
func Tokens(count int) string {
	switch {
	case count >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(count)/1_000_000)
	case count >= 1_000:
		return fmt.Sprintf("%dk", count/1_000)
	default:
		return fmt.Sprintf("%d", count)
	}
}

// shortDuration is a duration at the one unit that matters, since these are
// read at a glance in a table.
func shortDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%.0fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0fm", d.Minutes())
	default:
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
}

// StateTyping is the sweep's own name for a pane whose prompt already has a
// half-written line on it. No status engine produces it: a person typing is
// not a state a pane advertises, but the send ends in Enter, so their line
// would be submitted mixed with ours. It is refused exactly like a dialog.
const StateTyping = "typing"
