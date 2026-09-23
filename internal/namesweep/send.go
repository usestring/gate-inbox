package namesweep

import (
	"time"

	"github.com/usestring/gate-inbox/internal/promptcache"
)

// Gate re-answers both questions for one pane. Build runs on a list, then a
// person reads it and approves it, and the sweep then staggers its sends over
// minutes: by the time any one message is about to land, the answers Build
// wrote down are minutes old. A pane that took a permission dialog in that
// window is exactly the pane the safe gate exists for, so both gates are
// asked again immediately before each keystroke rather than trusted from the
// list.
type Gate interface {
	// Status is the pane's status right now. An error means the pane could
	// not be read, which is never a reason to type into it.
	Status(target Verdict) (string, error)
	// Cache is the session's prompt-cache state right now.
	Cache(target Verdict) promptcache.State
}

// Result is what a run of the sweep did.
type Result struct {
	Sent []Verdict
	// Held is the targets a gate closed on at the door, each carrying the
	// reason the re-check gave rather than the one the plan did.
	Held []Verdict
	// Err is the first send that failed. The sweep stops there: a tmux error
	// mid-run means the next ninety are unlikely to fare better.
	Err error
	// Stopped marks a sweep the operator ended part way through. The panes
	// already sent to keep their message; the rest are simply not in Held,
	// because nothing was decided about them.
	Stopped bool
}

// Send types the directive into each approved pane, re-checking both gates
// first and pausing pace between panes. Sending ninety messages at once is a
// thundering herd on ninety real agents, so the stagger is not politeness --
// it is what keeps the machine usable while the sweep runs.
func Send(targets []Verdict, gate Gate, send func(id, text string) error, now func() time.Time, pace time.Duration, sleep func(time.Duration), stop <-chan struct{}) Result {
	var result Result
	for i, target := range targets {
		if i > 0 && pace > 0 {
			sleep(pace)
		}
		select {
		case <-stop:
			result.Stopped = true
			return result
		default:
		}
		state, err := gate.Status(target)
		if err != nil {
			target.Reason = "unreadable: the pane gave no answer at send time"
			result.Held = append(result.Held, target)
			continue
		}
		if !atRestingPrompt(state) {
			target.Reason = "changed since the plan — " + unsafeReason(state)
			result.Held = append(result.Held, target)
			continue
		}
		cache := gate.Cache(target)
		if !stillWarm(cache, now()) {
			target.Cache = cache
			target.Reason = "changed since the plan — " + coldReason(cache, now())
			result.Held = append(result.Held, target)
			continue
		}
		if err := send(target.ID, target.Directive); err != nil {
			result.Err = err
			return result
		}
		result.Sent = append(result.Sent, target)
	}
	return result
}
