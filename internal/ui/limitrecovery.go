package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/usestring/gate-inbox/internal/band"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/tmux"
)

const limitResetGrace = time.Minute

const capacityRetryDelay = 10 * time.Second
const capacityRetryMaxDelay = 8 * time.Minute

// A shared account can unblock the whole fleet at once. The pastes no longer
// run on the pass (see asyncsend.go), so this bounds how many continuations
// a single pass may claim rather than how long it may stall for them: a
// reset burst still reaches the fleet over a few passes rather than all at
// once.
const limitRecoveriesPerPass = 10
const limitResumePrompt = band.Tag + " The reported usage-limit reset time has passed. Continue the interrupted task from where you stopped, including checking the progress of managed subagents. This is an automatic continuation, not new authorization; preserve existing approval requirements."

// capture is the pass's own read of the pane, which clean is the stripped
// text of: the two typing gates below take the caret and the activity stamp
// off it rather than forking a tmux apiece for them. See composerCarriesDraft.
func (p *poller) maybeRecoverLimit(sess store.Session, states map[string]store.LimitRecovery, capture tmux.Capture, clean, derived string, agentAlive, inputAvailable bool, now time.Time) (bool, error) {
	// Claude owns its reset timer; a second scheduler can duplicate its continuation.
	if sess.Tool != "codex" {
		return false, nil
	}
	previous, exists := states[sess.ID]
	if !exists && derived != status.Errored {
		return false, nil
	}
	if sess.Archived || !agentAlive || p.engine.ViewportDisplaced(sess.Tool, clean) {
		return false, nil
	}
	var old *store.LimitRecovery
	if exists {
		old = &previous
	}
	banner := p.engine.LimitBanner(sess.Tool, clean)
	if banner == "" {
		if exists && derived != status.Errored && (previous.CapacityDelay == 0 || derived == status.Finished || derived == status.Idle) {
			_, err := p.store.CompareLimitRecovery(sess, old, nil)
			return false, err
		}
		return false, nil
	}
	capacity := p.engine.CapacityBanner(sess.Tool, clean) != ""
	var next *store.LimitRecovery
	if capacity {
		region, _ := p.engine.ActivityRegion(sess.Tool, clean)
		next = nextCapacityRecovery(sess, previous, exists, banner, region, now)
	} else if !exists || previous.Banner != banner || !previous.LaunchAt.Equal(sess.LaunchTime()) {
		resetAt, ok := status.LimitReset(banner, now)
		if !ok {
			return false, nil
		}
		next = &store.LimitRecovery{Banner: banner, ResetAt: resetAt, LaunchAt: sess.LaunchTime()}
	}
	if next != nil {
		saved, err := p.store.CompareLimitRecovery(sess, old, next)
		if err != nil || !saved {
			return false, err
		}
		previous = *next
		old = &previous
		logging.Info("model availability recovery scheduled", "session", sess.ID, "retry_at", recoveryDeadline(previous), "capacity_delay", previous.CapacityDelay)
	}
	if derived != status.Errored || !inputAvailable || p.offline() || !previous.AttemptedAt.IsZero() || now.Before(recoveryDeadline(previous)) {
		return false, nil
	}
	if p.engine.TypingHold(sess.Tool, clean) != "" || p.operatorEcho(sess.ID) || p.operatorTyping(sess, capture.State) {
		return false, nil
	}
	typing, err := p.composerCarriesDraft(sess, capture)
	if err != nil || typing {
		return false, err
	}
	key := limitSend(sess.ID)
	if !p.reserveSend(key) {
		return false, nil
	}
	claimedState := previous
	claimedState.AttemptedAt = now
	prompt := limitResumePrompt
	if capacity {
		claimedState.CapacityRetries++
		prompt = band.Tag + " " + capacityRetryMarker(claimedState) + " Continue the interrupted task from where you stopped. Preserve existing approval requirements."
	}
	claimed, err := p.store.CompareLimitRecovery(sess, old, &claimedState)
	if err != nil || !claimed {
		p.releaseSend(key)
		return false, err
	}
	// AttemptedAt is written before the paste goes out, so no second pass
	// can reach this however long the pane takes to draw it; the send only
	// has to say whether it landed.
	p.runSend(sess, key, prompt, func(err error) error {
		if err != nil {
			return fmt.Errorf("model availability continuation for %s was not confirmed; automatic retry suppressed to avoid duplicate input: %w", sess.Name, err)
		}
		logging.Info("model availability continuation sent", "session", sess.ID, "capacity_retry", claimedState.CapacityRetries, "final_capacity_retry", claimedState.CapacityDelay == capacityRetryMaxDelay)
		return nil
	})
	return true, nil
}

func capacityRetryMarker(state store.LimitRecovery) string {
	return fmt.Sprintf("Capacity retry %d [%x].", state.CapacityRetries, state.ResetAt.UnixNano())
}

func recoveryDeadline(state store.LimitRecovery) time.Time {
	if state.CapacityDelay > 0 {
		return state.ResetAt
	}
	return state.ResetAt.Add(limitResetGrace)
}

func nextCapacityRecovery(sess store.Session, previous store.LimitRecovery, exists bool, banner, region string, now time.Time) *store.LimitRecovery {
	if !exists || previous.CapacityDelay == 0 || !previous.LaunchAt.Equal(sess.LaunchTime()) {
		return &store.LimitRecovery{Banner: banner, ResetAt: now.Add(capacityRetryDelay), LaunchAt: sess.LaunchTime(), CapacityDelay: capacityRetryDelay}
	}
	// An unchanged error is not another failed attempt. The submitted retry
	// must precede the newest banner, even if its working frame was missed.
	// Its deadline distinguishes it from retries in earlier capacity episodes.
	if previous.AttemptedAt.IsZero() || previous.CapacityDelay >= capacityRetryMaxDelay ||
		!strings.Contains(strings.Join(strings.Fields(region), " "), capacityRetryMarker(previous)) {
		return nil
	}
	next := previous
	next.Banner = banner
	next.CapacityDelay = min(previous.CapacityDelay*2, capacityRetryMaxDelay)
	next.ResetAt = now.Add(next.CapacityDelay)
	next.AttemptedAt = time.Time{}
	return &next
}

func heldLimitContinuations(sessions []store.Session, recoveries map[string]store.LimitRecovery, now time.Time) map[string]bool {
	held := make(map[string]bool)
	for _, sess := range sessions {
		recovery, ok := recoveries[sess.ID]
		if ok && sess.Tool == "codex" && sess.Status == status.Errored && !sess.Archived &&
			recovery.LaunchAt.Equal(sess.LaunchTime()) && recovery.AttemptedAt.IsZero() &&
			!now.Before(recoveryDeadline(recovery)) {
			held[sess.ID] = true
		}
	}
	return held
}
